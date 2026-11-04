package handler

import (
	"log/slog"
	"net/http"

	"gorm.io/gorm"
)

// HealthHandler answers liveness/readiness probes.
type HealthHandler struct {
	db *gorm.DB
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(db *gorm.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// Check returns 200 when the database answers a ping, and 503 otherwise.
func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	sqlDB, err := h.db.DB()
	if err == nil {
		err = sqlDB.PingContext(r.Context())
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "health check", "err", err)
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}
