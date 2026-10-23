package cache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
)

func newCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return New(rdb, config.Cache{Enabled: true, TTL: time.Minute, OnlyWhenTemperatureZero: true, MaxEntryBytes: 1024}, 100*time.Millisecond, nil), mr
}

func req(t *testing.T, s string) *api.ChatRequest {
	t.Helper()
	var r api.ChatRequest
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func TestKeyCanonical(t *testing.T) {
	c, _ := newCache(t)
	a, ok1 := c.Key("p1", req(t, `{"model":"m","temperature":0,"messages":[{"role":"user","content":"hi"}],"top_p":1}`))
	b, ok2 := c.Key("p1", req(t, `{ "top_p": 1, "messages": [ {"content":"hi","role":"user"} ], "temperature": 0, "model": "m", "stream": true, "user": "u1" }`))
	if !ok1 || !ok2 || a != b {
		t.Fatalf("same request must give same key: %q %q", a, b)
	}
	other, _ := c.Key("p2", req(t, `{"model":"m","temperature":0,"messages":[{"role":"user","content":"hi"}],"top_p":1}`))
	if other == a {
		t.Fatal("different projects must not share cache entries")
	}
	changed, _ := c.Key("p1", req(t, `{"model":"m","temperature":0,"messages":[{"role":"user","content":"hi!"}],"top_p":1}`))
	if changed == a {
		t.Fatal("different prompts must give different keys")
	}
}

func TestKeyEligibility(t *testing.T) {
	c, _ := newCache(t)
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}]}`,                   // temperature unset (defaults to 1)
		`{"model":"m","temperature":0.7,"messages":[{"role":"user","content":"x"}]}`, // non-deterministic
		`{"model":"m","temperature":0,"n":2,"messages":[{"role":"user","content":"x"}]}`,
	} {
		if _, ok := c.Key("p", req(t, body)); ok {
			t.Errorf("must not be cacheable: %s", body)
		}
	}
	var nilCache *Cache
	if _, ok := nilCache.Key("p", req(t, `{"model":"m","temperature":0,"messages":[]}`)); ok {
		t.Error("nil cache must disable caching")
	}
}

func TestPutGet(t *testing.T) {
	c, mr := newCache(t)
	e := &Entry{Target: "a/m", Model: "m", Content: "hello world", FinishReason: "stop", Usage: api.Usage{PromptTokens: 3, CompletionTokens: 2}}
	if !c.Put("cache:k", e) {
		t.Fatal("put rejected")
	}
	deadline := time.Now().Add(time.Second)
	for !mr.Exists("cache:k") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ttl := mr.TTL("cache:k"); ttl != time.Minute {
		t.Errorf("ttl = %s", ttl)
	}
	got, err := c.Get(context.Background(), "cache:k")
	if err != nil || got == nil || got.Content != "hello world" || got.Usage.CompletionTokens != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if miss, err := c.Get(context.Background(), "cache:nope"); miss != nil || err != nil {
		t.Fatalf("miss = %+v, %v", miss, err)
	}
	if c.Put("cache:big", &Entry{Content: strings.Repeat("x", 2000)}) {
		t.Error("oversized entry must be skipped")
	}
}

func TestReplayChunks(t *testing.T) {
	e := &Entry{Model: "m", Content: strings.Repeat("abcde", 9), FinishReason: "stop", Usage: api.Usage{CompletionTokens: 9}}
	chunks := e.Chunks(true)
	var text strings.Builder
	var sawUsage, sawFinish bool
	for _, b := range chunks {
		var c api.ChatChunk
		if err := json.Unmarshal(b, &c); err != nil {
			t.Fatal(err)
		}
		text.WriteString(c.ContentText())
		if c.Usage != nil {
			sawUsage = true
		}
		if len(c.Choices) == 1 && c.Choices[0].FinishReason != nil {
			sawFinish = true
		}
	}
	if text.String() != e.Content || !sawUsage || !sawFinish || len(chunks) != 1+3+1+1 {
		t.Fatalf("replay wrong: %d chunks, text %q usage %v finish %v", len(chunks), text.String(), sawUsage, sawFinish)
	}
	if len(e.Chunks(false)) != 5 {
		t.Error("usage chunk must be omitted unless requested")
	}
}
