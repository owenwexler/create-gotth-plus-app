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
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// project makes a directory with the README heading recordJS needs.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	readme := "# app\n\n### The JavaScript libraries\n\ntext\n\n### Seed data\n\nseed\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakeDownload behaves like download-js.sh (alone or inside make setup): it writes the three files
// and prints their versions. Every command is recorded.
func fakeDownload(calls *[]string) Runner {
	return func(_ context.Context, dir string, out io.Writer, name string, args ...string) error {
		*calls = append(*calls, strings.Join(append([]string{name}, args...), " "))
		if err := os.MkdirAll(filepath.Join(dir, "static", "js"), 0o755); err != nil {
			return err
		}
		for _, f := range []string{"htmx.min.js", "alpine-csp.min.js", "hx-optimistic.min.js"} {
			if err := os.WriteFile(filepath.Join(dir, "static", "js", f), []byte("// "+f), 0o644); err != nil {
				return err
			}
		}
		fmt.Fprint(out, "htmx 2.0.11\n  htmx.min.js <- url\nalpine (csp build) 3.15.2\nhx-optimistic 1.4.0\ndone\n")
		return nil
	}
}

func steps(run Runner) Steps {
	return Steps{Out: io.Discard, Run: run, HTTP: http.DefaultClient, Today: func() string { return "2026-11-03" }}
}

func only(o options.Options) options.Options {
	o.CommitJS, o.Axe, o.Git, o.Setup = false, false, false, false
	return o
}

func TestDownloadJSRecordsVersions(t *testing.T) {
	dir := project(t)
	var calls []string
	o := only(options.Defaults())
	o.CommitJS = true

	outcomes := steps(fakeDownload(&calls)).RunAll(context.Background(), dir, o)
	if len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if len(calls) != 1 || calls[0] != "bash download-js.sh" {
		t.Errorf("calls = %v", calls)
	}

	readme, _ := os.ReadFile(filepath.Join(dir, "README.md"))
	sum := sha512.Sum384([]byte("// htmx.min.js"))
	for _, want := range []string{
		"#### Pinned versions\n",
		"| `htmx.min.js` | htmx | 2.0.11 | `sha384-" + base64.StdEncoding.EncodeToString(sum[:]) + "` |",
		"HTMX_VERSION=2.0.11 ALPINE_VERSION=3.15.2 HX_OPTIMISTIC_VERSION=1.4.0 ./download-js.sh",
		"Downloaded on 2026-11-03",
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README is missing %q:\n%s", want, readme)
		}
	}
	if strings.Index(string(readme), "#### Pinned versions") > strings.Index(string(readme), "### Seed data") {
		t.Error("pinned versions are not before the Seed data section")
	}
}

