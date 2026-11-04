package scaffold

import (
	"fmt"
	"path"
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

// authConditions are the names //cgpa:if blocks in the auth variants can test.
func authConditions(o options.Options) map[string]bool {
	return map[string]bool{
		"password": o.HasMethod(options.Password),
		"oauth":    o.HasOAuth(),
		"google":   o.HasMethod(options.Google),
		"github":   o.HasMethod(options.GitHub),
		"apple":    o.HasMethod(options.Apple),
		"e2e":      o.E2E,
	}
}

// stripBlocks keeps or drops the lines between "// cgpa:if a b" and "// cgpa:end": they stay when
// any of the named conditions is true. The marker lines themselves always go. Blocks don't nest.
//
// The markers have a space after "//" on purpose: gofmt treats "//word:" comments as directives
// (like //go:build) and moves them to the end of a doc comment, which would leave doc comments
// outside their blocks.
func stripBlocks(src string, conds map[string]bool) (string, error) {
	var out []string
	in, keep := false, true
	for i, line := range strings.SplitAfter(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "//cgpa:"):
			return "", fmt.Errorf("line %d: write markers as \"// cgpa:\" (with a space), or gofmt moves them", i+1)
		case strings.HasPrefix(trimmed, "// cgpa:if "):
			if in {
				return "", fmt.Errorf("line %d: // cgpa:if inside another block", i+1)
			}
			in, keep = true, false
			for _, name := range strings.Fields(strings.TrimPrefix(trimmed, "// cgpa:if ")) {
				v, ok := conds[name]
				if !ok {
					return "", fmt.Errorf("line %d: unknown condition %q", i+1, name)
				}
				keep = keep || v
			}
		case trimmed == "// cgpa:end":
			if !in {
				return "", fmt.Errorf("line %d: // cgpa:end without // cgpa:if", i+1)
			}
			in, keep = false, true
		case strings.HasPrefix(trimmed, "// cgpa:"):
			return "", fmt.Errorf("line %d: unknown marker %q", i+1, trimmed)
		default:
			if keep {
				out = append(out, line)
			}
		}
	}
	if in {
		return "", fmt.Errorf("// cgpa:if without // cgpa:end")
	}
	return strings.Join(out, ""), nil
}

// applyAuth adds built-in sign-in: the shared core (_variants/auth), the password and OAuth parts
// for the chosen methods, their settings, and what the docs say about them. It runs before the
// module is renamed.
func applyAuth(p Project, o options.Options) error {
	if o.Auth != options.BuiltinAuth {
		return nil
	}
	conds := authConditions(o)
	variants := []string{"auth"}
	if conds["password"] {
		variants = append(variants, "auth-password")
	}
	if conds["oauth"] {
		variants = append(variants, "auth-oauth")
	}
	for _, v := range variants {
		written, err := p.overlay(v)
		if err != nil {
			return err
		}
		for _, name := range written {
			if path.Ext(name) != ".go" {
				continue
			}
			s, err := stripBlocks(string(p[name]), conds)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			p[name] = []byte(s)
		}
	}
	if !o.E2E {
		// they need the E2E harness, which is gone
		delete(p, "cmd/server/e2e_auth_test.go")
		delete(p, "cmd/server/e2e_oauth_test.go")
	}
	p["internal/model/user.go"] = []byte(strings.ReplaceAll(string(p["internal/model/user.go"]), "__UUID_TYPE__", uuidColumnType(o.Database)))

	if err := p.edits(authCodeEdits(o)); err != nil {
		return err
	}
	return p.edits(authDocEdits(o))
}

// setting is one .env setting the chosen sign-in methods add to config.Env.
type setting struct {
	name    string
	comment string // above the field, without "// "
}

