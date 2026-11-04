// Package post runs the steps after a project's files are written: make setup, downloading the JS
// libraries and axe-core, and git init. Each step reports its own outcome; a failed step never stops
// the others or removes the project, and says what to run by hand instead.
package post

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// Runner runs a command in dir, writing its output to out.
type Runner func(ctx context.Context, dir string, out io.Writer, name string, args ...string) error

// Steps holds what the steps need. Tests replace the runner, the HTTP client and the registry.
type Steps struct {
	Out      io.Writer
	Run      Runner
	HTTP     *http.Client
	Registry string // npm registry base URL, for axe-core
	Today    func() string
}

// System returns Steps that run real commands and download from the real npm registry.
func System(out io.Writer) Steps {
	return Steps{
		Out: out,
		Run: func(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir = dir
			cmd.Stdout = out
			cmd.Stderr = out
			return cmd.Run()
		},
		HTTP:     &http.Client{Timeout: 2 * time.Minute},
		Registry: "https://registry.npmjs.org",
		Today:    func() string { return time.Now().Format("2006-01-02") },
	}
}

// Outcome is the result of one step.
type Outcome struct {
	Step string
	Err  error
	Note string // shown on success, e.g. the version downloaded
	Fix  string // what to run by hand when the step failed
}

// RunAll runs the steps o asks for, in order: make setup (or only the JS download), axe-core, git.
// git comes last so the first commit includes the downloaded files.
func (s Steps) RunAll(ctx context.Context, dir string, o options.Options) []Outcome {
	var outcomes []Outcome

	switch {
	case o.Setup:
		outcomes = append(outcomes, s.setup(ctx, dir, o.CommitJS))
	case o.CommitJS:
		outcomes = append(outcomes, s.downloadJS(ctx, dir))
	}
	if o.Axe {
		outcomes = append(outcomes, s.axe(ctx, dir))
	}
	if o.Git {
		outcomes = append(outcomes, s.git(ctx, dir))
	}
	return outcomes
}

// setup runs make setup, which also downloads the JS libraries; with commitJS their versions are
// read from its output and recorded.
func (s Steps) setup(ctx context.Context, dir string, commitJS bool) Outcome {
	fmt.Fprintf(s.Out, "\n== make setup\n")
	var buf bytes.Buffer
	err := s.Run(ctx, dir, io.MultiWriter(s.Out, &buf), "make", "setup")
	out := Outcome{Step: "make setup", Err: err, Fix: "make setup"}
	if commitJS {
		// record what was downloaded even if a later part of make setup failed
		if note, recErr := s.recordJS(dir, buf.String()); recErr == nil {
			out.Note = note
		} else if err == nil {
			out.Err = recErr
		}
	}
	return out
}

func (s Steps) downloadJS(ctx context.Context, dir string) Outcome {
	fmt.Fprintf(s.Out, "\n== download-js.sh\n")
	var buf bytes.Buffer
	out := Outcome{Step: "JS libraries", Fix: "make js"}
	if out.Err = s.Run(ctx, dir, io.MultiWriter(s.Out, &buf), "bash", "download-js.sh"); out.Err != nil {
		return out
	}
	out.Note, out.Err = s.recordJS(dir, buf.String())
	return out
}

// library is one file download-js.sh writes, and the line it prints with the version.
type library struct {
	file, name string
	versionEnv string
	line       *regexp.Regexp
}

var libraries = []library{
	{"htmx.min.js", "htmx", "HTMX_VERSION", regexp.MustCompile(`(?m)^htmx (\S+)$`)},
	{"alpine-csp.min.js", "Alpine.js (CSP build)", "ALPINE_VERSION", regexp.MustCompile(`(?m)^alpine \(csp build\) (\S+)$`)},
	{"hx-optimistic.min.js", "hx-optimistic", "HX_OPTIMISTIC_VERSION", regexp.MustCompile(`(?m)^hx-optimistic (\S+)$`)},
}

// recordJS adds the downloaded versions and SHA-384 hashes to the README, with the command that
// downloads exactly these versions again.
func (s Steps) recordJS(dir, output string) (string, error) {
	var rows, pins, short []string
	for _, lib := range libraries {
		m := lib.line.FindStringSubmatch(output)
		if m == nil {
			return "", fmt.Errorf("download-js.sh did not report a version for %s", lib.name)
		}
		b, err := os.ReadFile(filepath.Join(dir, "static", "js", lib.file))
		if err != nil {
			return "", err
		}
		sum := sha512.Sum384(b)
		hash := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
		rows = append(rows, fmt.Sprintf("| `%s` | %s | %s | `%s` |", lib.file, lib.name, m[1], hash))
		pins = append(pins, lib.versionEnv+"="+m[1])
		short = append(short, lib.file+" "+m[1])
	}

	section := "#### Pinned versions\n\n" +
		"Downloaded on " + s.Today() + " when the project was created:\n\n" +
		"| File | Library | Version | SHA-384 |\n|---|---|---|---|\n" +
		strings.Join(rows, "\n") + "\n\n" +
		"To download exactly these versions again:\n\n" +
		"```bash\n" + strings.Join(pins, " ") + " ./download-js.sh\n```\n\n" +
		"To upgrade, read each library's release notes, run the script with the new versions, compare the\n" +
		"printed hashes with the upstream release, update this table and commit.\n\n"

	readme := filepath.Join(dir, "README.md")
	b, err := os.ReadFile(readme)
	if err != nil {
		return "", err
	}
	const anchor = "### Seed data\n"
	if strings.Count(string(b), anchor) != 1 {
		return "", errors.New("README.md has no single \"### Seed data\" heading to put the pinned versions before")
	}
	b = []byte(strings.Replace(string(b), anchor, section+anchor, 1))
	if err := os.WriteFile(readme, b, 0o644); err != nil {
		return "", err
	}
	return strings.Join(short, ", ") + "; versions and hashes are in README.md", nil
}

