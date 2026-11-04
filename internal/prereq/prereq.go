// Package prereq checks that the tools a generated project needs are installed, and explains how to
// install the ones that are missing or too old. Problems are warnings: the project can always be
// written, but `make setup` and the other make targets need these tools.
package prereq

import (
	"context"
	"errors"
	"fmt"
	"go/version"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Runner finds and runs commands. Tests replace both functions.
type Runner struct {
	LookPath func(file string) (string, error)
	Output   func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// System is the Runner that uses the real PATH.
func System() Runner {
	return Runner{
		LookPath: exec.LookPath,
		Output: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
	}
}

// Tool is one prerequisite.
type Tool struct {
	Name    string // shown to the user
	Why     string // what the project uses it for
	Install string // how to get it
	// Check returns the version found (or "" when it only checks presence), and an error when the
	// tool is missing or unsuitable.
	Check func(ctx context.Context, r Runner) (string, error)
}

// Result is the outcome of checking one tool.
type Result struct {
	Tool    Tool
	Version string
	Err     error
}

// Needs are the tools that only some projects need.
type Needs struct {
	// CCompiler is needed by the CGO SQLite driver (mattn/go-sqlite3).
	CCompiler bool
}

// Requirements are versions read from the template's go.mod.
type Requirements struct {
	Go    string // minimum Go version, e.g. "1.27.3"
	Templ string // templ module version, e.g. "v0.3.1070"
}

var errNotFound = errors.New("not found on PATH")

// Tools lists the prerequisites for a project.
func Tools(req Requirements, needs Needs) []Tool {
	tools := []Tool{
		{
			Name:    "Go",
			Why:     "builds and runs the app",
			Install: "https://go.dev/dl/",
			Check:   goCheck("go" + req.Go),
		},
		{
			Name:    "templ",
			Why:     "turns .templ files into Go (make generate)",
			Install: "go install github.com/a-h/templ/cmd/templ@" + req.Templ,
			Check:   templCheck(req.Templ),
		},
		{
			Name:    "Tailwind CSS v4 standalone CLI (tailwindcss)",
			Why:     "builds static/output.css (make css)",
			Install: "https://github.com/tailwindlabs/tailwindcss/releases/latest, saved on your PATH as tailwindcss",
			Check:   tailwindCheck,
		},
		{
			Name:    "make",
			Why:     "runs the project's tasks (make help)",
			Install: "macOS: xcode-select --install; Debian/Ubuntu: sudo apt install make",
			Check:   onPath("make"),
		},
		{
			Name:    "bash",
			Why:     "runs download-js.sh and seed.sh",
			Install: "preinstalled on macOS and Linux; on Windows, use WSL",
			Check:   onPath("bash"),
		},
		{
			Name:    "git",
			Why:     "download-js.sh looks up library versions from git tags",
			Install: "https://git-scm.com/downloads",
			Check:   onPath("git"),
		},
		{
			Name:    "curl",
			Why:     "download-js.sh downloads htmx, Alpine and hx-optimistic",
			Install: "https://curl.se/download.html, or your package manager",
			Check:   onPath("curl"),
		},
		{
			Name:    "openssl",
			Why:     "download-js.sh prints each library's sha384 hash",
			Install: "your package manager (brew install openssl, sudo apt install openssl)",
			Check:   onPath("openssl"),
		},
	}
	if needs.CCompiler {
		tools = append(tools, Tool{
			Name:    "C compiler",
			Why:     "the SQLite driver uses CGO",
			Install: "macOS: xcode-select --install; Debian/Ubuntu: sudo apt install build-essential",
			Check:   cCompilerCheck,
		})
	}
	return tools
}

// timeout bounds each version command, so a hanging tool can't stall the CLI.
const timeout = 5 * time.Second

func (r Runner) output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := r.Output(ctx, name, args...)
	return strings.TrimSpace(string(out)), err
}

func onPath(command string) func(context.Context, Runner) (string, error) {
	return func(_ context.Context, r Runner) (string, error) {
		if _, err := r.LookPath(command); err != nil {
			return "", errNotFound
		}
		return "", nil
	}
}

func goCheck(min string) func(context.Context, Runner) (string, error) {
	return func(ctx context.Context, r Runner) (string, error) {
		if _, err := r.LookPath("go"); err != nil {
			return "", errNotFound
		}
		v, err := r.output(ctx, "go", "env", "GOVERSION")
		if err != nil {
			return "", fmt.Errorf("go env GOVERSION failed: %w", err)
		}
		// development builds ("devel go1.28-...") have no comparable version; trust them
		if version.IsValid(v) && version.Compare(v, min) < 0 {
			return v, fmt.Errorf("the project needs %s or newer (security fixes)", strings.TrimPrefix(min, "go"))
		}
		return v, nil
	}
}

func templCheck(want string) func(context.Context, Runner) (string, error) {
	return func(ctx context.Context, r Runner) (string, error) {
		if _, err := r.LookPath("templ"); err != nil {
			return "", errNotFound
		}
		v, err := r.output(ctx, "templ", "version")
		if err != nil {
			return "", fmt.Errorf("templ version failed: %w", err)
		}
		// templ prints e.g. "v0.3.1070"; older releases may add more text after it
		v = strings.Fields(v + " ")[0]
		if semver.IsValid(v) && semver.Compare(v, want) != 0 {
			return v, fmt.Errorf("the project uses templ %s; a different CLI version generates different code", want)
		}
		return v, nil
	}
}

var tailwindVersion = regexp.MustCompile(`v(\d+\.\d+\.\d+)`)

func tailwindCheck(ctx context.Context, r Runner) (string, error) {
	if _, err := r.LookPath("tailwindcss"); err != nil {
		return "", errNotFound
	}
	// the standalone CLI prints "≈ tailwindcss v4.x.y" at the top of its help
	out, _ := r.output(ctx, "tailwindcss", "--help")
	m := tailwindVersion.FindStringSubmatch(out)
	if m == nil {
		return "", errors.New("could not read its version; the project needs v4")
	}
	v := "v" + m[1]
	if semver.Major(v) != "v4" {
		return v, errors.New("the project needs Tailwind CSS v4")
	}
	return v, nil
}

func cCompilerCheck(ctx context.Context, r Runner) (string, error) {
	if cgo, err := r.output(ctx, "go", "env", "CGO_ENABLED"); err == nil && cgo == "0" {
		return "", errors.New("CGO is turned off (go env CGO_ENABLED is 0)")
	}
	cc, err := r.output(ctx, "go", "env", "CC")
	if err != nil || cc == "" {
		cc = "cc"
	}
	// CC may carry flags ("gcc -m64"); the first word is the program
	if _, err := r.LookPath(strings.Fields(cc)[0]); err != nil {
		return "", fmt.Errorf("%s (go env CC) %w", cc, errNotFound)
	}
	return cc, nil
}

// Check runs every tool's check.
func (r Runner) Check(ctx context.Context, tools []Tool) []Result {
	results := make([]Result, len(tools))
	for i, t := range tools {
		v, err := t.Check(ctx, r)
		results[i] = Result{Tool: t, Version: v, Err: err}
	}
	return results
}

// Report prints the results and returns whether everything was found and suitable.
func Report(w io.Writer, results []Result) bool {
	ok := true
	for _, res := range results {
		if res.Err != nil {
			ok = false
		}
	}

	if ok {
		names := make([]string, len(results))
		for i, res := range results {
			names[i] = res.Tool.Name
			if res.Version != "" {
				names[i] += " " + res.Version
			}
		}
		fmt.Fprintf(w, "Tools: all found (%s).\n", strings.Join(names, ", "))
		return true
	}

	fmt.Fprintln(w, "Tools:")
	for _, res := range results {
		label := res.Tool.Name
		if res.Version != "" {
			label += " " + res.Version
		}
		if res.Err == nil {
			fmt.Fprintf(w, "  ok       %s\n", label)
			continue
		}
		fmt.Fprintf(w, "  problem  %s: %v\n", label, res.Err)
		fmt.Fprintf(w, "           Used to: %s\n", res.Tool.Why)
		fmt.Fprintf(w, "           Install: %s\n", res.Tool.Install)
	}
	fmt.Fprintln(w, "\nThe project can still be created. Fix the problems above before running make setup.")
	return false
}
