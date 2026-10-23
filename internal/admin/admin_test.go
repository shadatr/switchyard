package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthAndCORS(t *testing.T) {
	h := Handler(Deps{Token: "secret", CORSOrigins: []string{"http://localhost:3000"}})
	cases := []struct {
		name, auth, origin string
		method             string
		want               int
	}{
		{"no token", "", "", http.MethodGet, http.StatusUnauthorized},
		{"wrong token", "Bearer nope", "", http.MethodGet, http.StatusUnauthorized},
		{"ok token, no analytics configured", "Bearer secret", "", http.MethodGet, http.StatusServiceUnavailable},
		{"preflight from dashboard", "", "http://localhost:3000", http.MethodOptions, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/admin/api/overview", nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status %d, want %d", rr.Code, c.want)
			}
			if c.origin != "" && rr.Header().Get("Access-Control-Allow-Origin") != c.origin {
				t.Error("missing CORS header")
			}
		})
	}
	// Unknown origins get no CORS headers.
	req := httptest.NewRequest(http.MethodOptions, "/admin/api/overview", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unexpected CORS header for unknown origin")
	}
}

func TestDisabledWithoutToken(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler(Deps{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/api/keys", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	r := Request{TS: time.UnixMilli(1_700_000_000_123).UTC(), RequestID: "01HXYZ"}
	ts, id, err := decodeCursor(encodeCursor(r))
	if err != nil || !ts.Equal(r.TS) || id != r.RequestID {
		t.Fatalf("got %v %q %v", ts, id, err)
	}
	for _, bad := range []string{"%%%", "bm9jb2xvbg", "MTIzOg"} {
		if _, _, err := decodeCursor(bad); err == nil {
			t.Errorf("cursor %q should be rejected", bad)
		}
	}
}

func TestAutoInterval(t *testing.T) {
	for d, want := range map[time.Duration]time.Duration{
		15 * time.Minute: 10 * time.Second, time.Hour: 30 * time.Second, 24 * time.Hour: 15 * time.Minute, 30 * 24 * time.Hour: 6 * time.Hour,
	} {
		if got := AutoInterval(d); got != want {
			t.Errorf("AutoInterval(%s) = %s, want %s", d, got, want)
		}
	}
}