// authSettings are the config fields, grouped by what needs them.
func authSettings(o options.Options) (fields []setting, env string) {
	fields = append(fields, setting{"BaseURL", "BaseURL is the app's public address, e.g. https://example.com. Links in emails and the\n// sign-in providers' redirect URLs are built from it, never from the request's Host header,\n// which the client controls. Required in production."})
	env = "\n# The app's public address. Links in emails and the providers' redirect URLs use it.\n" +
		"# Required in production (https).\nBaseURL=http://localhost:7331\n"

	if o.HasMethod(options.Password) {
		fields = append(fields,
			setting{"SeedUserPassword", "SeedUserPassword is the password of the development account POST /seed creates\n// (dev@example.com). Empty: no account. Never used in production."},
			setting{"SMTPHost", "SMTP server for sign-up and password reset emails. Required in production; in development,\n// emails go to the log while SMTPHost is empty."},
			setting{"SMTPPort", ""}, setting{"SMTPUsername", ""}, setting{"SMTPPassword", ""}, setting{"MailFrom", ""})
		env += "\n# Optional, development only. POST /seed creates dev@example.com with this password (15+ characters).\n" +
			"SeedUserPassword=\n\n" +
			"# SMTP server for sign-up and password reset emails. Required in production. In development, an\n" +
			"# empty SMTPHost writes the emails (with their links) to the log instead.\n" +
			"SMTPHost=\nSMTPPort=587\nSMTPUsername=\nSMTPPassword=\nMailFrom=\n"
	}
	if o.HasMethod(options.Google) {
		fields = append(fields, setting{"GoogleClientID", "Google sign-in (https://console.cloud.google.com/apis/credentials). Required in production;\n// in development, Google sign-in is off while they are empty."}, setting{"GoogleClientSecret", ""})
		env += "\n# Google sign-in: an OAuth client at https://console.cloud.google.com/apis/credentials, with\n" +
			"# <BaseURL>/auth/google/callback as an authorized redirect URI. Required in production.\n" +
			"GoogleClientID=\nGoogleClientSecret=\n"
	}
	if o.HasMethod(options.GitHub) {
		fields = append(fields, setting{"GitHubClientID", "GitHub sign-in (https://github.com/settings/developers). Required in production; in\n// development, GitHub sign-in is off while they are empty."}, setting{"GitHubClientSecret", ""})
		env += "\n# GitHub sign-in: an OAuth app at https://github.com/settings/developers, with\n" +
			"# <BaseURL>/auth/github/callback as its callback URL. Required in production.\n" +
			"GitHubClientID=\nGitHubClientSecret=\n"
	}
	if o.HasMethod(options.Apple) {
		fields = append(fields, setting{"AppleClientID", "Sign in with Apple: the Services ID, team, key ID and the key's .p8 file. Required in\n// production; in development, Apple sign-in is off while they are empty."},
			setting{"AppleTeamID", ""}, setting{"AppleKeyID", ""}, setting{"ApplePrivateKeyFile", ""})
		env += "\n# Sign in with Apple: a Services ID with <BaseURL>/auth/apple/callback as its return URL (Apple\n" +
			"# only allows https), and a key with its .p8 file, from the Apple developer account. Keep the .p8\n" +
			"# file outside the project (it is a secret). Required in production.\n" +
			"AppleClientID=\nAppleTeamID=\nAppleKeyID=\nApplePrivateKeyFile=\n"
	}
	return fields, env
}

func authCodeEdits(o options.Options) []edit {
	fields, env := authSettings(o)
	var decl, read strings.Builder
	for _, f := range fields {
		if f.comment != "" {
			decl.WriteString("\n\t// " + strings.ReplaceAll(f.comment, "\n// ", "\n\t// ") + "\n")
		}
		decl.WriteString("\t" + f.name + " string\n")
		read.WriteString("\t\t" + f.name + ": os.Getenv(\"" + f.name + "\"),\n")
	}

	checks := "\tif env.BaseURL == \"\" && !env.IsProduction() {\n" +
		"\t\tenv.BaseURL = \"http://localhost:7331\" // make dev's live-reload proxy\n" +
		"\t}\n" +
		"\tbase, err := url.Parse(env.BaseURL)\n" +
		"\tif err != nil || base.Host == \"\" || base.Path != \"\" && base.Path != \"/\" || (base.Scheme != \"https\" && (env.IsProduction() || base.Scheme != \"http\")) {\n" +
		"\t\tpanic(\"BaseURL must be the app's address without a path, https in production (e.g. https://example.com)\")\n" +
		"\t}\n" +
		"\tenv.BaseURL = strings.TrimSuffix(env.BaseURL, \"/\")\n"
	required := func(what string, names ...string) string {
		var conds []string
		for _, n := range names {
			conds = append(conds, "env."+n+" == \"\"")
		}
		return "\tif env.IsProduction() && (" + strings.Join(conds, " || ") + ") {\n" +
			"\t\tpanic(\"" + strings.Join(names, ", ") + " must be set in production: " + what + "\")\n\t}\n"
	}
	if o.HasMethod(options.Password) {
		checks += required("sign-up and password reset send emails", "SMTPHost", "MailFrom") +
			"\tif env.SMTPPort == \"\" {\n\t\tenv.SMTPPort = \"587\"\n\t}\n"
	}
	if o.HasMethod(options.Google) {
		checks += required("Google sign-in is on", "GoogleClientID", "GoogleClientSecret")
	}
	if o.HasMethod(options.GitHub) {
		checks += required("GitHub sign-in is on", "GitHubClientID", "GitHubClientSecret")
	}
	if o.HasMethod(options.Apple) {
		checks += required("Sign in with Apple is on", "AppleClientID", "AppleTeamID", "AppleKeyID", "ApplePrivateKeyFile")
	}

	return []edit{
		{"internal/config/config.go",
			"\t// SeedAdminKey optionally protects POST /seed in development. Never used in production.\n\tSeedAdminKey string\n}",
			"\t// SeedAdminKey optionally protects POST /seed in development. Never used in production.\n\tSeedAdminKey string\n" + decl.String() + "}"},
		{"internal/config/config.go", "\t\tSeedAdminKey: os.Getenv(\"SeedAdminKey\"),\n", "\t\tSeedAdminKey: os.Getenv(\"SeedAdminKey\"),\n" + read.String()},
		{"internal/config/config.go", "\treturn env\n}", checks + "\n\treturn env\n}"},
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
		{".env.example", "\nSeedAdminKey=\n", "\nSeedAdminKey=\n" + env},
	}
}

