package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/mockllm"
	"github.com/shadatr/switchyard/internal/provider"
	"github.com/shadatr/switchyard/internal/provider/openai"
)

// An in-flight stream must complete when the server shuts down gracefully,
// and new connections must be refused once shutdown starts.
func TestGracefulShutdownCompletesInFlightStreams(t *testing.T) {
	up := httptest.NewServer(mockllm.New(mockllm.Options{Tokens: 40, ITL: 20 * time.Millisecond}).Handler()) // ~0.8s stream
	defer up.Close()
	cfg := &config.Config{
		Providers: []config.Provider{{Name: "mock", Type: config.ProviderOpenAI, BaseURL: up.URL + "/v1"}},
		Models:    []config.Model{{Alias: "fast", Targets: []config.Target{{Provider: "mock", Model: "mock-a"}}}},
	}
	cfg.ApplyDefaults()
	s := New(Deps{Config: cfg, Providers: map[string]provider.Provider{"mock": openai.New(cfg.Providers[0], "")}})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: s.Handler()}
	go func() { _ = hs.Serve(ln) }()
	url := "http://" + ln.Addr().String()

	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(streamBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil { // stream has started
		t.Fatal(err)
	}

	shutdownErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr <- hs.Shutdown(ctx)
	}()

	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("in-flight stream was cut off: %v", err)
	}
	if !strings.Contains(string(rest), "token-40") || !strings.HasSuffix(strings.TrimSpace(string(rest)), "data: [DONE]") {
		t.Fatalf("stream incomplete after shutdown began:\n%s", rest)
	}
	if err := <-shutdownErr; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_, err = http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(chatBody))
	var opErr *net.OpError
	if err == nil || !errors.As(err, &opErr) {
		t.Fatalf("new connections must be refused after shutdown, got %v", err)
	}
}
