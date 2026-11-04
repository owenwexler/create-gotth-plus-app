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
	// cgpa:if password
	"github.com/owenwexler/gotth-plus/internal/mail"
	// cgpa:end
	"github.com/owenwexler/gotth-plus/internal/middleware"
	"github.com/owenwexler/gotth-plus/internal/repository"
	"github.com/owenwexler/gotth-plus/internal/requestid"
	"github.com/owenwexler/gotth-plus/internal/seed"
	"github.com/owenwexler/gotth-plus/internal/service"
	// cgpa:if oauth
	"github.com/owenwexler/gotth-plus/internal/view"
	// cgpa:end
)

// Option changes how NewHandler builds the app. Tests use them to swap in fakes.
type Option func(*options)

type options struct {
	// cgpa:if password
	mailer mail.Mailer
	// cgpa:end
	// cgpa:if oauth
	endpoints map[string]auth.Endpoints
	// cgpa:end
}

// WithMailer sends the app's emails through m instead of SMTP (or the log, in development).
//
// cgpa:if password
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

// WithEndpoints points the named sign-in provider ("google", "github", "apple") at other URLs.
// Tests use it to talk to a fake provider.
//
// cgpa:end
// cgpa:if oauth
func WithEndpoints(provider string, e auth.Endpoints) Option {
	return func(o *options) {
		if o.endpoints == nil {
			o.endpoints = map[string]auth.Endpoints{}
		}
		o.endpoints[provider] = e
	}
}

// NewHandler wires the dependencies, registers every route, and wraps them in the global middleware.
//
// cgpa:end
func NewHandler(env config.Env, db *gorm.DB, opts ...Option) http.Handler {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	// cgpa:if password
	if o.mailer == nil {
		o.mailer = newMailer(env)
	}
	// cgpa:end

	sessions := auth.NewSessions(db, env.IsProduction())
	users := repository.NewUserRepository(db)
	mux := newMux(env, db, sessions, users, o)

	// the session and the signed-in user are loaded inside the CSRF check (see buildHandler)
	return buildHandler(env, auth.Middleware(sessions, users)(mux))
}

