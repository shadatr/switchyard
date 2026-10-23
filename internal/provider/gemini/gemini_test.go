package gemini

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/providertest"
)

// fake serves one fixture and records the request URL and body.
func fake(t *testing.T, status int, contentType, fixture string) (*Provider, *string, *[]byte) {
	t.Helper()
	var url string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		url = r.URL.Path
		if r.URL.RawQuery != "" {
			url += "?" + r.URL.RawQuery
		}
		body, _ = io.ReadAll(r.Body)
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(providertest.Read(t, fixture))
	}))
	t.Cleanup(srv.Close)
	return New(config.Provider{Name: "gemini", BaseURL: srv.URL + "/v1beta"}, "test-key"), &url, &body
}

func TestRequestTranslationGolden(t *testing.T) {
	p, url, body := fake(t, 200, "application/json", "response.json")
	resp, err := p.Chat(context.Background(), providertest.Request(t, "request.openai.json"), "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	if *url != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Errorf("url %q", *url)
	}
	providertest.JSONEqual(t, *body, "request.gemini.golden.json")
	if resp.Choices[0].Message.Text() != "Orange." || *resp.Choices[0].FinishReason != "stop" ||
		resp.Usage.PromptTokens != 30 || resp.Usage.CompletionTokens != 2 || resp.Model != "gemini-2.5-flash" {
		t.Errorf("response %+v usage %+v", resp, resp.Usage)
	}
}

func TestStreamTranslation(t *testing.T) {
	p, url, _ := fake(t, 200, "text/event-stream", "stream.sse")
	st, err := p.ChatStream(context.Background(), providertest.Request(t, "request.openai.json"), "gemini-2.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	c := providertest.Drain(t, st)
	if *url != "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse" {
		t.Errorf("url %q", *url)
	}
	if c.Text != "Hello, world!" || c.Roles != 1 || len(c.Finish) != 1 || c.Finish[0] != "stop" {
		t.Errorf("collected %+v", c)
	}
	if c.Usage == nil || c.Usage.PromptTokens != 9 || c.Usage.CompletionTokens != 4 || !c.UsageLast {
		t.Errorf("usage %+v last=%v", c.Usage, c.UsageLast)
	}
}

func TestQuotaErrorIsRateLimited(t *testing.T) {
	p, _, _ := fake(t, 429, "application/json", "error_quota.json")
	_, err := p.Chat(context.Background(), providertest.Request(t, "request.openai.json"), "m")
	pe, ok := provider.AsError(err)
	if !ok || pe.Status != 429 || !pe.Retryable || !strings.HasPrefix(pe.Message, "RESOURCE_EXHAUSTED") {
		t.Fatalf("err = %#v", err)
	}
}

func TestRemoteImageURLRejected(t *testing.T) {
	req := providertest.Request(t, "request.openai.json")
	req.Messages[1].Content = []byte(`[{"type":"image_url","image_url":{"url":"https://example.com/x.png"}}]`)
	if _, err := BuildRequest("gemini", req); err == nil {
		t.Fatal("remote image URLs must be rejected with a clear error")
	}
}

func TestFinishReasons(t *testing.T) {
	for in, want := range map[string]string{"STOP": "stop", "MAX_TOKENS": "length", "SAFETY": "content_filter", "": "stop"} {
		if got := FinishReason(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
