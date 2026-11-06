package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

func TestSecurityHeaders(t *testing.T) {
	for _, hsts := range []bool{false, true} {
		rec := httptest.NewRecorder()
		SecurityHeaders(hsts)(ok).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "Cross-Origin-Opener-Policy"} {
			if rec.Header().Get(h) == "" {
				t.Errorf("hsts=%v: missing %s", hsts, h)
			}
		}
		if got := rec.Header().Get("Strict-Transport-Security") != ""; got != hsts {
			t.Errorf("HSTS present = %v, want %v", got, hsts)
		}
	}
}

func TestMaxBodyBytes(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := MaxBodyBytes(10)(read)

	for _, tt := range []struct {
		body string
		want int
	}{{"0123456789", 200}, {"0123456789A", 413}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(tt.body)))
		if rec.Code != tt.want {
			t.Errorf("%d-byte body: status %d, want %d", len(tt.body), rec.Code, tt.want)
		}
	}

	// chunked bodies have no Content-Length, so the reader limit must catch them
	req := httptest.NewRequest("POST", "/", strings.NewReader("0123456789A"))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Errorf("unknown-length body: status %d, want 413", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	h := RateLimit(RateLimits{
		All:       Limit{Rate: 0.001, Burst: 5},
		Mutations: Limit{Rate: 0.001, Burst: 2},
	})(ok)

	do := func(method, path, addr string, hx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = addr
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// mutations hit their own, smaller bucket
	for i, want := range []int{200, 200, 429} {
		if got := do("POST", "/hello", "1.1.1.1:1000", false).Code; got != want {
			t.Fatalf("mutation %d: status %d, want %d", i, got, want)
		}
	}

	// a different IP is unaffected; the port doesn't matter for the same IP
	if got := do("GET", "/", "2.2.2.2:1000", false).Code; got != 200 {
		t.Fatalf("other IP: %d", got)
	}
	if got := do("POST", "/hello", "1.1.1.1:2000", false).Code; got != 429 {
		t.Fatalf("same IP, new port should share the bucket: %d", got)
	}

	// static assets are never limited, even once the general bucket is empty
	for i := 0; i < 20; i++ {
		if got := do("GET", "/static/js/htmx.min.js", "3.3.3.3:1", false).Code; got != 200 {
			t.Fatalf("static request %d limited: %d", i, got)
		}
	}

	// the general bucket (burst 5) runs out, and htmx gets a toast it can swap
	var rec *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		rec = do("GET", "/", "4.4.4.4:1", true)
	}
	if rec.Code != 429 || rec.Header().Get("HX-Reswap") != "none" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("htmx 429: code=%d headers=%v", rec.Code, rec.Header())
	}
	if !strings.Contains(rec.Body.String(), "toasts") {
		t.Errorf("expected an out of band toast, got %q", rec.Body.String())
	}
}
