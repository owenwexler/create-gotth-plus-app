// Package app builds the full HTTP handler (routes plus global middleware) so that
// cmd/server and the integration tests run exactly the same wiring.
package app

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-playground/validator/v10"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/config"
	"github.com/owenwexler/gotth-plus/internal/csrf"
	"github.com/owenwexler/gotth-plus/internal/factory"
	"github.com/owenwexler/gotth-plus/internal/handler"
	"github.com/owenwexler/gotth-plus/internal/mail"
	"github.com/owenwexler/gotth-plus/internal/middleware"
	"github.com/owenwexler/gotth-plus/internal/repository"
	"github.com/owenwexler/gotth-plus/internal/requestid"
	"github.com/owenwexler/gotth-plus/internal/seed"
	"github.com/owenwexler/gotth-plus/internal/service"
)

// Option changes how NewHandler builds the app. Tests use them to swap in fakes.
type Option func(*options)

type options struct {
	mailer mail.Mailer
}

// WithMailer sends the app's emails through m instead of SMTP (or the log, in development).
func WithMailer(m mail.Mailer) Option {
	return func(o *options) { o.mailer = m }
}

// newMailer is SMTP when SMTPHost is set, and the log otherwise (config refuses production without SMTP).
func newMailer(env config.Env) mail.Mailer {
	if env.SMTPHost == "" {
		return mail.LogMailer{}
	}
	return mail.SMTPMailer{Host: env.SMTPHost, Port: env.SMTPPort, Username: env.SMTPUsername, Password: env.SMTPPassword, From: env.MailFrom}
}

// NewHandler wires the dependencies, registers every route, and wraps them in the global middleware.
func NewHandler(env config.Env, db *gorm.DB, opts ...Option) http.Handler {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.mailer == nil {
		o.mailer = newMailer(env)
	}

	sessions := auth.NewSessions(db, env.IsProduction())
	users := repository.NewUserRepository(db)
	mux := newMux(env, db, sessions, users, o.mailer)

	// the session and the signed-in user are loaded inside the CSRF check (see buildHandler)
	return buildHandler(env, auth.Middleware(sessions, users)(mux))
}

// newMux wires the dependencies and registers every route. Register new routes here.
func newMux(env config.Env, db *gorm.DB, sessions *scs.SessionManager, users repository.UserRepository, mailer mail.Mailer) *http.ServeMux {
	validate := validator.New()

	exampleRepo := repository.NewExampleRepository(db)
	greetingSvc := service.NewGreetingService()
	healthHandler := handler.NewHealthHandler(db)
	seedHandler := seed.NewSeedHandler(db, exampleRepo)
	authHandlers := factory.NewAuth(sessions, service.NewAuthService(users, mailer, env.BaseURL), users, validate, env.IsProduction())

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticFiles("static")))
	mux.HandleFunc("GET /healthz", healthHandler.Check)
	// {$} matches "/" only, so unknown paths 404 instead of rendering the home page
	mux.HandleFunc("GET /{$}", factory.RenderHome())

	// Sign-in. Wrap routes that need a signed-in user in auth.RequireUser.
	mux.HandleFunc("GET /login", authHandlers.LoginPage())
	mux.HandleFunc("POST /login", authHandlers.Login())
	mux.HandleFunc("POST /logout", authHandlers.Logout())
	mux.HandleFunc("GET /signup", authHandlers.SignupPage())
	mux.HandleFunc("POST /signup", authHandlers.Signup())
	mux.HandleFunc("GET /verify-email", authHandlers.VerifyPage())
	mux.HandleFunc("POST /verify-email", authHandlers.Verify())
	mux.HandleFunc("GET /forgot-password", authHandlers.ForgotPage())
	mux.HandleFunc("POST /forgot-password", authHandlers.Forgot())
	mux.HandleFunc("GET /reset-password", authHandlers.ResetPage())
	mux.HandleFunc("POST /reset-password", authHandlers.Reset())
	mux.Handle("GET /account", auth.RequireUser(authHandlers.Account()))
	mux.Handle("POST /logout-everywhere", auth.RequireUser(authHandlers.LogoutEverywhere()))

	// Example app: see "Removing the example app" in README.md
	mux.HandleFunc("POST /hello", factory.Hello(greetingSvc, validate))

	// Dev-only: the route is not registered at all in production, so it 404s.
	if !env.IsProduction() {
		mux.Handle("POST /seed", middleware.RequireAdminKey(env.SeedAdminKey,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := seedHandler.Seed(r); err != nil {
					slog.ErrorContext(r.Context(), "seed failed", "err", err)
					http.Error(w, "seed failed", http.StatusInternalServerError)
					return
				}
				if err := seed.DevUser(r.Context(), users, env.SeedUserPassword); err != nil {
					slog.ErrorContext(r.Context(), "seed dev user failed", "err", err)
					http.Error(w, "seed failed", http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})))
	}

	return mux
}

// staticFiles serves the files in dir, but never a directory listing.
func staticFiles(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// MaxBodyBytes caps every request body; raise it for routes that accept uploads.
const MaxBodyBytes = 1 << 20

// buildHandler wraps the mux in the global middleware. Outermost runs first, so cheap rejections
// (rate limit, cross-origin, body size, CSRF) happen before any handler work.
func buildHandler(env config.Env, mux http.Handler) http.Handler {
	// Limits are per client IP. Development is far looser so test suites and live reload don't trip them.
	limits := middleware.RateLimits{
		All:       middleware.Limit{Rate: 20, Burst: 60},
		Mutations: middleware.Limit{Rate: 5, Burst: 20},
	}
	if !env.IsProduction() {
		limits = middleware.RateLimits{
			All:       middleware.Limit{Rate: 1000, Burst: 2000},
			Mutations: middleware.Limit{Rate: 500, Burst: 1000},
		}
	}

	// Rejects cross-origin browser requests using Sec-Fetch-Site/Origin (defence in depth next to the CSRF token).
	// Requests without those headers (curl, tests) pass; they are covered by the CSRF token check.
	crossOrigin := http.NewCrossOriginProtection()
	if !env.IsProduction() {
		// templ's live-reload proxy serves the app from another port
		for _, origin := range []string{"http://localhost:7331", "http://127.0.0.1:7331"} {
			if err := crossOrigin.AddTrustedOrigin(origin); err != nil {
				slog.Error("trusting dev origin", "origin", origin, "err", err)
			}
		}
	}

	handler := csrf.Middleware(env.IsProduction())(mux)
	handler = middleware.MaxBodyBytes(MaxBodyBytes)(handler)
	handler = crossOrigin.Handler(handler)
	handler = middleware.RateLimit(limits)(handler)
	handler = middleware.Compress(handler)
	handler = middleware.SecurityHeaders(env.IsProduction())(handler)
	handler = requestid.Middleware(handler)

	return handler
}
