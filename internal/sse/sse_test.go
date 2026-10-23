package sse

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, s string) []Event {
	t.Helper()
	r := NewReader(strings.NewReader(s))
	var evs []Event
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			return evs
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		evs = append(evs, ev)
	}
}

func TestReader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Event
	}{
		{"openai style", "data: {\"a\":1}\n\ndata: [DONE]\n\n",
			[]Event{{Data: []byte(`{"a":1}`)}, {Data: []byte("[DONE]")}}},
		{"crlf", "data: x\r\n\r\ndata: y\r\n\r\n",
			[]Event{{Data: []byte("x")}, {Data: []byte("y")}}},
		{"bare cr", "data: x\r\rdata: y\r\r",
			[]Event{{Data: []byte("x")}, {Data: []byte("y")}}},
		{"multi-line data", "data: a\ndata: b\ndata:\n\n",
			[]Event{{Data: []byte("a\nb\n")}}},
		{"no space after colon", "data:x\n\n", []Event{{Data: []byte("x")}}},
		{"comments and blank lines ignored", ": ping\n\n\n: keepalive\ndata: z\n\n",
			[]Event{{Data: []byte("z")}}},
		{"named event (anthropic style)", "event: message_start\ndata: {}\n\n",
			[]Event{{Name: "message_start", Data: []byte("{}")}}},
		{"id and retry ignored", "id: 7\nretry: 100\ndata: q\n\n", []Event{{Data: []byte("q")}}},
		{"trailing event without blank line", "data: last", []Event{{Data: []byte("last")}}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := readAll(t, tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %q, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i].Name != tt.want[i].Name || !bytes.Equal(got[i].Data, tt.want[i].Data) {
					t.Errorf("event %d = {%q %q}, want {%q %q}", i, got[i].Name, got[i].Data, tt.want[i].Name, tt.want[i].Data)
				}
			}
		})
	}
}

func TestIsDone(t *testing.T) {
	if !(Event{Data: []byte("[DONE]")}).IsDone() || (Event{Data: []byte("{}")}).IsDone() {
		t.Fatal("IsDone wrong")
	}
}

func TestEventTooLarge(t *testing.T) {
	big := "data: " + strings.Repeat("x", MaxEventBytes+1) + "\n\n"
	_, err := NewReader(strings.NewReader(big)).Next()
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("err = %v, want ErrEventTooLarge", err)
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	payloads := [][]byte{[]byte(`{"x":1}`), []byte("line1\nline2"), []byte("a\r\nb"), {}, Done}
	for _, p := range payloads {
		if err := w.WriteData(p); err != nil {
			t.Fatal(err)
		}
	}
	got := readAll(t, buf.String())
	want := []string{`{"x":1}`, "line1\nline2", "a\nb", "", "[DONE]"}
	if len(got) != len(want) {
		t.Fatalf("got %d events", len(got))
	}
	for i := range want {
		if string(got[i].Data) != want[i] {
			t.Errorf("event %d = %q, want %q", i, got[i].Data, want[i])
		}
	}
}

func FuzzReader(f *testing.F) {
	f.Add([]byte("data: {\"a\":1}\n\ndata: [DONE]\n\n"))
	f.Add([]byte("event: x\r\ndata: y\r\n\r\n: c\n"))
	f.Add([]byte("data:\rdata:\n\n\r\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		r := NewReader(bytes.NewReader(in))
		for i := 0; i < len(in)+2; i++ {
			ev, err := r.Next()
			if err != nil {
				return
			}
			if len(ev.Data) > MaxEventBytes {
				t.Fatalf("event exceeds MaxEventBytes")
			}
		}
		t.Fatalf("reader did not terminate on %d bytes", len(in))
	})
}

func FuzzWriterRoundTrip(f *testing.F) {
	f.Add([]byte(`{"choices":[{"delta":{"content":"hi"}}]}`))
	f.Add([]byte("a\nb"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if bytes.ContainsAny(payload, "\r") || len(payload) > 4096 {
			t.Skip() // CR is normalized to LF by the spec; covered by TestWriterRoundTrip
		}
		var buf bytes.Buffer
		if err := NewWriter(&buf).WriteData(payload); err != nil {
			t.Fatal(err)
		}
		ev, err := NewReader(&buf).Next()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(ev.Data, payload) {
			t.Fatalf("round trip: got %q want %q", ev.Data, payload)
		}
	})
}