// newMux wires the dependencies and registers every route. Register new routes here.
func newMux(env config.Env, db *gorm.DB, sessions *scs.SessionManager, users repository.UserRepository, o options) *http.ServeMux {
	validate := validator.New()

	exampleRepo := repository.NewExampleRepository(db)
	greetingSvc := service.NewGreetingService()
	healthHandler := handler.NewHealthHandler(db)
	seedHandler := seed.NewSeedHandler(db, exampleRepo)
	authHandlers := &factory.Auth{Sessions: sessions, Users: users, Secure: env.IsProduction()}
	// cgpa:if password
	authHandlers.Login.Password = true
	passwordHandlers := factory.NewPasswordAuth(authHandlers, service.NewAuthService(users, o.mailer, env.BaseURL), validate)
	// cgpa:end
	// cgpa:if oauth
	providers := newProviders(env, o.endpoints)
	for _, p := range providers.All() {
		authHandlers.Login.Providers = append(authHandlers.Login.Providers, view.LoginProvider{Name: p.Name, Label: p.Label})
	}
	oauthHandlers := factory.NewOAuth(authHandlers, providers, service.NewIdentityService(users, repository.NewIdentityRepository(db)))
	// cgpa:end
	// cgpa:if supabase
	authHandlers.Login.Password = true
	supabaseProviders, buttons := supabaseProviderButtons(env.SupabaseProviders)
	authHandlers.Login.Providers = append(authHandlers.Login.Providers, buttons...)
	supabaseHandlers := factory.NewSupabase(authHandlers, auth.NewSupabase(env.SupabaseURL, env.SupabasePublishableKey),
		service.NewIdentityService(users, repository.NewIdentityRepository(db)), validate, env.BaseURL, supabaseProviders)
	// cgpa:end
	// cgpa:if oidc
	// signing out here ends the provider's session too, when it has a logout endpoint
	if p := providers.Get("oidc"); p != nil {
		authHandlers.AfterLogout = func(r *http.Request) string { return p.LogoutURL(r.Context(), env.BaseURL+"/") }
	}
	// cgpa:end

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticFiles("static")))
	mux.HandleFunc("GET /healthz", healthHandler.Check)
	// {$} matches "/" only, so unknown paths 404 instead of rendering the home page
	mux.HandleFunc("GET /{$}", factory.RenderHome())

	// Sign-in. Wrap routes that need a signed-in user in auth.RequireUser.
	mux.HandleFunc("GET /login", authHandlers.LoginPage())
	mux.HandleFunc("POST /logout", authHandlers.Logout())
	mux.Handle("GET /account", auth.RequireUser(authHandlers.Account()))
	mux.Handle("POST /logout-everywhere", auth.RequireUser(authHandlers.LogoutEverywhere()))
	// cgpa:if password
	mux.HandleFunc("POST /login", passwordHandlers.Login())
	mux.HandleFunc("GET /signup", passwordHandlers.SignupPage())
	mux.HandleFunc("POST /signup", passwordHandlers.Signup())
	mux.HandleFunc("GET /verify-email", passwordHandlers.VerifyPage())
	mux.HandleFunc("POST /verify-email", passwordHandlers.Verify())
	mux.HandleFunc("GET /forgot-password", passwordHandlers.ForgotPage())
	mux.HandleFunc("POST /forgot-password", passwordHandlers.Forgot())
	mux.HandleFunc("GET /reset-password", passwordHandlers.ResetPage())
	mux.HandleFunc("POST /reset-password", passwordHandlers.Reset())
	// cgpa:end
	// cgpa:if oauth
	mux.HandleFunc("GET /auth/{provider}/login", oauthHandlers.Begin())
	mux.HandleFunc("GET /auth/{provider}/callback", oauthHandlers.Callback())
	// cgpa:end
	// cgpa:if supabase
	// Supabase Auth: users and passwords live in Supabase; the app keeps its own session
	mux.HandleFunc("POST /login", supabaseHandlers.Login())
	mux.HandleFunc("GET /signup", supabaseHandlers.SignupPage())
	mux.HandleFunc("POST /signup", supabaseHandlers.Signup())
	mux.HandleFunc("GET /forgot-password", supabaseHandlers.ForgotPage())
	mux.HandleFunc("POST /forgot-password", supabaseHandlers.Forgot())
	mux.HandleFunc("GET /reset-password", supabaseHandlers.ResetPage())
	mux.HandleFunc("POST /reset-password", supabaseHandlers.Reset())
	mux.HandleFunc("GET /auth/{provider}/login", supabaseHandlers.Begin())
	mux.HandleFunc("GET /auth/supabase/callback", supabaseHandlers.Callback())
	// cgpa:end
	// cgpa:if apple
	// Apple comes back with a cross-site POST (see buildHandler); Callback refuses POST for the others
	mux.HandleFunc("POST /auth/{provider}/callback", oauthHandlers.Callback())
	// cgpa:end

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
				// cgpa:if password
				if err := seed.DevUser(r.Context(), users, env.SeedUserPassword); err != nil {
					slog.ErrorContext(r.Context(), "seed dev user failed", "err", err)
					http.Error(w, "seed failed", http.StatusInternalServerError)
					return
				}
				// cgpa:end
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
	// cgpa:if apple
	// Apple returns with a cross-site POST (response_mode=form_post). It carries no CSRF token and
	// fails the cross-origin check by design, so this one route skips both. The OAuth state (tied to a
	// cookie set when the sign-in began) and the ID token's nonce protect it instead.
	const appleCallback = "POST /auth/apple/callback"
	crossOrigin.AddInsecureBypassPattern(appleCallback)
	// cgpa:end

	handler := csrf.Middleware(env.IsProduction())(mux)
	// cgpa:if apple
	handler = skipCSRFForAppleCallback(mux, handler)
	// cgpa:end
	handler = middleware.MaxBodyBytes(MaxBodyBytes)(handler)
	handler = crossOrigin.Handler(handler)
	handler = middleware.RateLimit(limits)(handler)
	handler = middleware.Compress(handler)
	handler = middleware.SecurityHeaders(env.IsProduction())(handler)
	handler = requestid.Middleware(handler)

	return handler
}

// skipCSRFForAppleCallback sends Apple's callback past the CSRF check, and everything else through it.
//
// cgpa:if apple
func skipCSRFForAppleCallback(next, withCSRF http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/auth/apple/callback" {
			next.ServeHTTP(w, r)
			return
		}
		withCSRF.ServeHTTP(w, r)
	})
}

// cgpa:end
