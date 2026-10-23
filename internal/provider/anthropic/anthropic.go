// Package anthropic adapts the OpenAI chat format to Anthropic's Messages
// API (requests, responses and streaming events).
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
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

// APIVersion is sent as the anthropic-version header.
const APIVersion = "2023-06-01"

// DefaultMaxTokens is used when the client doesn't set max_tokens, which
// Anthropic requires.
const DefaultMaxTokens = 4096

// Provider talks to the Anthropic Messages API.
type Provider struct {
	name    string
	baseURL string
	apiKey  string
	timeout time.Duration
	client  *http.Client
}

// New builds a provider with its own transport.
func New(cfg config.Provider, apiKey string) *Provider {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 3 * time.Second
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = 64
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        cfg.MaxIdleConnsPerHost * 2,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: cfg.ConnectTimeout,
	}
	return &Provider{name: cfg.Name, baseURL: base, apiKey: apiKey, timeout: cfg.Timeout, client: &http.Client{Transport: tr}}
}

func (p *Provider) Name() string { return p.name }

// --- request translation ---

type message struct {
	Role    string  `json:"role"`
	Content []block `json:"content"`
}

type block struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *imageSource `json:"source,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // base64 | url
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type request struct {
	Model         string    `json:"model"`
	MaxTokens     int       `json:"max_tokens"`
	System        string    `json:"system,omitempty"`
	Messages      []message `json:"messages"`
	Temperature   *float64  `json:"temperature,omitempty"`
	TopP          *float64  `json:"top_p,omitempty"`
	StopSequences []string  `json:"stop_sequences,omitempty"`
	Stream        bool      `json:"stream,omitempty"`
}

// BuildRequest translates an OpenAI chat request into an Anthropic one.
// System messages are hoisted into "system"; consecutive messages with the
// same role are merged, as Anthropic expects alternating turns.
func BuildRequest(providerName string, req *api.ChatRequest, model string, stream bool) (*request, error) {
	if provider.UsesTools(req) {
		return nil, provider.Invalid(providerName, "tool/function calling is not supported by the anthropic adapter yet")
	}
	out := &request{Model: model, MaxTokens: DefaultMaxTokens, Temperature: req.Temperature,
		TopP: provider.ExtraFloat(req, "top_p"), StopSequences: provider.Stops(req), Stream: stream}
	if mt := provider.MaxTokens(req); mt != nil {
		out.MaxTokens = *mt
	}
	var system []string
	for _, m := range req.Messages {
		parts, err := provider.Parts(m)
		if err != nil {
			return nil, provider.Invalid(providerName, "unsupported message content: "+err.Error())
		}
		switch m.Role {
		case "system", "developer":
			for _, p := range parts {
				if p.Text != "" {
					system = append(system, p.Text)
				}
			}
			continue
		case "user", "assistant":
		default:
			return nil, provider.Invalid(providerName, fmt.Sprintf("unsupported role %q", m.Role))
		}
		blocks := make([]block, 0, len(parts))
		for _, p := range parts {
			switch {
			case p.Data != nil:
				blocks = append(blocks, block{Type: "image", Source: &imageSource{Type: "base64", MediaType: p.MediaType, Data: base64.StdEncoding.EncodeToString(p.Data)}})
			case p.URL != "":
				blocks = append(blocks, block{Type: "image", Source: &imageSource{Type: "url", URL: p.URL}})
			default:
				blocks = append(blocks, block{Type: "text", Text: p.Text})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == m.Role {
			out.Messages[n-1].Content = append(out.Messages[n-1].Content, blocks...)
			continue
		}
		out.Messages = append(out.Messages, message{Role: m.Role, Content: blocks})
	}
	out.System = strings.Join(system, "\n\n")
	if len(out.Messages) == 0 {
		return nil, provider.Invalid(providerName, "at least one user or assistant message is required")
	}
	return out, nil
}

// --- response translation ---

type response struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// FinishReason maps Anthropic stop reasons to OpenAI finish reasons.
func FinishReason(stop string) string {
	switch stop {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	default: // end_turn, stop_sequence, pause_turn
		return "stop"
	}
}

func toChatResponse(r *response) *api.ChatResponse {
	var text strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	finish := FinishReason(r.StopReason)
	return &api.ChatResponse{
		ID: r.ID, Object: "chat.completion", Created: time.Now().Unix(), Model: r.Model,
		Choices: []api.Choice{{Index: 0, Message: api.TextMessage("assistant", text.String()), FinishReason: &finish}},
		Usage:   &api.Usage{PromptTokens: r.Usage.InputTokens, CompletionTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.InputTokens + r.Usage.OutputTokens},
	}
}

// --- transport ---

func (p *Provider) do(ctx, parent context.Context, body *request) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("anthropic-version", APIVersion)
	if p.apiKey != "" {
		hr.Header.Set("x-api-key", p.apiKey)
	}
	if body.Stream {
		hr.Header.Set("Accept", "text/event-stream")
	}
	resp, err := p.client.Do(hr)
	if err != nil {
		return nil, provider.TransportError(parent, p.name, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		e := provider.StatusError(p.name, resp.StatusCode, errorMessage(raw))
		if resp.StatusCode == 529 { // overloaded
			e.Code, e.Retryable = provider.CodeUpstream, true
		}
		return nil, e
	}
	return resp, nil
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req *api.ChatRequest, model string) (*api.ChatResponse, error) {
	body, err := BuildRequest(p.name, req, model, false)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	resp, err := p.do(cctx, ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&r); err != nil {
		return nil, &provider.Error{Provider: p.name, Code: provider.CodeBadResponse, Message: "invalid JSON from upstream", Retryable: true, Err: err}
	}
	return toChatResponse(&r), nil
}

// ChatStream implements provider.Provider.
func (p *Provider) ChatStream(ctx context.Context, req *api.ChatRequest, model string) (provider.Stream, error) {
	body, err := BuildRequest(p.name, req, model, true)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	resp, err := p.do(cctx, ctx, body)
	if err != nil {
		cancel()
		return nil, err
	}
	return &stream{p: p, parent: ctx, cancel: cancel, body: resp.Body, r: sse.NewReader(resp.Body), created: time.Now().Unix()}, nil
}

type stream struct {
	p       *Provider
	parent  context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	r       *sse.Reader
	created int64

	id, model    string
	inputTokens  int
	outputTokens int
	finished     bool // message_stop seen; usage chunk emitted next
	done         bool
}

type event struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s *stream) chunk(d api.Delta, finish *string) *api.ChatChunk {
	return &api.ChatChunk{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []api.ChunkChoice{{Index: 0, Delta: d, FinishReason: finish}}}
}

// Next translates Anthropic stream events into OpenAI chunks.
func (s *stream) Next() (*api.ChatChunk, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.finished {
		s.done = true
		return &api.ChatChunk{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model, Choices: []api.ChunkChoice{},
			Usage: &api.Usage{PromptTokens: s.inputTokens, CompletionTokens: s.outputTokens, TotalTokens: s.inputTokens + s.outputTokens}}, nil
	}
	for {
		ev, err := s.r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "stream ended without message_stop"}
			}
			return nil, provider.TransportError(s.parent, s.p.name, err)
		}
		var e event
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "invalid event JSON", Err: err}
		}
		switch e.Type {
		case "message_start":
			s.id, s.model = e.Message.ID, e.Message.Model
			s.inputTokens, s.outputTokens = e.Message.Usage.InputTokens, e.Message.Usage.OutputTokens
			return s.chunk(api.Delta{Role: "assistant"}, nil), nil
		case "content_block_delta":
			if e.Delta.Type == "text_delta" && e.Delta.Text != "" {
				return s.chunk(api.Delta{Content: e.Delta.Text}, nil), nil
			}
		case "message_delta":
			if e.Usage.OutputTokens > 0 {
				s.outputTokens = e.Usage.OutputTokens
			}
			if e.Delta.StopReason != "" {
				f := FinishReason(e.Delta.StopReason)
				return s.chunk(api.Delta{}, &f), nil
			}
		case "message_stop":
			s.finished = true
			return s.Next()
		case "error":
			code := provider.CodeUpstream
			return nil, &provider.Error{Provider: s.p.name, Code: code, Message: e.Error.Type + ": " + e.Error.Message}
		default: // ping, content_block_start, content_block_stop
		}
	}
}

func (s *stream) Close() error {
	s.cancel()
	return s.body.Close()
}

func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Type + ": " + e.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
