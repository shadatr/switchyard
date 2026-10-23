// Package api defines the OpenAI-compatible wire types Switchyard accepts and
// returns. Known fields are typed; every unknown field is preserved so the
// gateway can pass requests through to OpenAI-style upstreams unchanged.
package api

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ChatRequest is a /v1/chat/completions request body.
type ChatRequest struct {
	Model         string
	Messages      []Message
	Stream        bool
	StreamOptions *StreamOptions
	Temperature   *float64
	MaxTokens     *int
	User          string

	// Extra holds every field not listed above, verbatim.
	Extra map[string]json.RawMessage
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

var chatRequestFields = []string{"model", "messages", "stream", "stream_options", "temperature", "max_tokens", "user"}

func (r *ChatRequest) UnmarshalJSON(b []byte) error {
	fields, err := splitObject(b)
	if err != nil {
		return err
	}
	if err := decodeField(fields, "model", &r.Model); err != nil {
		return err
	}
	if err := decodeField(fields, "messages", &r.Messages); err != nil {
		return err
	}
	if err := decodeField(fields, "stream", &r.Stream); err != nil {
		return err
	}
	if err := decodeField(fields, "stream_options", &r.StreamOptions); err != nil {
		return err
	}
	if err := decodeField(fields, "temperature", &r.Temperature); err != nil {
		return err
	}
	if err := decodeField(fields, "max_tokens", &r.MaxTokens); err != nil {
		return err
	}
	if err := decodeField(fields, "user", &r.User); err != nil {
		return err
	}
	r.Extra = remaining(fields, chatRequestFields)
	return nil
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	out := copyExtra(r.Extra)
	if err := setField(out, "model", r.Model, r.Model == ""); err != nil {
		return nil, err
	}
	if err := setField(out, "messages", r.Messages, r.Messages == nil); err != nil {
		return nil, err
	}
	if err := setField(out, "stream", r.Stream, !r.Stream); err != nil {
		return nil, err
	}
	if err := setField(out, "stream_options", r.StreamOptions, r.StreamOptions == nil); err != nil {
		return nil, err
	}
	if err := setField(out, "temperature", r.Temperature, r.Temperature == nil); err != nil {
		return nil, err
	}
	if err := setField(out, "max_tokens", r.MaxTokens, r.MaxTokens == nil); err != nil {
		return nil, err
	}
	if err := setField(out, "user", r.User, r.User == ""); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// PromptText concatenates the text of every message, for token estimation and
// cache keys. Non-text content parts are ignored.
func (r *ChatRequest) PromptText() string {
	var sb strings.Builder
	for _, m := range r.Messages {
		sb.WriteString(m.Role)
		sb.WriteByte('\n')
		sb.WriteString(m.Text())
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Message is one chat message. Content is kept raw because OpenAI allows
// either a string or an array of typed content parts.
type Message struct {
	Role    string
	Content json.RawMessage
	Extra   map[string]json.RawMessage
}

var messageFields = []string{"role", "content"}

func (m *Message) UnmarshalJSON(b []byte) error {
	fields, err := splitObject(b)
	if err != nil {
		return err
	}
	if err := decodeField(fields, "role", &m.Role); err != nil {
		return err
	}
	if c, ok := fields["content"]; ok {
		m.Content = append(json.RawMessage(nil), c...)
	}
	m.Extra = remaining(fields, messageFields)
	return nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	out := copyExtra(m.Extra)
	if err := setField(out, "role", m.Role, false); err != nil {
		return nil, err
	}
	if m.Content != nil {
		out["content"] = m.Content
	}
	return json.Marshal(out)
}

// TextMessage builds a message whose content is a plain string.
func TextMessage(role, text string) Message {
	c, _ := json.Marshal(text)
	return Message{Role: role, Content: c}
}

// Text returns the textual content of the message: the string itself, or the
// concatenation of all "text" parts.
func (m Message) Text() string {
	c := bytes.TrimSpace(m.Content)
	if len(c) == 0 || bytes.Equal(c, []byte("null")) {
		return ""
	}
	if c[0] == '"' {
		var s string
		if json.Unmarshal(c, &s) == nil {
			return s
		}
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for i, p := range parts {
		if p.Type != "text" {
			continue
		}
		if i > 0 && sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// Usage is token accounting reported by the upstream (or estimated).
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse is a non-streaming completion. When Raw is set (OpenAI-style
// upstreams) the gateway writes it verbatim; the typed fields are used for
// accounting and for translated providers.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`

	Raw []byte `json:"-"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason *string `json:"finish_reason"`
}

// ChatChunk is one streaming event payload (object "chat.completion.chunk").
type ChatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	Usage   *Usage        `json:"usage,omitempty"`

	// Raw is the exact upstream data payload, if the chunk came from an
	// OpenAI-style upstream and can be forwarded verbatim.
	Raw []byte `json:"-"`
}

type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type Delta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// ContentText returns the concatenated delta content of all choices.
func (c *ChatChunk) ContentText() string {
	if len(c.Choices) == 1 {
		return c.Choices[0].Delta.Content
	}
	var sb strings.Builder
	for _, ch := range c.Choices {
		sb.WriteString(ch.Delta.Content)
	}
	return sb.String()
}

// ErrorBody is the OpenAI-style error envelope.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
