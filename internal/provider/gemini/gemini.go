// Package gemini adapts the OpenAI chat format to Google's Gemini
// generateContent API (requests, responses and SSE streaming).
package gemini

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
	"net/url"
	"strings"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/sse"
)

// Provider talks to the Gemini API.
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
		base = "https://generativelanguage.googleapis.com/v1beta"
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

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type generationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type request struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

// BuildRequest translates an OpenAI chat request into a Gemini one. The
// "assistant" role becomes "model"; system messages become systemInstruction;
// consecutive same-role turns are merged.
func BuildRequest(providerName string, req *api.ChatRequest) (*request, error) {
	if provider.UsesTools(req) {
		return nil, provider.Invalid(providerName, "tool/function calling is not supported by the gemini adapter yet")
	}
	out := &request{}
	var system []part
	for _, m := range req.Messages {
		parts, err := provider.Parts(m)
		if err != nil {
			return nil, provider.Invalid(providerName, "unsupported message content: "+err.Error())
		}
		gp := make([]part, 0, len(parts))
		for _, p := range parts {
			switch {
			case p.Data != nil:
				gp = append(gp, part{InlineData: &inlineData{MimeType: p.MediaType, Data: base64.StdEncoding.EncodeToString(p.Data)}})
			case p.URL != "":
				return nil, provider.Invalid(providerName, "gemini adapter only supports inline (data:) images")
			default:
				gp = append(gp, part{Text: p.Text})
			}
		}
		if len(gp) == 0 {
			continue
		}
		role := ""
		switch m.Role {
		case "system", "developer":
			system = append(system, gp...)
			continue
		case "user":
			role = "user"
		case "assistant":
			role = "model"
		default:
			return nil, provider.Invalid(providerName, fmt.Sprintf("unsupported role %q", m.Role))
		}
		if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == role {
			out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, gp...)
			continue
		}
		out.Contents = append(out.Contents, content{Role: role, Parts: gp})
	}
	if len(out.Contents) == 0 {
		return nil, provider.Invalid(providerName, "at least one user or assistant message is required")
	}
	if len(system) > 0 {
		out.SystemInstruction = &content{Parts: system}
	}
	gc := &generationConfig{Temperature: req.Temperature, TopP: provider.ExtraFloat(req, "top_p"),
		MaxOutputTokens: provider.MaxTokens(req), StopSequences: provider.Stops(req)}
	if gc.Temperature != nil || gc.TopP != nil || gc.MaxOutputTokens != nil || len(gc.StopSequences) > 0 {
		out.GenerationConfig = gc
	}
	return out, nil
}

// --- response translation ---

type response struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
	ResponseID   string `json:"responseId"`
}

// FinishReason maps Gemini finish reasons to OpenAI ones.
func FinishReason(r string) string {
	switch r {
	case "", "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default: // SAFETY, RECITATION, BLOCKLIST, PROHIBITED_CONTENT, SPII, OTHER…
		return "content_filter"
	}
}

