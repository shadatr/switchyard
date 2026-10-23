package api

import (
	"encoding/json"
	"testing"
)

func TestChatRequestRoundTrip(t *testing.T) {
	in := `{
		"model": "fast",
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": [
				{"type": "text", "text": "what is in"},
				{"type": "image_url", "image_url": {"url": "https://x/y.png"}},
				{"type": "text", "text": "this image?"}
			], "name": "shada"},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "c1", "type": "function"}]}
		],
		"stream": true,
		"stream_options": {"include_usage": true},
		"temperature": 0,
		"max_tokens": 64,
		"top_p": 0.9,
		"tools": [{"type": "function", "function": {"name": "f"}}]
	}`
	var req ChatRequest
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "fast" || !req.Stream || req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Fatalf("known fields wrong: %+v", req)
	}
	if req.Temperature == nil || *req.Temperature != 0 {
		t.Fatalf("temperature 0 must be preserved as non-nil")
	}
	if _, ok := req.Extra["top_p"]; !ok {
		t.Fatalf("unknown field top_p dropped")
	}
	if got := req.Messages[0].Text(); got != "be brief" {
		t.Errorf("string content text = %q", got)
	}
	if got := req.Messages[1].Text(); got != "what is in\nthis image?" {
		t.Errorf("parts content text = %q", got)
	}
	if got := req.Messages[2].Text(); got != "" {
		t.Errorf("null content text = %q", got)
	}
	if _, ok := req.Messages[1].Extra["name"]; !ok {
		t.Errorf("message extra field dropped")
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, in, string(out)) {
		t.Errorf("round trip changed JSON:\n in: %s\nout: %s", in, out)
	}
}

func TestChatRequestOmitsUnsetFields(t *testing.T) {
	req := ChatRequest{Model: "m", Messages: []Message{TextMessage("user", "hi")}}
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	if !jsonEqual(t, want, string(out)) {
		t.Errorf("got %s want %s", out, want)
	}
}

func TestChatRequestRejectsNonObject(t *testing.T) {
	var req ChatRequest
	if err := json.Unmarshal([]byte(`[1,2]`), &req); err == nil {
		t.Fatal("expected error for array body")
	}
	if err := json.Unmarshal([]byte(`{"messages":"nope"}`), &req); err == nil {
		t.Fatal("expected error for wrong messages type")
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatal(err)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}
