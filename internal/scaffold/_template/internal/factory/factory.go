package factory

import (
	"log/slog"
	"net/http"

	"github.com/owenwexler/gotth-plus/internal/view"
)

// logError records an error server-side only. Clients only ever see the generic toast or error page, so
// internals (SQL, paths, driver messages) never leave the server.
func logError(r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, "method", r.Method, "path", r.URL.Path, "err", err)
}

// logRejected records input that failed validation. That is the client's mistake, not ours, so it is a warning.
func logRejected(r *http.Request, msg string, err error) {
	slog.WarnContext(r.Context(), msg, "method", r.Method, "path", r.URL.Path, "err", err)
}

// respondWithErrorPage logs err and sends the generic error page with the given status.
func respondWithErrorPage(w http.ResponseWriter, r *http.Request, status int, msg string, err error) {
	logError(r, msg, err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// htmx skips swapping error statuses; app.js swaps them anyway when HX-Reswap says how, so the user
	// sees this page in the request's target instead of nothing
	w.Header().Set("HX-Reswap", "innerHTML")
	w.WriteHeader(status)
	_ = view.ErrorPage().Render(r.Context(), w)
}

// respondWithToast sends only a toast.  HX-Reswap: none leaves the page as it was (app.js allows
// swapping error statuses for this case), so the UI keeps showing its original state.
func respondWithToast(w http.ResponseWriter, r *http.Request, status int, toast view.ToastArgs) {
	w.Header().Set("HX-Reswap", "none")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = view.Toast(toast).Render(r.Context(), w)
}

func RenderHome() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if err := view.Home().Render(r.Context(), w); err != nil {
			logError(r, "render home", err)
		}
	}
}
