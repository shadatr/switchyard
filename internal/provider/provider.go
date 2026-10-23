// Package provider defines the interface every upstream model provider
// implements, plus a typed error the router uses to decide on retries.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/shadatr/switchyard/internal/api"
)

// Provider is one upstream (OpenAI, Anthropic, …). Implementations must be
// safe for concurrent use.
type Provider interface {
	Name() string
	// Chat performs a non-streaming completion against model.
	Chat(ctx context.Context, req *api.ChatRequest, model string) (*api.ChatResponse, error)
	// ChatStream starts a streaming completion. The returned Stream must be
	// closed by the caller. Errors returned here happen before any chunk.
	ChatStream(ctx context.Context, req *api.ChatRequest, model string) (Stream, error)
}

// Stream yields chunks until io.EOF.
type Stream interface {
	Next() (*api.ChatChunk, error)
	Close() error
}

// Error codes used across providers.
const (
	CodeTimeout     = "timeout"
	CodeConnect     = "connect_error"
	CodeUpstream    = "upstream_error"
	CodeRateLimited = "rate_limited"
	CodeInvalid     = "invalid_request"
	CodeCanceled    = "canceled"
	CodeBadResponse = "bad_response"
)

// Error is a classified upstream failure.
type Error struct {
	Provider  string
	Status    int    // upstream HTTP status, 0 if none
	Code      string // one of the Code* constants
	Message   string
	Retryable bool
	Err       error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s: %s (status %d): %s", e.Provider, e.Code, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Provider, e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// AsError extracts a *Error from err.
func AsError(err error) (*Error, bool) {
	var pe *Error
	ok := errors.As(err, &pe)
	return pe, ok
}

// RetryableStatus reports whether an upstream HTTP status is worth retrying on
// another attempt or target.
func RetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// StatusError builds an Error from an upstream HTTP error response.
func StatusError(providerName string, status int, msg string) *Error {
	code := CodeUpstream
	switch {
	case status == http.StatusTooManyRequests:
		code = CodeRateLimited
	case status >= 400 && status < 500 && status != http.StatusRequestTimeout:
		code = CodeInvalid
	}
	return &Error{Provider: providerName, Status: status, Code: code, Message: msg, Retryable: RetryableStatus(status)}
}

// TransportError classifies a failure to talk to the upstream at all.
// parent is the caller's context: if it was canceled the client went away and
// retrying is pointless.
func TransportError(parent context.Context, providerName string, err error) *Error {
	if parent.Err() != nil {
		return &Error{Provider: providerName, Code: CodeCanceled, Message: "client canceled", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Provider: providerName, Code: CodeTimeout, Message: "upstream timed out", Retryable: true, Err: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &Error{Provider: providerName, Code: CodeTimeout, Message: "upstream timed out", Retryable: true, Err: err}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return &Error{Provider: providerName, Code: CodeConnect, Message: "cannot connect to upstream", Retryable: true, Err: err}
	}
	return &Error{Provider: providerName, Code: CodeUpstream, Message: err.Error(), Retryable: true, Err: err}
}