func TestSetupDownloadsJSOnce(t *testing.T) {
	dir := project(t)
	var calls []string
	o := only(options.Defaults())
	o.Setup, o.CommitJS = true, true

	outcomes := steps(fakeDownload(&calls)).RunAll(context.Background(), dir, o)
	if len(outcomes) != 1 || outcomes[0].Err != nil || outcomes[0].Step != "make setup" {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if len(calls) != 1 || calls[0] != "make setup" {
		t.Errorf("calls = %v, want only make setup (it downloads the JS itself)", calls)
	}
	if readme, _ := os.ReadFile(filepath.Join(dir, "README.md")); !strings.Contains(string(readme), "HTMX_VERSION=2.0.11") {
		t.Error("versions from make setup's output were not recorded")
	}
}

func TestFailedStepDoesNotStopOthers(t *testing.T) {
	dir := project(t)
	var calls []string
	run := func(_ context.Context, _ string, _ io.Writer, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "make" {
			return errors.New("exit status 2")
		}
		return nil
	}
	o := only(options.Defaults())
	o.Setup, o.Git = true, true

	outcomes := steps(run).RunAll(context.Background(), dir, o)
	if len(outcomes) != 2 || outcomes[0].Err == nil || outcomes[1].Err != nil {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	var buf bytes.Buffer
	if Report(&buf, outcomes) {
		t.Error("Report = true with a failed step")
	}
	if !strings.Contains(buf.String(), "Run by hand: make setup") {
		t.Errorf("report: %s", buf.String())
	}
}

// registry serves axe-core metadata and a tarball holding js; tamper changes the tarball after its
// hash was published.
func registry(t *testing.T, js string, tamper bool, tarballHost string) *httptest.Server {
	t.Helper()
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"package/package.json": "{}", "package/axe.min.js": js} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(tgz.Bytes())
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	served := tgz.Bytes()
	if tamper {
		served = append(append([]byte{}, served...), 0)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/axe-core/latest":
			host := srv.URL
			if tarballHost != "" {
				host = tarballHost
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": "4.11.0",
				"dist":    map[string]string{"tarball": host + "/axe-core/-/axe-core-4.11.0.tgz", "integrity": integrity},
			})
		case "/axe-core/-/axe-core-4.11.0.tgz":
			_, _ = w.Write(served)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func axeSteps(srv *httptest.Server) Steps {
	s := steps(nil)
	s.Registry = srv.URL
	return s
}

func axeOnly() options.Options {
	o := only(options.Defaults())
	o.Axe = true
	return o
}

func TestAxe(t *testing.T) {
	dir := t.TempDir()
	srv := registry(t, "/*! axe */", false, "")
	outcomes := axeSteps(srv).RunAll(context.Background(), dir, axeOnly())
	if outcomes[0].Err != nil {
		t.Fatal(outcomes[0].Err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "cmd", "server", "testdata", "axe.min.js"))
	if err != nil || string(b) != "/*! axe */" {
		t.Errorf("axe.min.js = %q, %v", b, err)
	}
	if !strings.Contains(outcomes[0].Note, "4.11.0") {
		t.Errorf("note = %q", outcomes[0].Note)
	}
}

func TestAxeRejectsTamperedTarball(t *testing.T) {
	dir := t.TempDir()
	outcomes := axeSteps(registry(t, "x", true, "")).RunAll(context.Background(), dir, axeOnly())
	if outcomes[0].Err == nil || !strings.Contains(outcomes[0].Err.Error(), "does not match") {
		t.Errorf("err = %v", outcomes[0].Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "server", "testdata", "axe.min.js")); err == nil {
		t.Error("a tarball that failed the hash check was extracted")
	}
}

func TestAxeRejectsTarballElsewhere(t *testing.T) {
	outcomes := axeSteps(registry(t, "x", false, "https://evil.example")).RunAll(context.Background(), t.TempDir(), axeOnly())
	if outcomes[0].Err == nil || !strings.Contains(outcomes[0].Err.Error(), "is not on") {
		t.Errorf("err = %v", outcomes[0].Err)
	}
}

func TestGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// a commit identity that doesn't depend on the machine, and no global or system config
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	dir := project(t)
	o := only(options.Defaults())
	o.Git = true
	s := System(io.Discard)

	outcomes := s.RunAll(context.Background(), dir, o)
	if outcomes[0].Err != nil {
		t.Fatal(outcomes[0].Err)
	}
	log, err := exec.Command("git", "-C", dir, "log", "--format=%s", "-1").Output()
	if err != nil || strings.TrimSpace(string(log)) != "Initial commit from create-gotth-plus-app" {
		t.Errorf("git log = %q, %v", log, err)
	}

	// a project inside an existing repository gets no repository of its own
	inner := filepath.Join(dir, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	outcomes = s.RunAll(context.Background(), inner, o)
	if outcomes[0].Err != nil || !strings.Contains(outcomes[0].Note, "already inside") {
		t.Errorf("outcome = %+v", outcomes[0])
	}
	if _, err := os.Stat(filepath.Join(inner, ".git")); err == nil {
		t.Error("a nested repository was created")
	}
}