func (r *response) text() string {
	var sb strings.Builder
	if len(r.Candidates) > 0 {
		for _, p := range r.Candidates[0].Content.Parts {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func (r *response) usage() *api.Usage {
	u := r.UsageMetadata
	if u.TotalTokenCount == 0 && u.PromptTokenCount == 0 {
		return nil
	}
	return &api.Usage{PromptTokens: u.PromptTokenCount, CompletionTokens: u.CandidatesTokenCount, TotalTokens: u.PromptTokenCount + u.CandidatesTokenCount}
}

// --- transport ---

func (p *Provider) do(ctx, parent context.Context, model string, stream bool, body *request) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	u := p.baseURL + "/models/" + url.PathEscape(model) + ":generateContent"
	if stream {
		u = p.baseURL + "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		hr.Header.Set("x-goog-api-key", p.apiKey)
	}
	resp, err := p.client.Do(hr)
	if err != nil {
		return nil, provider.TransportError(parent, p.name, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, provider.StatusError(p.name, resp.StatusCode, errorMessage(raw))
	}
	return resp, nil
}

// Chat implements provider.Provider.
func (p *Provider) Chat(ctx context.Context, req *api.ChatRequest, model string) (*api.ChatResponse, error) {
	body, err := BuildRequest(p.name, req)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	resp, err := p.do(cctx, ctx, model, false, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&r); err != nil {
		return nil, &provider.Error{Provider: p.name, Code: provider.CodeBadResponse, Message: "invalid JSON from upstream", Retryable: true, Err: err}
	}
	finish := "stop"
	if len(r.Candidates) > 0 {
		finish = FinishReason(r.Candidates[0].FinishReason)
	}
	return &api.ChatResponse{
		ID: r.ResponseID, Object: "chat.completion", Created: time.Now().Unix(), Model: modelName(r.ModelVersion, model),
		Choices: []api.Choice{{Index: 0, Message: api.TextMessage("assistant", r.text()), FinishReason: &finish}},
		Usage:   r.usage(),
	}, nil
}

func modelName(version, requested string) string {
	if version != "" {
		return version
	}
	return requested
}

// ChatStream implements provider.Provider.
func (p *Provider) ChatStream(ctx context.Context, req *api.ChatRequest, model string) (provider.Stream, error) {
	body, err := BuildRequest(p.name, req)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, p.timeout)
	resp, err := p.do(cctx, ctx, model, true, body)
	if err != nil {
		cancel()
		return nil, err
	}
	return &stream{p: p, parent: ctx, cancel: cancel, body: resp.Body, r: sse.NewReader(resp.Body),
		model: model, created: time.Now().Unix()}, nil
}

type stream struct {
	p       *Provider
	parent  context.Context
	cancel  context.CancelFunc
	body    io.ReadCloser
	r       *sse.Reader
	model   string
	created int64

	pending []*api.ChatChunk // chunks produced by one SSE event, emitted in order
	started bool
	usage   *api.Usage
	ended   bool
}

func (s *stream) chunk(d api.Delta, finish *string) *api.ChatChunk {
	return &api.ChatChunk{ID: "chatcmpl-gemini", Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []api.ChunkChoice{{Index: 0, Delta: d, FinishReason: finish}}}
}

// Next translates Gemini SSE events (each a partial GenerateContentResponse)
// into OpenAI chunks. The stream ends at EOF; a usage chunk is emitted last.
func (s *stream) Next() (*api.ChatChunk, error) {
	for len(s.pending) == 0 {
		if s.ended {
			return nil, io.EOF
		}
		ev, err := s.r.Next()
		if errors.Is(err, io.EOF) {
			s.ended = true
			if !s.started {
				return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "empty stream"}
			}
			if s.usage != nil {
				s.pending = append(s.pending, &api.ChatChunk{ID: "chatcmpl-gemini", Object: "chat.completion.chunk", Created: s.created,
					Model: s.model, Choices: []api.ChunkChoice{}, Usage: s.usage})
			}
			continue
		}
		if err != nil {
			return nil, provider.TransportError(s.parent, s.p.name, err)
		}
		if len(bytes.TrimSpace(ev.Data)) == 0 {
			continue
		}
		var r response
		if err := json.Unmarshal(ev.Data, &r); err != nil {
			return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeBadResponse, Message: "invalid event JSON", Err: err}
		}
		if msg := errorMessage(ev.Data); msg != "" && len(r.Candidates) == 0 && strings.Contains(string(ev.Data), `"error"`) {
			return nil, &provider.Error{Provider: s.p.name, Code: provider.CodeUpstream, Message: msg}
		}
		if r.ModelVersion != "" {
			s.model = r.ModelVersion
		}
		if !s.started {
			s.started = true
			s.pending = append(s.pending, s.chunk(api.Delta{Role: "assistant"}, nil))
		}
		if t := r.text(); t != "" {
			s.pending = append(s.pending, s.chunk(api.Delta{Content: t}, nil))
		}
		if len(r.Candidates) > 0 && r.Candidates[0].FinishReason != "" {
			f := FinishReason(r.Candidates[0].FinishReason)
			s.pending = append(s.pending, s.chunk(api.Delta{}, &f))
		}
		if u := r.usage(); u != nil {
			s.usage = u
		}
	}
	c := s.pending[0]
	s.pending = s.pending[1:]
	return c, nil
}

func (s *stream) Close() error {
	s.cancel()
	return s.body.Close()
}

func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		if e.Error.Status != "" {
			return e.Error.Status + ": " + e.Error.Message
		}
		return e.Error.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
