// Package cli runs create-gotth-plus-app: it reads flags, asks for anything missing, shows a
// summary, and generates the project.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"golang.org/x/term"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
	"github.com/owenwexler/create-gotth-plus-app/internal/post"
	"github.com/owenwexler/create-gotth-plus-app/internal/prereq"
	"github.com/owenwexler/create-gotth-plus-app/internal/scaffold"
)

// Exit codes.
const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitCancelled = 130 // same as a shell's Ctrl+C
)

// IO is where the CLI reads and writes. Tests pass buffers; main passes the process's streams.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Getwd returns the working directory the project name is relative to.
	Getwd func() (string, error)
	// Tools finds the prerequisite tools; nil means the real PATH.
	Tools *prereq.Runner
	// Post runs the steps after the files are written; nil means the real ones.
	Post PostSteps
}

// PostSteps runs the after-creation steps (see package post).
type PostSteps interface {
	RunAll(ctx context.Context, dir string, o options.Options) []post.Outcome
}

// flags are the parsed command-line flags, plus which ones were given (so prompts can skip them).
type flags struct {
	yes, force, accessible, version, skipChecks bool

	module       string
	port         int
	seedAdminKey bool
	db           string
	sqliteDriver string

	js, axe, git, setup, e2e, docker, ci bool
	liveReload                           string

	set  map[string]bool
	name string // the positional project name, if any
}

func newFlagSet(f *flags, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("create-gotth-plus-app", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaults := options.Defaults()

	fs.BoolVar(&f.yes, "yes", false, "don't ask anything: use flags, then defaults")
	fs.BoolVar(&f.yes, "y", false, "short for --yes")
	fs.BoolVar(&f.force, "force", false, "write into a directory that is not empty")
	fs.BoolVar(&f.accessible, "accessible", os.Getenv("ACCESSIBLE") != "", "plain prompts for screen readers (also set by ACCESSIBLE=1)")
	fs.BoolVar(&f.version, "version", false, "print the version and exit")
	fs.BoolVar(&f.skipChecks, "skip-checks", false, "don't check that Go, templ, tailwindcss and the other tools are installed")

	fs.StringVar(&f.module, "module", "", "Go module path (default github.com/<git config github.user>/<name>, or <name>)")
	fs.IntVar(&f.port, "port", defaults.Port, "development port")
	fs.BoolVar(&f.seedAdminKey, "seed-admin-key", defaults.SeedAdminKey, "write a random SeedAdminKey into .env (--seed-admin-key=false to skip)")
	fs.StringVar(&f.db, "db", string(defaults.Database), "database: sqlite, postgres or mysql")
	fs.StringVar(&f.sqliteDriver, "sqlite-driver", string(defaults.SQLiteDriver), "SQLite driver: purego (no C compiler) or cgo (mattn/go-sqlite3)")
	fs.BoolVar(&f.e2e, "e2e", defaults.E2E, "include the Playwright end-to-end (browser) tests")
	fs.StringVar(&f.liveReload, "live-reload", string(defaults.LiveReload), "what make dev runs: templ (templ's proxy) or air")
	fs.BoolVar(&f.docker, "docker", defaults.Docker, "add a production Dockerfile (distroless, non-root)")
	fs.BoolVar(&f.ci, "ci", defaults.CI, "add a GitHub Actions workflow (verify, generated-code check, vet, test, govulncheck)")
	fs.BoolVar(&f.js, "js", defaults.CommitJS, "download htmx, Alpine (CSP) and hx-optimistic, record their versions and hashes, and commit them")
	fs.BoolVar(&f.axe, "axe", defaults.Axe, "download axe-core for the accessibility test")
	fs.BoolVar(&f.git, "git", defaults.Git, "git init and make a first commit")
	fs.BoolVar(&f.setup, "setup", defaults.Setup, "run make setup (Go modules, JS libraries, test browser, first build)")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: create-gotth-plus-app [flags] [project-name]\n\n")
		fmt.Fprintf(stderr, "Creates a new GOTTH+ app (Go, Templ, Tailwind, htmx, hx-optimistic, Alpine CSP).\n")
		fmt.Fprintf(stderr, "Asks for anything not given as a flag, unless --yes is set. Use \".\" as the name for the current directory.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags allows flags before and after the project name (Go's flag package stops at the first
// non-flag argument otherwise).
func parseFlags(args []string, stderr io.Writer) (*flags, error) {
	f := &flags{set: map[string]bool{}}
	fs := newFlagSet(f, stderr)

	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("expected one project name, got %d: %s", len(positional), strings.Join(positional, " "))
	}
	if len(positional) == 1 {
		f.name = positional[0]
	}

	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	if f.set["y"] {
		f.set["yes"] = true
	}
	return f, nil
}

// Run runs the CLI and returns the process exit code.
func Run(ctx context.Context, args []string, stdio IO) int {
	f, err := parseFlags(args, stdio.Err)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stdio.Err, "error:", err)
		return exitUsage
	}

	tmpl := scaffold.Template()
	if f.version {
		fmt.Fprintf(stdio.Out, "create-gotth-plus-app %s (gotth-plus %s)\n", cliVersion(), scaffold.TemplateVersion(tmpl))
		return exitOK
	}

	err = run(ctx, f, tmpl, stdio)
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, huh.ErrUserAborted), errors.Is(err, errCancelled):
		fmt.Fprintln(stdio.Err, "cancelled, nothing was written")
		return exitCancelled
	default:
		fmt.Fprintln(stdio.Err, "error:", err)
		return exitError
	}
}

