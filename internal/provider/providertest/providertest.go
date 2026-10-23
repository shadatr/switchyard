// Package providertest has helpers for adapter golden tests.
package providertest

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shadatr/switchyard/internal/api"
	"github.com/shadatr/switchyard/internal/provider"
)

// Read returns testdata/name.
func Read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Request parses an OpenAI request fixture.
func Request(t *testing.T, name string) *api.ChatRequest {
	t.Helper()
	var r api.ChatRequest
	if err := json.Unmarshal(Read(t, name), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

// JSONEqual fails the test unless got and the golden file are the same JSON.
func JSONEqual(t *testing.T, got []byte, golden string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("got invalid JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(Read(t, golden), &b); err != nil {
		t.Fatal(err)
	}
	ab, _ := json.MarshalIndent(a, "", "  ")
	bb, _ := json.MarshalIndent(b, "", "  ")
	if string(ab) != string(bb) {
		t.Fatalf("request mismatch with %s\n--- got\n%s\n--- want\n%s", golden, ab, bb)
	}
}

// Collected is everything read from a translated stream.
type Collected struct {
	Text      string
	Roles     int
	Finish    []string
	Usage     *api.Usage
	UsageLast bool // the usage chunk was the final chunk
}

// Drain reads a stream to EOF.
func Drain(t *testing.T, s provider.Stream) Collected {
	t.Helper()
	defer s.Close()
	var c Collected
	var sb strings.Builder
	for {
		ch, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		c.UsageLast = false
		for _, choice := range ch.Choices {
			if choice.Delta.Role != "" {
				c.Roles++
			}
			sb.WriteString(choice.Delta.Content)
			if choice.FinishReason != nil {
				c.Finish = append(c.Finish, *choice.FinishReason)
			}
		}
		if ch.Usage != nil {
			c.Usage, c.UsageLast = ch.Usage, len(ch.Choices) == 0
		}
	}
	c.Text = sb.String()
	return c
}
