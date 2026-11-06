package requestid

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware(t *testing.T) {
	var seen string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	}))

	tests := []struct {
		name, incoming string
		keep           bool
	}{
		{"generated when absent", "", false},
		{"valid incoming kept", "abc-123_XYZ", true},
		{"too long replaced", strings.Repeat("a", 65), false},
		{"newline (log injection) replaced", "abc\nINFO fake", false},
		{"spaces replaced", "a b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.incoming != "" {
				req.Header.Set(HeaderName, tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(HeaderName)
			if got == "" || got != seen {
				t.Fatalf("header %q must be set and match context %q", got, seen)
			}
			if tt.keep != (got == tt.incoming) {
				t.Fatalf("kept=%v, header=%q, incoming=%q", tt.keep, got, tt.incoming)
			}
		})
	}

	// generated IDs are unique
	a, b := httptest.NewRecorder(), httptest.NewRecorder()
	h.ServeHTTP(a, httptest.NewRequest("GET", "/", nil))
	h.ServeHTTP(b, httptest.NewRequest("GET", "/", nil))
	if a.Header().Get(HeaderName) == b.Header().Get(HeaderName) {
		t.Fatal("IDs must differ between requests")
	}
}

func TestLogHandlerAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewLogHandler(slog.NewTextHandler(&buf, nil)))

	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.ErrorContext(r.Context(), "boom")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(HeaderName, "req-42")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "request_id=req-42") {
		t.Fatalf("log line missing request_id: %q", buf.String())
	}

	buf.Reset()
	logger.Error("no request")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("no request_id expected outside a request: %q", buf.String())
	}
}
