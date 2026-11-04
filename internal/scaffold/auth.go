package scaffold

import (
	"strings"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// uuidColumnType is the column type for uuid.UUID fields in the auth models. uuid.UUID has no
// type of its own in GORM; servers need one, and SQLite takes the portable varchar(36).
func uuidColumnType(d options.Database) string {
	switch d {
	case options.Postgres:
		return "uuid"
	case options.MySQL:
		return "char(36)"
	}
	return "varchar(36)"
}

// applyAuth adds built-in sign-in (email and password): the _variants/auth files, the config and
// .env settings, CSRF rotation, and what the docs say about it. It runs before the module is renamed.
func applyAuth(p Project, o options.Options) error {
	if o.Auth != options.BuiltinAuth {
		return nil
	}
	if err := p.overlay("auth"); err != nil {
		return err
	}
	if !o.E2E {
		delete(p, "cmd/server/e2e_auth_test.go") // needs the E2E harness, which is gone
	}
	p["internal/model/user.go"] = []byte(strings.ReplaceAll(string(p["internal/model/user.go"]), "__UUID_TYPE__", uuidColumnType(o.Database)))

	if err := p.edits(authCodeEdits); err != nil {
		return err
	}
	return p.edits(authDocEdits)
}

var authCodeEdits = []edit{
	// config: BaseURL for links in emails, SMTP, and the development account's password
	{"internal/config/config.go",
		"\t// SeedAdminKey optionally protects POST /seed in development. Never used in production.\n\tSeedAdminKey string\n}",
		"\t// SeedAdminKey optionally protects POST /seed in development. Never used in production.\n\tSeedAdminKey string\n" +
			"\t// SeedUserPassword is the password of the development account POST /seed creates\n" +
			"\t// (dev@example.com). Empty: no account. Never used in production.\n" +
			"\tSeedUserPassword string\n\n" +
			"\t// BaseURL is the app's public address, e.g. https://example.com. Links in emails are built from\n" +
			"\t// it, never from the request's Host header, which the client controls. Required in production.\n" +
			"\tBaseURL string\n" +
			"\t// SMTP server for sign-up and password reset emails. Required in production; in development,\n" +
			"\t// emails go to the log while SMTPHost is empty.\n" +
			"\tSMTPHost     string\n\tSMTPPort     string\n\tSMTPUsername string\n\tSMTPPassword string\n\tMailFrom     string\n}"},
	{"internal/config/config.go",
		"\t\tSeedAdminKey: os.Getenv(\"SeedAdminKey\"),\n",
		"\t\tSeedAdminKey: os.Getenv(\"SeedAdminKey\"),\n" +
			"\t\tSeedUserPassword: os.Getenv(\"SeedUserPassword\"),\n" +
			"\t\tBaseURL: os.Getenv(\"BaseURL\"),\n" +
			"\t\tSMTPHost: os.Getenv(\"SMTPHost\"),\n\t\tSMTPPort: os.Getenv(\"SMTPPort\"),\n" +
			"\t\tSMTPUsername: os.Getenv(\"SMTPUsername\"),\n\t\tSMTPPassword: os.Getenv(\"SMTPPassword\"),\n" +
			"\t\tMailFrom: os.Getenv(\"MailFrom\"),\n"},
	{"internal/config/config.go",
		"\treturn env\n}",
		"\tif env.BaseURL == \"\" && !env.IsProduction() {\n" +
			"\t\tenv.BaseURL = \"http://localhost:7331\" // make dev's live-reload proxy\n" +
			"\t}\n" +
			"\tbase, err := url.Parse(env.BaseURL)\n" +
			"\tif err != nil || base.Host == \"\" || base.Path != \"\" && base.Path != \"/\" || (base.Scheme != \"https\" && (env.IsProduction() || base.Scheme != \"http\")) {\n" +
			"\t\tpanic(\"BaseURL must be the app's address without a path, https in production (e.g. https://example.com)\")\n" +
			"\t}\n" +
			"\tenv.BaseURL = strings.TrimSuffix(env.BaseURL, \"/\")\n\n" +
			"\tif env.IsProduction() && (env.SMTPHost == \"\" || env.MailFrom == \"\") {\n" +
			"\t\tpanic(\"SMTPHost and MailFrom must be set in production: sign-up and password reset send emails\")\n" +
			"\t}\n" +
			"\tif env.SMTPPort == \"\" {\n\t\tenv.SMTPPort = \"587\"\n\t}\n\n" +
			"\treturn env\n}"},
	{"internal/config/config.go", "\t\"os\"\n", "\t\"net/url\"\n\t\"os\"\n\t\"strings\"\n"},

	// CSRF: a new token at every sign-in and sign-out
	{"internal/csrf/csrf.go", "func newToken() (string, error) {",
		"// Rotate replaces the token cookie. Call it when someone signs in or out, so a token planted or\n" +
			"// seen before then stops working. The next full page load renders the new token.\n" +
			"func Rotate(w http.ResponseWriter, secure bool) error {\n" +
			"\ttoken, err := newToken()\n\tif err != nil {\n\t\treturn err\n\t}\n" +
			"\thttp.SetCookie(w, &http.Cookie{\n\t\tName:     CookieName,\n\t\tValue:    token,\n\t\tPath:     \"/\",\n" +
			"\t\tHttpOnly: true,\n\t\tSecure:   secure,\n\t\tSameSite: http.SameSiteLaxMode,\n\t})\n\treturn nil\n}\n\n" +
			"func newToken() (string, error) {"},

	// every model, not only the example, in the seed route and the tests
	{"internal/seed/seed.go", "h.db.AutoMigrate(&model.Example{})", "h.db.AutoMigrate(model.All()...)"},
	{"cmd/server/testutil_test.go", "db.AutoMigrate(&model.Example{})", "db.AutoMigrate(model.All()...)"},

	// .env.example (setEnv adds a random SeedUserPassword to .env)
	{".env.example", "\nSeedAdminKey=\n",
		"\nSeedAdminKey=\n\n" +
			"# Optional, development only. POST /seed creates dev@example.com with this password (15+ characters).\n" +
			"SeedUserPassword=\n\n" +
			"# The app's public address; links in emails use it. Required in production (https).\n" +
			"BaseURL=http://localhost:7331\n\n" +
			"# SMTP server for sign-up and password reset emails. Required in production. In development, an\n" +
			"# empty SMTPHost writes the emails (with their links) to the log instead.\n" +
			"SMTPHost=\nSMTPPort=587\nSMTPUsername=\nSMTPPassword=\nMailFrom=\n"},
}

var authDocEdits = []edit{
	// README
	{"README.md", "- A dev-only `/seed` route and a script that runs it with a valid CSRF token\n",
		"- A dev-only `/seed` route and a script that runs it with a valid CSRF token\n" +
			"- Sign-in with email and password: argon2id, server-side sessions, confirmation and reset emails, per-account rate limits\n"},
	{"README.md", "make seed     # POST /seed with a valid CSRF token; loads seedData.json\n```\n",
		"make seed     # POST /seed with a valid CSRF token; loads seedData.json\n```\n\n" +
			"It also creates `dev@example.com`, confirmed, with the password in `SeedUserPassword` (`.env`).\n" +
			"In development, emails (with their links) are written to the server log; set `SMTPHost` to send them.\n"},
	{"README.md", "  config/           environment loading and validation\n",
		"  auth/             sessions, the signed-in user, password hashing, per-account limits\n  config/           environment loading and validation\n"},
	{"README.md", "  middleware/       security headers, rate limit, compression, body limit, admin key\n",
		"  mail/             emails: SMTP in production, the log in development\n  middleware/       security headers, rate limit, compression, body limit, admin key\n"},
	{"README.md", "| POST | `/hello` | Example app: greeting fragment |\n",
		"| GET, POST | `/login` | Sign in |\n" +
			"| GET, POST | `/signup` | Create an account (emails a confirmation link) |\n" +
			"| GET, POST | `/verify-email` | Confirm the email (the link opens a page; the button posts) |\n" +
			"| GET, POST | `/forgot-password`, `/reset-password` | Password reset by email |\n" +
			"| GET | `/account` | The signed-in user's account |\n" +
			"| POST | `/logout`, `/logout-everywhere` | Sign out here, or on every device |\n" +
			"| POST | `/hello` | Example app: greeting fragment |\n"},

	// AGENTS.md
	{"AGENTS.md", "`SeedAdminKey`); see `.env.example`.",
		"`SeedAdminKey`, `SeedUserPassword`, `BaseURL`, `SMTPHost`/`SMTPPort`/`SMTPUsername`/`SMTPPassword`/`MailFrom`); see `.env.example`."},
	{"AGENTS.md",
		"- Middleware order (outermost first): request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF.",
		"- Middleware order (outermost first): request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF, then the session and the signed-in user (`auth.Middleware`, skipped for `/static/`).\n" +
			"- Auth: email and password (argon2id in `internal/auth`, flows in `internal/service/auth.go`, handlers in `internal/factory/auth.go`). " +
			"Sessions are server-side in the `sessions` table (`alexedwards/scs`; cookie `__Host-session` in production). " +
			"`auth.CurrentUser(ctx)` is the signed-in user; wrap routes that need one in `auth.RequireUser`, and authorise every object access by fetching with `(id, owner_id)`. " +
			"Sign-in renews the session token and replaces the CSRF token (`csrf.Rotate`). Raising `User.SessionVersion` ends all of a user's sessions (password reset, \"Sign out everywhere\"). " +
			"Emailed links are single-use tokens stored as SHA-256 (`model.AuthToken`), built from `BaseURL`, and open a page whose button POSTs. " +
			"Every model is listed in `model.All()` (AutoMigrate in the seed route and the tests)."},

	// SECURITY.md
	{"SECURITY.md",
		"It ships **no authentication, no\nauthorisation and no user data**. Those are yours to add, and most of the guidance below is about\nadding them without undoing the defaults.",
		"This project also signs users in\nwith an email and a password. Authorisation (who may see what) is yours to add, and most of the\nguidance below is about adding it without undoing the defaults."},
	{"SECURITY.md",
		"- [ ] Config comes from the platform's environment or secret store, not from a `.env` file on the\n      server. `.env` stays git-ignored.\n",
		"- [ ] Config comes from the platform's environment or secret store, not from a `.env` file on the\n      server. `.env` stays git-ignored.\n" +
			"- [ ] `BaseURL` is the public `https://` address, and SMTP is configured (`SMTPHost`, `MailFrom`);\n" +
			"      production refuses to start without them. `SeedUserPassword` is not set.\n" +
			"- [ ] The `users`, `identities`, `auth_tokens` and `sessions` tables exist (reviewed migrations;\n" +
			"      `AutoMigrate` only runs in the development seed route).\n"},
	{"SECURITY.md", "### Authentication and sessions (when you add users)\n\nThe template has none of this yet. When you add it:\n",
		"### Authentication and sessions\n\n" +
			"This project signs users in with an email and a password. What it does, so you keep doing it as\n" +
			"you build on it:\n\n" +
			"- Passwords are hashed with argon2id (`internal/auth/password.go`), 15 to 256 characters, with no\n" +
			"  rules about kinds of characters (NIST SP 800-63B-4). Unknown emails cost the same time as wrong\n" +
			"  passwords (`VerifyDummy`), and sign-in, sign-up and reset answer the same whether or not an\n" +
			"  account exists.\n" +
			"- Sessions are stored server-side and can be revoked: `__Host-session` (HttpOnly, Secure,\n" +
			"  SameSite=Lax) in production, 7 days at most and 24 hours idle. Sign-in issues a new session token\n" +
			"  and a new CSRF token. A password reset and \"Sign out everywhere\" end every session of the user.\n" +
			"- Emailed links carry single-use, expiring tokens stored only as SHA-256. They are built from\n" +
			"  `BaseURL`, never from the Host header (which would allow reset-link poisoning), open a page with\n" +
			"  `Referrer-Policy: no-referrer`, and change nothing until its button posts.\n" +
			"- Sign-in, sign-up and reset emails are rate-limited per email address as well as per IP.\n" +
			"- `?next=` after sign-in only accepts paths on this site (`auth.SafeNext`).\n\n" +
			"When you add more:\n"},
	{"SECURITY.md",
		"- Never log passwords, session IDs, CSRF tokens, `X-Admin-Key`, full request bodies, or personal\n  data. Be careful logging whole headers or form values.\n",
		"- Never log passwords, session IDs, CSRF tokens, `X-Admin-Key`, full request bodies, or personal\n  data. Be careful logging whole headers or form values.\n" +
			"- In development, without `SMTPHost`, emails and their links go to the log (`mail.LogMailer`). Those\n" +
			"  links work like passwords; production refuses to start without SMTP, so this never happens there.\n"},
	{"SECURITY.md",
		"- No authentication, authorisation or sessions. Every route is public.\n- The CSRF token is not tied to a session (there are no sessions) and uses a plain cookie name.\n",
		"- Sign-in is email and password only: no second factor (TOTP or passkeys) yet.\n" +
			"- Authorisation is yours: `auth.RequireUser` only checks that someone is signed in.\n" +
			"- The CSRF token is a double-submit cookie, replaced at sign-in and sign-out but not bound to the\n" +
			"  session, and uses a plain cookie name.\n" +
			"- Sign-in doesn't work without JavaScript: the forms rely on htmx sending the CSRF header.\n"},
}