var errCancelled = errors.New("cancelled")

func run(ctx context.Context, f *flags, tmpl fs.FS, stdio IO) error {
	wd, err := stdio.Getwd()
	if err != nil {
		return err
	}
	dirFor := func(name string) string { return filepath.Join(wd, name) }

	interactive := !f.yes
	if interactive && !isTerminal(stdio.In) {
		return errors.New("no terminal to ask questions on; pass --yes to use defaults (and flags to change them)")
	}

	opts := options.Defaults()
	opts.Name = f.name
	opts.ModulePath = f.module
	opts.Port = f.port
	opts.SeedAdminKey = f.seedAdminKey
	if opts.Database, err = options.ParseDatabase(f.db); err != nil {
		return err
	}
	if opts.SQLiteDriver, err = options.ParseSQLiteDriver(f.sqliteDriver); err != nil {
		return err
	}
	opts.CommitJS, opts.Axe, opts.Git, opts.Setup, opts.E2E = f.js, f.axe, f.git, f.setup, f.e2e
	opts.Docker, opts.CI = f.docker, f.ci
	if opts.LiveReload, err = options.ParseLiveReload(f.liveReload); err != nil {
		return err
	}
	if !opts.E2E {
		if f.set["axe"] && f.axe {
			return errors.New("--axe only applies with the E2E tests (--e2e)")
		}
		opts.Axe = false // axe-core is only used by the E2E tests
	}
	if f.set["sqlite-driver"] && f.set["db"] && opts.Database != options.SQLite {
		return errors.New("--sqlite-driver only applies to --db=sqlite")
	}

	// check a name given as an argument before asking anything else
	if opts.Name != "" {
		if err := checkName(opts.Name, dirFor(opts.Name), f.force); err != nil {
			return err
		}
	} else if !interactive {
		return errors.New("a project name is required with --yes: create-gotth-plus-app --yes my-app")
	}

	p := prompter{in: stdio.In, out: stdio.Out, accessible: f.accessible}
	if interactive {
		if err := p.askName(ctx, &opts, func(name string) error { return checkName(name, dirFor(name), f.force) }); err != nil {
			return err
		}
	}
	if opts.ModulePath == "" {
		opts.ModulePath = options.DefaultModulePath(dirFor(opts.Name), githubUser(ctx))
	}
	if interactive {
		if err := p.askRest(ctx, &opts, f.set); err != nil {
			return err
		}
		if !opts.E2E {
			opts.Axe = false
		}
	}

	if err := opts.Validate(); err != nil {
		return err
	}

	dir := dirFor(opts.Name)
	// git add -A would commit whatever was already in a directory written into with --force
	if opts.Git && !isEmptyDir(dir) {
		opts.Git = false
		fmt.Fprintf(stdio.Out, "\nNot running git init: %s already has files in it.\n", dir)
	}
	printSummary(stdio.Out, opts, dir, scaffold.TemplateVersion(tmpl))
	if !f.skipChecks {
		if err := checkTools(ctx, stdio, tmpl, opts); err != nil {
			return err
		}
	}
	if interactive {
		ok, err := p.confirm(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return errCancelled
		}
	}

	project, err := scaffold.Build(tmpl, opts, dir)
	if err != nil {
		return fmt.Errorf("generate project: %w", err)
	}
	if err := project.Write(dir); err != nil {
		return fmt.Errorf("write project: %w", err)
	}

	steps := stdio.Post
	if steps == nil {
		steps = post.System(stdio.Out)
	}
	allOK := post.Report(stdio.Out, steps.RunAll(ctx, dir, opts))

	printNextSteps(stdio.Out, opts, dir, allOK)
	if !allOK {
		return errStepsFailed
	}
	return nil
}

