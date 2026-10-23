// Package openai implements provider.Provider for OpenAI and any
// OpenAI-compatible API (OpenRouter, Groq, Together, mockllm, …).
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/sse"
)

const maxResponseBytes = 16 << 20

// Provider talks to one OpenAI-compatible base URL.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	timeout time.Duration
	client  *http.Client
}

// New builds a provider with its own tuned HTTP transport.
func New(cfg config.Provider, apiKey string) *Provider {
	dialer := &net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.MaxIdleConnsPerHost * 2,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &Provider{
		name:    cfg.Name,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  apiKey,
		timeout: cfg.Timeout,
		client:  &http.Client{Transport: tr},
	}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) newRequest(ctx context.Context, req *api.ChatRequest, model string, stream bool) (*http.Request, error) {
	out := *req
	out.Model = model
	out.Stream = stream
	if stream {
		// Always ask for usage so the gateway can account tokens; the
		// usage-only chunk is filtered out if the client didn't ask for it.
		out.StreamOptions = &api.StreamOptions{IncludeUsage: true}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if stream {
		hr.Header.Set("Accept", "text/event-stream")
	}
	if p.apiKey != "" {
		hr.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	return hr, nil
}

func (p *Provider) do(parent context.Context, hr *http.Request) (*http.Response, error) {
	resp, err := p.client.Do(hr)
	if err != nil {
		return nil, provider.TransportError(parent, p.name, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, provider.StatusError(p.name, resp.StatusCode, errorMessage(body))
	}
	return resp, nil
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req *api.ChatRequest, model string) (*api.ChatResponse, error) {
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	hr, err := p.newRequest(cctx, req, model, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, provider.TransportError(ctx, p.name, err)
	}
	var out api.ChatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, &provider.Error{Provider: p.name, Status: resp.StatusCode, Code: provider.CodeBadResponse, Message: "invalid JSON from upstream", Retryable: true, Err: err}
	}
	out.Raw = body
	return &out, nil
}

// ChatStream implements provider.Provider.
func (p *Provider) ChatStream(ctx context.Context, req *api.ChatRequest, model string) (provider.Stream, error) {
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	hr, err := p.newRequest(cctx, req, model, true)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := p.do(ctx, hr)
	if err != nil {
		cancel()
		return nil, err
	}
	return &stream{p: p, parent: ctx, cancel: cancel, body: resp.Body, r: sse.NewReader(resp.Body)}, nil
}

type stream struct {
	p      *Provider
	parent context.Context
	cancel context.CancelFunc
	body   io.ReadCloser
	r      *sse.Reader
}

func (s *stream) Next() (*api.ChatChunk, error) {
	for {
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "stream ended without [DONE]"}
			}
			return nil, provider.TransportError(s.parent, s.p.name, err)
		}
		if len(bytes.TrimSpace(ev.Data)) == 0 {
			continue
		}
		if ev.IsDone() {
			return nil, io.EOF
		}
		var c api.ChatChunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "invalid chunk JSON", Err: err}
		}
		if c.Object == "" && len(c.Choices) == 0 && c.Usage == nil {
			if msg, ok := embeddedError(ev.Data); ok {
				return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeUpstream, Message: msg}
			}
		}
		c.Raw = ev.Data
		return &c, nil
	}
}

func (s *stream) Close() error {
	s.cancel()
	return s.body.Close()
}

// embeddedError reports whether a stream payload is an error object.
func embeddedError(data []byte) (string, bool) {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &e) != nil || len(e.Error) == 0 {
		return "", false
	}
	return errorMessage(data), true
}

// errorMessage extracts error.message from an OpenAI-style error body.
func errorMessage(body []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && len(e.Error) > 0 {
		var d api.ErrorDetail
		if json.Unmarshal(e.Error, &d) == nil && d.Message != "" {
			return d.Message
		}
		var s string
		if json.Unmarshal(e.Error, &s) == nil {
			return s
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
