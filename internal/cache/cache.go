// Package cache is an exact-match response cache in Redis.
//
// Keys are scoped per project (one tenant can never read another's cached
// answers) and derived from a canonical form of the request, so field order
// and formatting don't matter. Only deterministic requests are cached.
package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/metrics"
)

// Entry is a cached completion. Raw holds the original non-streaming body
// when available; Content is used to synthesize responses and streams.
type Entry struct {
	Target       string    `json:"target"`
	Model        string    `json:"model"`
	Content      string    `json:"content"`
	FinishReason string    `json:"finish_reason"`
	Usage        api.Usage `json:"usage"`
	Raw          []byte    `json:"raw,omitempty"`
}

// Cache wraps a Redis client.
type Cache struct {
	rdb     redis.UniversalClient
	cfg     config.Cache
	timeout time.Duration
	metrics *metrics.Metrics
}

// New returns a Cache. timeout bounds lookups on the hot path.
func New(rdb redis.UniversalClient, cfg config.Cache, timeout time.Duration, m *metrics.Metrics) *Cache {
	return &Cache{rdb: rdb, cfg: cfg, timeout: timeout, metrics: m}
}

// ignoredFields don't change the completion and are excluded from the key.
var ignoredFields = map[string]bool{"stream": true, "stream_options": true, "user": true}

// Key returns the cache key for req, or ok=false if req is not cacheable.
func (c *Cache) Key(projectID string, req *api.ChatRequest) (key string, ok bool) {
	if c == nil || !c.cfg.Enabled {
		return "", false
	}
	if c.cfg.OnlyWhenTemperatureZero && (req.Temperature == nil || *req.Temperature != 0) {
		return "", false
	}
	if n, ok := req.Extra["n"]; ok && string(n) != "1" {
		return "", false
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	for f := range ignoredFields {
		delete(m, f)
	}
	canonical, err := json.Marshal(m) // encoding/json sorts map keys
	if err != nil {
		return "", false
	}
	h := sha256.New()
	h.Write([]byte(projectID))
	h.Write([]byte{0})
	h.Write(canonical)
	return "cache:" + hex.EncodeToString(h.Sum(nil)), true
}

// Get looks up key. A miss returns (nil, nil); Redis errors are returned so
// the caller can treat them as a bypass.
func (c *Cache) Get(ctx context.Context, key string) (*Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	b, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		c.depErr()
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	plain, err := io.ReadAll(io.LimitReader(zr, int64(c.cfg.MaxEntryBytes)*4))
	if err != nil {
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(plain, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// Put stores e asynchronously. Entries larger than MaxEntryBytes are skipped.
// It reports whether the entry was accepted for storage.
func (c *Cache) Put(key string, e *Entry) bool {
	plain, err := json.Marshal(e)
	if err != nil || len(plain) > c.cfg.MaxEntryBytes {
		return false
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.rdb.Set(ctx, key, buf.Bytes(), c.cfg.TTL).Err(); err != nil {
			c.depErr()
		}
	}()
	return true
}

func (c *Cache) depErr() {
	if c.metrics != nil {
		c.metrics.DependencyErrors.WithLabelValues("redis").Inc()
	}
}

// Response builds a non-streaming response body for a hit.
func (e *Entry) Response() []byte {
	if len(e.Raw) > 0 {
		return e.Raw
	}
	finish := e.FinishReason
	u := e.Usage
	b, _ := json.Marshal(api.ChatResponse{
		ID: "chatcmpl-cache", Object: "chat.completion", Created: time.Now().Unix(), Model: e.Model,
		Choices: []api.Choice{{Index: 0, Message: api.TextMessage("assistant", e.Content), FinishReason: &finish}},
		Usage:   &u,
	})
	return b
}

// Chunks builds the stream payloads replaying a hit: a role chunk, content
// split into ~chunkSize pieces, a finish chunk and, if requested, usage.
func (e *Entry) Chunks(includeUsage bool) [][]byte {
	const chunkSize = 20
	created := time.Now().Unix()
	mk := func(d api.Delta, finish *string) []byte {
		b, _ := json.Marshal(api.ChatChunk{ID: "chatcmpl-cache", Object: "chat.completion.chunk", Created: created, Model: e.Model,
			Choices: []api.ChunkChoice{{Index: 0, Delta: d, FinishReason: finish}}})
		return b
	}
	out := [][]byte{mk(api.Delta{Role: "assistant"}, nil)}
	runes := []rune(e.Content)
	for i := 0; i < len(runes); i += chunkSize {
		out = append(out, mk(api.Delta{Content: string(runes[i:min(i+chunkSize, len(runes))])}, nil))
	}
	finish := e.FinishReason
	out = append(out, mk(api.Delta{}, &finish))
	if includeUsage {
		u := e.Usage
		b, _ := json.Marshal(api.ChatChunk{ID: "chatcmpl-cache", Object: "chat.completion.chunk", Created: created, Model: e.Model,
			Choices: []api.ChunkChoice{}, Usage: &u})
		out = append(out, b)
	}
	return out
}
