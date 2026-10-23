package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/shadatr/switchyard/internal/api"
)

// Part is one piece of message content, normalized from OpenAI's
// string-or-array form for adapters that use another wire format.
type Part struct {
	Text string // set for text parts

	// Image parts: either inline data or a remote URL.
	MediaType string
	Data      []byte
	URL       string
}

// ErrUnsupportedContent is returned for content an adapter can't translate.
var ErrUnsupportedContent = errors.New("unsupported content part")

// Parts normalizes a message's content.
func Parts(m api.Message) ([]Part, error) {
	c := strings.TrimSpace(string(m.Content))
	if c == "" || c == "null" {
		return nil, nil
	}
	if c[0] == '"' {
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return nil, err
		}
		return []Part{{Text: s}}, nil
	}
	var raw []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(m.Content, &raw); err != nil {
		return nil, err
	}
	out := make([]Part, 0, len(raw))
	for _, p := range raw {
		switch p.Type {
		case "text":
			out = append(out, Part{Text: p.Text})
		case "image_url":
			u := p.ImageURL.URL
			if strings.HasPrefix(u, "data:") {
				mt, data, err := parseDataURL(u)
				if err != nil {
					return nil, err
				}
				out = append(out, Part{MediaType: mt, Data: data})
			} else {
				out = append(out, Part{URL: u})
			}
		default:
			return nil, ErrUnsupportedContent
		}
	}
	return out, nil
}

// parseDataURL decodes "data:<media type>;base64,<data>".
func parseDataURL(u string) (string, []byte, error) {
	meta, data, ok := strings.Cut(strings.TrimPrefix(u, "data:"), ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return "", nil, errors.New("image data URL must be base64")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSuffix(meta, ";base64"), b, nil
}

// ExtraFloat reads an optional numeric field from the request's
// pass-through fields (e.g. "top_p").
func ExtraFloat(req *api.ChatRequest, name string) *float64 {
	raw, ok := req.Extra[name]
	if !ok {
		return nil
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return &f
}

// Stops reads the OpenAI "stop" field (string or array).
func Stops(req *api.ChatRequest) []string {
	raw, ok := req.Extra["stop"]
	if !ok {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

// MaxTokens returns max_tokens, falling back to max_completion_tokens.
func MaxTokens(req *api.ChatRequest) *int {
	if req.MaxTokens != nil {
		return req.MaxTokens
	}
	if raw, ok := req.Extra["max_completion_tokens"]; ok {
		var n int
		if json.Unmarshal(raw, &n) == nil {
			return &n
		}
	}
	return nil
}

// UsesTools reports whether the request relies on tool/function calling,
// which the translating adapters don't support yet.
func UsesTools(req *api.ChatRequest) bool {
	for _, f := range []string{"tools", "functions", "tool_choice", "function_call"} {
		if _, ok := req.Extra[f]; ok {
			return true
		}
	}
	for _, m := range req.Messages {
		if m.Role == "tool" || m.Role == "function" {
			return true
		}
		if _, ok := m.Extra["tool_calls"]; ok {
			return true
		}
	}
	return false
}

// Invalid builds a non-retryable 400-style error for a request the adapter
// cannot translate.
func Invalid(providerName, msg string) *Error {
	return &Error{Provider: providerName, Status: 400, Code: CodeInvalid, Message: msg}
}
