package cli

import (
	"context"
	"errors"
	"io"
	"strconv"

	"charm.land/huh/v2"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// prompter asks the questions. Each question is skipped when its flag was given.
type prompter struct {
	in         io.Reader
	out        io.Writer
	accessible bool
}

func (p prompter) run(ctx context.Context, groups ...*huh.Group) error {
	return huh.NewForm(groups...).
		WithInput(p.in).
		WithOutput(p.out).
		WithAccessible(p.accessible).
		RunWithContext(ctx)
}

// askName asks for the project name when none was given. check validates it, including whether the
// directory can be used, so a bad answer is reported right away.
func (p prompter) askName(ctx context.Context, o *options.Options, check func(string) error) error {
	if o.Name != "" {
		return nil
	}
	return p.run(ctx, huh.NewGroup(
		huh.NewInput().
			Title("Project name").
			Description(`The directory to create. Use "." for the current directory.`).
			Placeholder("my-app").
			Value(&o.Name).
			Validate(check),
	))
}

// askRest asks everything after the name. The module path is already filled with its default.
func (p prompter) askRest(ctx context.Context, o *options.Options, given map[string]bool) error {
	var fields []huh.Field

	if !given["module"] {
		fields = append(fields, huh.NewInput().
			Title("Go module path").
			Description("Written to go.mod and every import, e.g. github.com/you/my-app.").
			Value(&o.ModulePath).
			Validate(options.ValidateModulePath))
	}

	port := strconv.Itoa(o.Port)
	if !given["port"] {
		fields = append(fields, huh.NewInput().
			Title("Development port").
			Description("The server's port in .env. make dev serves a live-reload proxy on 7331 in front of it.").
			Value(&port).
			Validate(options.ValidatePort))
	}

	if !given["seed-admin-key"] {
		fields = append(fields, huh.NewConfirm().
			Title("Generate a random SeedAdminKey?").
			Description("POST /seed (development only) then needs this key. Stored in .env, which git ignores.").
			Value(&o.SeedAdminKey))
	}

	groups := []*huh.Group{}
	if len(fields) > 0 {
		groups = append(groups, huh.NewGroup(fields...))
	}

	// auth comes before the database: some sign-in providers decide the database (Supabase: Postgres)
	if !given["auth"] {
		groups = append(groups, huh.NewGroup(huh.NewSelect[options.Auth]().
			Title("Sign-in").
			Description("Built-in: server-side sessions in the app, with a password and/or Google, GitHub or Apple.").
			Options(
				huh.NewOption("None", options.NoAuth),
				huh.NewOption("Built-in", options.BuiltinAuth),
				huh.NewOption("OIDC (WorkOS/Logto/Kinde)", options.OIDCAuth),
				huh.NewOption("Supabase Auth (uses Supabase's Postgres)", options.SupabaseAuth),
			).
			Value(&o.Auth)))
	}
	if !given["auth-methods"] {
		methodOptions := make([]huh.Option[options.AuthMethod], len(options.AuthMethodList))
		for i, m := range options.AuthMethodList {
			methodOptions[i] = huh.NewOption(m.Label(), m).Selected(m == options.Password)
		}
		groups = append(groups, huh.NewGroup(huh.NewMultiSelect[options.AuthMethod]().
			Title("Sign-in methods").
			Description("Pick one or more. Google, GitHub and Apple each need an app registered with them; keys go in .env.").
			Options(methodOptions...).
			Validate(func(ms []options.AuthMethod) error {
				if len(ms) == 0 {
					return errors.New("choose at least one sign-in method")
				}
				return nil
			}).
			Value(&o.AuthMethods)).
			WithHideFunc(func() bool { return o.Auth != options.BuiltinAuth }))
	}
	if !given["oidc-preset"] {
		presetOptions := make([]huh.Option[options.OIDCPreset], len(options.OIDCPresets))
		for i, p := range options.OIDCPresets {
			label := p.Label
			if p.Preset == "other" {
				label = "Other (any OpenID Connect provider)"
			}
			presetOptions[i] = huh.NewOption(label, p.Preset)
		}
		groups = append(groups, huh.NewGroup(huh.NewSelect[options.OIDCPreset]().
			Title("OIDC provider").
			Description("Users sign in on the provider's page. The app reads its endpoints from discovery; the choice fills in .env.example.").
			Options(presetOptions...).
			Value(&o.OIDCPreset)).
			WithHideFunc(func() bool { return o.Auth != options.OIDCAuth }))
	}
	if !given["db"] {
		dbOptions := make([]huh.Option[options.Database], len(options.Databases))
		for i, d := range options.Databases {
			dbOptions[i] = huh.NewOption(d.Label(), d)
		}
		groups = append(groups, huh.NewGroup(huh.NewSelect[options.Database]().
			Title("Database").
			Description("SQLite is a file next to the app. Postgres and MySQL need a server, local or in the cloud, for the app and its tests.").
			Options(dbOptions...).
			Value(&o.Database)).
			WithHideFunc(func() bool { return o.Auth == options.SupabaseAuth })) // Supabase is Postgres
	}
	if !given["sqlite-driver"] {
		groups = append(groups, huh.NewGroup(huh.NewSelect[options.SQLiteDriver]().
			Title("SQLite driver").
			Options(
				huh.NewOption("Pure Go (glebarez/sqlite): no C compiler, CGO_ENABLED=0 builds", options.PureGo),
				huh.NewOption("CGO (mattn/go-sqlite3): the C SQLite library, needs a C compiler", options.CGO),
			).
			Value(&o.SQLiteDriver)).
			WithHideFunc(func() bool { return o.Database != options.SQLite || o.Auth == options.SupabaseAuth }))
	}

	var project []huh.Field
	if !given["e2e"] {
		project = append(project, huh.NewConfirm().
			Title("Include the Playwright end-to-end tests?").
			Description("Browser tests, including the checks that enforce the strict CSP (no third-party requests), focus rings and an axe scan. They need Chromium.").
			Value(&o.E2E))
	}
	if !given["live-reload"] {
		project = append(project, huh.NewSelect[options.LiveReload]().
			Title("Live reload for make dev").
			Options(
				huh.NewOption("templ's proxy (templ generate --watch): nothing else to install", options.TemplProxy),
				huh.NewOption("air (github.com/air-verse/air), with its browser-reloading proxy", options.Air),
			).
			Value(&o.LiveReload))
	}
	if !given["docker"] {
		project = append(project, huh.NewConfirm().
			Title("Add a production Dockerfile?").
			Description("Multi-stage build into a distroless image that runs as a non-root user. Fly.io, Railway and Render can build it as it is.").
			Value(&o.Docker))
	}
	if !given["ci"] {
		project = append(project, huh.NewConfirm().
			Title("Add GitHub Actions CI?").
			Description("go mod verify, a check that generated code and formatting are committed, vet, tests (with a database service when needed), govulncheck, and a Docker build.").
			Value(&o.CI))
	}
	if len(project) > 0 {
		groups = append(groups, huh.NewGroup(project...))
	}

	var after []huh.Field
	for _, q := range []struct {
		flag, title, desc string
		value             *bool
	}{
		{"js", "Download and commit the JS libraries?", "htmx, Alpine (CSP build) and hx-optimistic, with their versions and SHA-384 hashes recorded in README.md, so production serves reviewed copies.", &o.CommitJS},
		{"git", "Initialize git and make a first commit?", "Skipped when the project is inside an existing repository.", &o.Git},
		{"setup", "Run make setup now?", "Downloads the Go modules and the JS libraries (and Chromium, a few hundred MB, for the E2E tests), and builds once.", &o.Setup},
	} {
		if !given[q.flag] {
			after = append(after, huh.NewConfirm().Title(q.title).Description(q.desc).Value(q.value))
		}
	}
	if len(after) > 0 {
		groups = append(groups, huh.NewGroup(after...).Title("After creating the project"))
	}
	// axe-core only matters with the E2E tests, so it is its own group that hides without them
	if !given["axe"] {
		groups = append(groups, huh.NewGroup(huh.NewConfirm().
			Title("Download axe-core for the accessibility test?").
			Description("From the npm registry, checked against its sha512 hash. Without it, the axe scan in the E2E tests is skipped.").
			Value(&o.Axe)).
			WithHideFunc(func() bool { return !o.E2E }))
	}

	if len(groups) == 0 {
		return nil
	}
	if err := p.run(ctx, groups...); err != nil {
		return err
	}

	n, err := strconv.Atoi(port)
	if err != nil {
		return err // Validate already rejected anything that is not a number
	}
	o.Port = n
	return nil
}

func (p prompter) confirm(ctx context.Context) (bool, error) {
	ok := true
	err := p.run(ctx, huh.NewGroup(
		huh.NewConfirm().
			Title("Create the project?").
			Affirmative("Create").
			Negative("Cancel").
			Value(&ok),
	))
	return ok, err
}
