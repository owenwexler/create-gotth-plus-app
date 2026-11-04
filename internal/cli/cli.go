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
}

// flags are the parsed command-line flags, plus which ones were given (so prompts can skip them).
type flags struct {
	yes, force, accessible, version bool

	module       string
	port         int
	seedAdminKey bool

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

	fs.StringVar(&f.module, "module", "", "Go module path (default github.com/<git config github.user>/<name>, or <name>)")
	fs.IntVar(&f.port, "port", defaults.Port, "development port")
	fs.BoolVar(&f.seedAdminKey, "seed-admin-key", defaults.SeedAdminKey, "write a random SeedAdminKey into .env (--seed-admin-key=false to skip)")

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
	}

	if err := opts.Validate(); err != nil {
		return err
	}

	dir := dirFor(opts.Name)
	printSummary(stdio.Out, opts, dir, scaffold.TemplateVersion(tmpl))
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

	printNextSteps(stdio.Out, opts)
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
		{"Port", strconv.Itoa(o.Port)},
		{"Random SeedAdminKey", yesNo(o.SeedAdminKey)},
	}
	for _, r := range rows {
		fmt.Fprintf(w, "  %-20s %s\n", r[0], r[1])
	}
	fmt.Fprintln(w)
}

func printNextSteps(w io.Writer, o options.Options) {
	fmt.Fprintf(w, "Done. Next steps:\n\n")
	if o.Name != "." {
		fmt.Fprintf(w, "  cd %s\n", o.Name)
	}
	fmt.Fprintf(w, "  make setup    # Go modules, the JS libraries, the test browser, a first build\n")
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