// maxDownload bounds the axe-core tarball (a few MB) and the file in it.
const maxDownload = 32 << 20

// axe downloads the latest axe-core from the npm registry, checks the tarball against the
// registry's sha512 integrity hash, and extracts axe.min.js to cmd/server/testdata, where the
// accessibility test looks for it.
func (s Steps) axe(ctx context.Context, dir string) Outcome {
	out := Outcome{Step: "axe-core", Fix: "download axe.min.js from https://www.npmjs.com/package/axe-core into cmd/server/testdata/"}
	version, err := s.fetchAxe(ctx, dir)
	if err != nil {
		out.Err = err
		return out
	}
	out.Note = "axe-core " + version + " in cmd/server/testdata/axe.min.js"
	return out
}

func (s Steps) fetchAxe(ctx context.Context, dir string) (string, error) {
	fmt.Fprintf(s.Out, "\n== axe-core\n")
	base, err := url.Parse(s.Registry)
	if err != nil {
		return "", err
	}

	var meta struct {
		Version string `json:"version"`
		Dist    struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	body, err := s.get(ctx, strings.TrimSuffix(s.Registry, "/")+"/axe-core/latest")
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", fmt.Errorf("read axe-core metadata: %w", err)
	}

	// only download from the registry itself, whatever the metadata says
	tarURL, err := url.Parse(meta.Dist.Tarball)
	if err != nil || tarURL.Scheme != base.Scheme || tarURL.Host != base.Host {
		return "", fmt.Errorf("axe-core tarball %q is not on %s", meta.Dist.Tarball, s.Registry)
	}
	want, ok := strings.CutPrefix(meta.Dist.Integrity, "sha512-")
	if !ok {
		return "", fmt.Errorf("axe-core %s has no sha512 integrity hash", meta.Version)
	}

	tarball, err := s.get(ctx, tarURL.String())
	if err != nil {
		return "", err
	}
	sum := sha512.Sum512(tarball)
	if base64.StdEncoding.EncodeToString(sum[:]) != want {
		return "", fmt.Errorf("axe-core %s tarball does not match the registry's sha512 hash", meta.Version)
	}

	js, err := extract(tarball, "package/axe.min.js")
	if err != nil {
		return "", fmt.Errorf("axe-core %s: %w", meta.Version, err)
	}
	dest := filepath.Join(dir, "cmd", "server", "testdata")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, "axe.min.js"), js, 0o644); err != nil {
		return "", err
	}
	fmt.Fprintf(s.Out, "  axe-core %s, sha512 verified\n", meta.Version)
	return meta.Version, nil
}

func (s Steps) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownload {
		return nil, fmt.Errorf("GET %s: larger than %d bytes", u, maxDownload)
	}
	return b, nil
}

// extract returns one regular file from a .tgz.
func extract(tgz []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s not in the tarball", name)
		}
		if err != nil {
			return nil, err
		}
		if h.Name != name || h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxDownload+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxDownload {
			return nil, fmt.Errorf("%s is larger than %d bytes", name, maxDownload)
		}
		return b, nil
	}
}

// git makes the project a repository with one commit, unless it is already inside one.
func (s Steps) git(ctx context.Context, dir string) Outcome {
	fmt.Fprintf(s.Out, "\n== git\n")
	out := Outcome{Step: "git", Fix: "git init -b main && git add -A && git commit -m 'Initial commit from create-gotth-plus-app'"}

	if s.Run(ctx, dir, io.Discard, "git", "rev-parse", "--is-inside-work-tree") == nil {
		out.Note = "already inside a git repository, so no new one was made"
		return out
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
	} {
		if out.Err = s.Run(ctx, dir, s.Out, "git", args...); out.Err != nil {
			return out
		}
	}
	if err := s.Run(ctx, dir, s.Out, "git", "commit", "-q", "-m", "Initial commit from create-gotth-plus-app"); err != nil {
		out.Err = fmt.Errorf("git commit: %w (is git config user.name and user.email set?)", err)
		out.Fix = "git config --global user.name 'Your Name' && git config --global user.email you@example.com && git commit -m 'Initial commit from create-gotth-plus-app'"
		return out
	}
	out.Note = "repository on branch main with a first commit"
	return out
}

// Report prints the outcomes and returns whether every step succeeded.
func Report(w io.Writer, outcomes []Outcome) bool {
	if len(outcomes) == 0 {
		return true
	}
	ok := true
	fmt.Fprintln(w, "\nAfter-creation steps:")
	for _, o := range outcomes {
		if o.Err != nil {
			ok = false
			fmt.Fprintf(w, "  failed   %s: %v\n", o.Step, o.Err)
			fmt.Fprintf(w, "           Run by hand: %s\n", o.Fix)
			continue
		}
		fmt.Fprintf(w, "  ok       %s", o.Step)
		if o.Note != "" {
			fmt.Fprintf(w, ": %s", o.Note)
		}
		fmt.Fprintln(w)
	}
	return ok
}
