// Package requestid tags every request with an ID, returns it in the X-Request-ID response header,
// and adds it to every log line written through slog's *Context functions.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
)

const HeaderName = "X-Request-ID"

const maxIncomingLen = 64

type ctxKey struct{}

// FromContext returns the request ID for ctx, or "" outside a request.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// Middleware assigns each request an ID. A well-formed incoming X-Request-ID (e.g. from a proxy) is kept
// so IDs can be followed across services; anything else is replaced, since the value ends up in logs.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderName)
		if !validIncoming(id) {
			id = newID()
		}

		w.Header().Set(HeaderName, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func validIncoming(id string) bool {
	if id == "" || len(id) > maxIncomingLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// LogHandler wraps a slog.Handler so records logged with a request's context carry request_id.
type LogHandler struct{ slog.Handler }

func NewLogHandler(next slog.Handler) *LogHandler { return &LogHandler{next} }

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{h.Handler.WithAttrs(attrs)}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{h.Handler.WithGroup(name)}
}