var errStepsFailed = errors.New("the project was created, but some after-creation steps failed (see above)")

// isEmptyDir reports whether dir is missing or has no entries.
func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}

// checkTools reports missing or unsuitable tools. They are warnings: only reading the template's
// versions can fail.
func checkTools(ctx context.Context, stdio IO, tmpl fs.FS, opts options.Options) error {
	goVersion, templVersion, err := scaffold.Requirements(tmpl)
	if err != nil {
		return fmt.Errorf("read template requirements: %w", err)
	}
	runner := prereq.System()
	if stdio.Tools != nil {
		runner = *stdio.Tools
	}
	tools := prereq.Tools(prereq.Requirements{Go: goVersion, Templ: templVersion}, prereq.Needs{CCompiler: opts.NeedsCCompiler(), Air: opts.LiveReload == options.Air})
	prereq.Report(stdio.Out, runner.Check(ctx, tools))
	fmt.Fprintln(stdio.Out)
	return nil
}

func checkName(name, dir string, force bool) error {
	if err := options.ValidateName(name); err != nil {
		return err
	}
	return scaffold.CheckDest(dir, force)
}

func printSummary(w io.Writer, o options.Options, dir, version string) {
	yesNo := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Fprintf(w, "\nAbout to create a GOTTH+ app (gotth-plus %s):\n\n", version)
	rows := [][2]string{
		{"Directory", dir},
		{"Module path", o.ModulePath},
		{"Database", o.DatabaseLabel()},
		{"Port", strconv.Itoa(o.Port)},
		{"E2E tests", yesNo(o.E2E)},
		{"Live reload", string(o.LiveReload)},
		{"Dockerfile", yesNo(o.Docker)},
		{"GitHub Actions CI", yesNo(o.CI)},
		{"Random SeedAdminKey", yesNo(o.SeedAdminKey)},
		{"Commit JS libraries", yesNo(o.CommitJS)},
		{"Download axe-core", yesNo(o.Axe)},
		{"git init + commit", yesNo(o.Git)},
		{"Run make setup", yesNo(o.Setup)},
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %-20s %s\n", r[0], r[1])
	}
	fmt.Fprintln(w)
}

// projectName is the name the generator used: the directory's name when the project is ".".
func projectName(o options.Options, dir string) string {
	if o.Name == "." {
		return filepath.Base(dir)
	}
	return o.Name
}

func printNextSteps(w io.Writer, o options.Options, dir string, stepsOK bool) {
	fmt.Fprintf(w, "\nNext steps:\n\n")
	if o.Name != "." {
		fmt.Fprintf(w, "  cd %s\n", o.Name)
	}
	if cmd := scaffold.CreateDatabaseCommand(o.Database, options.DatabaseName(projectName(o, dir))); cmd != "" {
		fmt.Fprintf(w, "  %s    # the development database; check DatabaseURL in .env\n", cmd)
	}
	if !o.Setup || !stepsOK {
		fmt.Fprintf(w, "  make setup    # Go modules, the JS libraries, the test browser, a first build\n")
	}
	fmt.Fprintf(w, "  make dev      # live reload at http://localhost:7331\n\n")
	fmt.Fprintf(w, "Read AGENTS.md and SECURITY.md before you build on it.\n")
}

// githubUser is `git config github.user`, or "" when git or the setting is missing.
func githubUser(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "config", "--get", "github.user").Output()
	if err != nil {
		return ""
	}
	user := strings.TrimSpace(string(out))
	if options.ValidateModulePath("github.com/"+user+"/x") != nil {
		return ""
	}
	return user
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// cliVersion is the module version when installed with `go install ...@version`.
func cliVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}