func authDocEdits(o options.Options) []edit {
	password := o.HasMethod(options.Password)
	var providers []string
	for _, m := range o.AuthMethods {
		if m != options.Password {
			providers = append(providers, m.Label())
		}
	}
	oauth := len(providers) > 0
	providerList := strings.Join(providers, ", ")

	var methods []string
	if password {
		methods = append(methods, "an email and a password")
	}
	if oauth {
		methods = append(methods, providerList)
	}
	with := strings.Join(methods, ", or with ")

	// README
	var what []string
	if password {
		what = append(what, "email and password (argon2id, confirmation and reset emails, per-account rate limits)")
	}
	if oauth {
		what = append(what, providerList+" (OAuth with state, nonce and PKCE)")
	}
	routes := "| GET | `/login` | Sign in |\n"
	if password {
		routes += "| POST | `/login` | Sign in with email and password |\n" +
			"| GET, POST | `/signup` | Create an account (emails a confirmation link) |\n" +
			"| GET, POST | `/verify-email` | Confirm the email (the link opens a page; the button posts) |\n" +
			"| GET, POST | `/forgot-password`, `/reset-password` | Password reset by email |\n"
	}
	if oauth {
		routes += "| GET | `/auth/{provider}/login`, `/auth/{provider}/callback` | Sign in with " + providerList + " |\n"
	}
	if o.HasMethod(options.Apple) {
		routes += "| POST | `/auth/apple/callback` | Apple's return (a cross-site POST, exempt from the CSRF check) |\n"
	}
	routes += "| GET | `/account` | The signed-in user's account |\n" +
		"| POST | `/logout`, `/logout-everywhere` | Sign out here, or on every device |\n"

	readme := []edit{
		{"README.md", "- A dev-only `/seed` route and a script that runs it with a valid CSRF token\n",
			"- A dev-only `/seed` route and a script that runs it with a valid CSRF token\n" +
				"- Sign-in with " + strings.Join(what, "; or ") + ", on server-side sessions\n"},
		{"README.md", "  config/           environment loading and validation\n",
			"  auth/             sessions, the signed-in user, " + map[bool]string{true: "password hashing, ", false: ""}[password] +
				map[bool]string{true: "sign-in providers, ", false: ""}[oauth] + "per-account limits\n  config/           environment loading and validation\n"},
		{"README.md", "| POST | `/hello` | Example app: greeting fragment |\n", routes + "| POST | `/hello` | Example app: greeting fragment |\n"},
	}
	if password {
		readme = append(readme,
			edit{"README.md", "make seed     # POST /seed with a valid CSRF token; loads seedData.json\n```\n",
				"make seed     # POST /seed with a valid CSRF token; loads seedData.json\n```\n\n" +
					"It also creates `dev@example.com`, confirmed, with the password in `SeedUserPassword` (`.env`).\n" +
					"In development, emails (with their links) are written to the server log; set `SMTPHost` to send them.\n"},
			edit{"README.md", "  middleware/       security headers, rate limit, compression, body limit, admin key\n",
				"  mail/             emails: SMTP in production, the log in development\n  middleware/       security headers, rate limit, compression, body limit, admin key\n"})
	}
	if oauth {
		readme = append(readme, edit{"README.md", "### The JavaScript libraries\n",
			"### Sign-in providers\n\n" +
				"Each provider needs an app registered with it, with `<BaseURL>/auth/<provider>/callback` as its redirect\n" +
				"URL, and its keys in `.env` (see `.env.example`). In development, a provider without keys is left off the\n" +
				"sign-in page; production refuses to start without them." +
				map[bool]string{true: " Apple only allows `https` redirect URLs, so trying\nApple locally needs an `https` tunnel to the dev server.", false: ""}[o.HasMethod(options.Apple)] +
				"\n\n### The JavaScript libraries\n"})
	}

	// AGENTS.md
	fields, _ := authSettings(o)
	var names []string
	for _, f := range fields {
		names = append(names, "`"+f.name+"`")
	}
	authNote := "- Auth: sign-in with " + with + ". Sessions are server-side in the `sessions` table (`alexedwards/scs`; cookie `__Host-session` in production). " +
		"`auth.CurrentUser(ctx)` is the signed-in user; wrap routes that need one in `auth.RequireUser`, and authorise every object access by fetching with `(id, owner_id)`. " +
		"Every sign-in goes through `factory.Auth.signIn`, which renews the session token and replaces the CSRF token (`csrf.Rotate`). Raising `User.SessionVersion` ends all of a user's sessions (\"Sign out everywhere\"" +
		map[bool]string{true: ", password reset", false: ""}[password] + "). "
	if password {
		authNote += "Passwords: argon2id in `internal/auth/password.go`, flows in `internal/service/auth.go`, handlers in `internal/factory/password.go`. " +
			"Emailed links are single-use tokens stored as SHA-256 (`model.AuthToken`), built from `BaseURL`, and open a page whose button POSTs. "
	}
	if oauth {
		authNote += "Providers: `internal/auth/oauth.go` (state, nonce and PKCE in a short-lived `__Host-oauth` cookie) and `providers.go`; `service.IdentityService` matches users by `(provider, subject)` in `identities`, and links or creates by email only when the provider has verified it (and, to link, the user's email is confirmed too). " +
			"Only the chosen providers' code is in the project (the rest was left out when it was generated); there is no plugin mechanism. "
		if o.HasMethod(options.Apple) {
			authNote += "Apple's callback is a cross-site POST, so `POST /auth/apple/callback` alone skips the CSRF and cross-origin checks (`buildHandler`); its state and nonce protect it. "
		}
	}
	authNote += "Every model is listed in `model.All()` (AutoMigrate in the seed route and the tests)."

	agents := []edit{
		{"AGENTS.md", "`SeedAdminKey`); see `.env.example`.", "`SeedAdminKey`, " + strings.Join(names, ", ") + "); see `.env.example`."},
		{"AGENTS.md",
			"- Middleware order (outermost first): request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF.",
			"- Middleware order (outermost first): request ID, security headers, compression, rate limit, cross-origin protection, body limit, CSRF, then the session and the signed-in user (`auth.Middleware`, skipped for `/static/`).\n" + authNote},
	}

	// SECURITY.md
	tables := "`users`, `identities`, "
	if password {
		tables += "`auth_tokens`, "
	}
	checklist := "- [ ] `BaseURL` is the public `https://` address"
	if password {
		checklist += ", and SMTP is configured (`SMTPHost`, `MailFrom`)"
	}
	if oauth {
		checklist += "; each sign-in provider has its keys, and its app at the provider\n      allows only `<BaseURL>/auth/<provider>/callback` as a redirect URL"
	}
	checklist += ".\n      Production refuses to start without them."
	if password {
		checklist += " `SeedUserPassword` is not set."
	}
	checklist += "\n- [ ] The " + tables + "and `sessions` tables exist (reviewed migrations;\n      `AutoMigrate` only runs in the development seed route).\n"

	section := "### Authentication and sessions\n\n" +
		"This project signs users in with " + with + ". What it does, so you keep doing it as you build on it:\n\n" +
		"- Sessions are stored server-side and can be revoked: `__Host-session` (HttpOnly, Secure,\n" +
		"  SameSite=Lax) in production, 7 days at most and 24 hours idle. Every sign-in issues a new session token\n" +
		"  and a new CSRF token. \"Sign out everywhere\"" + map[bool]string{true: " and a password reset end", false: " ends"}[password] + " every session of the user.\n" +
		"- `?next=` after sign-in only accepts paths on this site (`auth.SafeNext`).\n"
	if password {
		section += "- Passwords are hashed with argon2id (`internal/auth/password.go`), 15 to 256 characters, with no\n" +
			"  rules about kinds of characters (NIST SP 800-63B-4). Unknown emails cost the same time as wrong\n" +
			"  passwords (`VerifyDummy`), and sign-in, sign-up and reset answer the same whether or not an\n" +
			"  account exists.\n" +
			"- Emailed links carry single-use, expiring tokens stored only as SHA-256. They are built from\n" +
			"  `BaseURL`, never from the Host header (which would allow reset-link poisoning), open a page with\n" +
			"  `Referrer-Policy: no-referrer`, and change nothing until its button posts.\n" +
			"- Sign-in, sign-up and reset emails are rate-limited per email address as well as per IP.\n"
	}
	if oauth {
		section += "- Provider sign-in (" + providerList + ") checks a random `state` (tied to a short-lived `__Host-oauth`\n" +
			"  cookie, cleared at the callback), uses PKCE where the provider supports it, and checks the ID token's\n" +
			"  signature, audience, expiry, issuer and `nonce`. It starts from a plain link, so the redirect to the\n" +
			"  provider needs no `form-action` exception in the CSP.\n" +
			"- Users are matched by the provider's `(provider, subject)`, never by email alone. An email links to an\n" +
			"  existing account only when the provider has verified it and the account's email is confirmed too;\n" +
			"  a new account needs a verified email. Otherwise nobody could take over, or squat, an account by\n" +
			"  signing in elsewhere with someone else's address.\n" +
			"- Provider access tokens are used once, to identify the user, and never stored.\n"
		if o.HasMethod(options.Apple) {
			section += "- Apple comes back with a cross-site POST, so `POST /auth/apple/callback` alone is exempt from the\n" +
				"  CSRF token and cross-origin checks, and its flow cookie is `SameSite=None`. Keep that exemption to\n" +
				"  that one route. The `.p8` key signs a fresh, five-minute client secret per sign-in; keep the file\n" +
				"  out of the repository and the image.\n"
		}
	}
	section += "\nWhen you add more:\n"

	limitations := "- No second factor (TOTP or passkeys) yet.\n" +
		"- Authorisation is yours: `auth.RequireUser` only checks that someone is signed in.\n" +
		"- The CSRF token is a double-submit cookie, replaced at sign-in and sign-out but not bound to the\n" +
		"  session, and uses a plain cookie name.\n"
	if password {
		limitations += "- Password sign-in doesn't work without JavaScript: the forms rely on htmx sending the CSRF header.\n"
	}

	security := []edit{
		{"SECURITY.md",
			"It ships **no authentication, no\nauthorisation and no user data**. Those are yours to add, and most of the guidance below is about\nadding them without undoing the defaults.",
			"This project also signs users in\n(with " + with + "). Authorisation (who may see what) is yours to add, and most of the\nguidance below is about adding it without undoing the defaults."},
		{"SECURITY.md",
			"- [ ] Config comes from the platform's environment or secret store, not from a `.env` file on the\n      server. `.env` stays git-ignored.\n",
			"- [ ] Config comes from the platform's environment or secret store, not from a `.env` file on the\n      server. `.env` stays git-ignored.\n" + checklist},
		{"SECURITY.md", "### Authentication and sessions (when you add users)\n\nThe template has none of this yet. When you add it:\n", section},
		{"SECURITY.md",
			"- No authentication, authorisation or sessions. Every route is public.\n- The CSRF token is not tied to a session (there are no sessions) and uses a plain cookie name.\n",
			limitations},
	}
	if password {
		security = append(security, edit{"SECURITY.md",
			"- Never log passwords, session IDs, CSRF tokens, `X-Admin-Key`, full request bodies, or personal\n  data. Be careful logging whole headers or form values.\n",
			"- Never log passwords, session IDs, CSRF tokens, `X-Admin-Key`, full request bodies, or personal\n  data. Be careful logging whole headers or form values.\n" +
				"- In development, without `SMTPHost`, emails and their links go to the log (`mail.LogMailer`). Those\n" +
				"  links work like passwords; production refuses to start without SMTP, so this never happens there.\n"})
	}

	return append(append(readme, agents...), security...)
}
