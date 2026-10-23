package mockllm

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/sse"
)

func post(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/v1/chat/completions", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNonStream(t *testing.T) {
	srv := httptest.NewServer(New(Options{Tokens: 3}).Handler())
	defer srv.Close()

	resp := post(t, srv.URL, `{"model":"mock-a","messages":[{"role":"user","content":"hello"}]}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out api.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if got := out.Choices[0].Message.Text(); got != "token-1 token-2 token-3" {
		t.Errorf("text = %q", got)
	}
	if out.Usage == nil || out.Usage.CompletionTokens != 3 || out.Usage.PromptTokens == 0 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestStreamWithUsage(t *testing.T) {
	srv := httptest.NewServer(New(Options{Tokens: 5}).Handler())
	defer srv.Close()

	resp := post(t, srv.URL, `{"model":"mock-a","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`, nil)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	r := sse.NewReader(resp.Body)
	var text strings.Builder
	var usage *api.Usage
	done := false
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if ev.IsDone() {
			done = true
			continue
		}
		var c api.ChatChunk
		if err := json.Unmarshal(ev.Data, &c); err != nil {
			t.Fatal(err)
		}
		text.WriteString(c.ContentText())
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if !done {
		t.Error("missing [DONE]")
	}
	if text.String() != Text(5) {
		t.Errorf("text = %q", text.String())
	}
	if usage == nil || usage.CompletionTokens != 5 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestHeaderOverrides(t *testing.T) {
	srv := httptest.NewServer(New(DefaultOptions()).Handler())
	defer srv.Close()

	resp := post(t, srv.URL, `{"model":"m","messages":[]}`, map[string]string{"X-Mock-Status": "429"})
	if resp.StatusCode != 429 {
		t.Fatalf("status %d", resp.StatusCode)
	}

	start := time.Now()
	resp = post(t, srv.URL, `{"model":"m","messages":[]}`, map[string]string{"X-Mock-TTFT": "0s", "X-Mock-ITL": "0s", "X-Mock-Tokens": "2"})
	if resp.StatusCode != 200 || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("override not applied: status %d after %s", resp.StatusCode, time.Since(start))
	}
}

func TestTimingRespected(t *testing.T) {
	srv := httptest.NewServer(New(Options{TTFT: 80 * time.Millisecond, ITL: 10 * time.Millisecond, Tokens: 4}).Handler())
	defer srv.Close()
	start := time.Now()
	resp := post(t, srv.URL, `{"model":"m","messages":[]}`, nil)
	_, _ = io.Copy(io.Discard, resp.Body)
	if el := time.Since(start); el < 110*time.Millisecond {
		t.Errorf("finished in %s, expected >= ttft + 3*itl", el)
	}
}

func TestFailAfterTokensAbortsStream(t *testing.T) {
	srv := httptest.NewServer(New(Options{Tokens: 10}).Handler())
	defer srv.Close()
	resp := post(t, srv.URL, `{"model":"m","stream":true,"messages":[]}`, map[string]string{"X-Mock-Fail-After-Tokens": "2"})
	_, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected a read error from the aborted stream")
	}
}

func TestErrorRate(t *testing.T) {
	srv := httptest.NewServer(New(Options{ErrorRate: 1, ErrorStatus: 502}).Handler())
	defer srv.Close()
	if resp := post(t, srv.URL, `{"model":"m","messages":[]}`, nil); resp.StatusCode != 502 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
