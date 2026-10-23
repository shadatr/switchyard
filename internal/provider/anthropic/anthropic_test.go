package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/providertest"
)

// fake serves one fixture and lets the test inspect the translated request.
func fake(t *testing.T, status int, contentType, fixture string, check func(body []byte)) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != APIVersion {
			t.Errorf("bad request: %s %s headers %v", r.Method, r.URL.Path, r.Header)
		}
		if check != nil {
			check(body)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(providertest.Read(t, fixture))
	}))
	t.Cleanup(srv.Close)
	return New(config.Provider{Name: "anthropic", BaseURL: srv.URL}, "test-key")
}

func TestRequestTranslationGolden(t *testing.T) {
	p := fake(t, 200, "application/json", "response.json", func(body []byte) {
		providertest.JSONEqual(t, body, "request.anthropic.golden.json")
	})
	resp, err := p.Chat(context.Background(), providertest.Request(t, "request.openai.json"), "claude-haiku-4-5")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Message.Text(); got != "Orange, with white paws." {
		t.Errorf("text %q", got)
	}
	if *resp.Choices[0].FinishReason != "stop" || resp.Usage.PromptTokens != 42 || resp.Usage.CompletionTokens != 7 || resp.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("response %+v usage %+v", resp, resp.Usage)
	}
}

func TestStreamTranslation(t *testing.T) {
	p := fake(t, 200, "text/event-stream", "stream.sse", func(body []byte) {
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("stream flag missing: %s", body)
		}
	})
	st, err := p.ChatStream(context.Background(), providertest.Request(t, "request.openai.json"), "claude-haiku-4-5")
	if err != nil {
		t.Fatal(err)
	}
	c := providertest.Drain(t, st)
	if c.Text != "Hello, world" || c.Roles != 1 {
		t.Errorf("text %q roles %d", c.Text, c.Roles)
	}
	if len(c.Finish) != 1 || c.Finish[0] != "length" {
		t.Errorf("finish %v (max_tokens must map to length)", c.Finish)
	}
	if c.Usage == nil || c.Usage.PromptTokens != 25 || c.Usage.CompletionTokens != 15 || !c.UsageLast {
		t.Errorf("usage %+v last=%v", c.Usage, c.UsageLast)
	}
}

func TestOverloadedIsRetryable(t *testing.T) {
	p := fake(t, 529, "application/json", "error_overloaded.json", nil)
	_, err := p.Chat(context.Background(), providertest.Request(t, "request.openai.json"), "m")
	pe, ok := provider.AsError(err)
	if !ok || !pe.Retryable || pe.Status != 529 || pe.Message != "overloaded_error: Overloaded" {
		t.Fatalf("err = %#v", err)
	}
}

func TestToolsRejected(t *testing.T) {
	req := providertest.Request(t, "request.openai.json")
	req.Extra = map[string]json.RawMessage{"tools": json.RawMessage(`[]`)}
	_, err := BuildRequest("anthropic", req, "m", false)
	if pe, ok := provider.AsError(err); !ok || pe.Status != 400 || pe.Retryable {
		t.Fatalf("err = %v", err)
	}
}

func TestMaxTokensDefaultAndOverride(t *testing.T) {
	req := providertest.Request(t, "request.openai.json")
	r, err := BuildRequest("anthropic", req, "m", false)
	if err != nil || r.MaxTokens != DefaultMaxTokens {
		t.Fatalf("max_tokens %d err %v", r.MaxTokens, err)
	}
	n := 77
	req.MaxTokens = &n
	if r, _ := BuildRequest("anthropic", req, "m", false); r.MaxTokens != 77 {
		t.Fatalf("max_tokens %d", r.MaxTokens)
	}
}

func TestFinishReasons(t *testing.T) {
	for in, want := range map[string]string{"end_turn": "stop", "stop_sequence": "stop", "max_tokens": "length", "tool_use": "tool_calls", "refusal": "content_filter"} {
		if got := FinishReason(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
