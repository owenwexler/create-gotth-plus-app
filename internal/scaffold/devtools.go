package scaffold

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// replaceFile replaces a template file completely. The file must be listed in replacedFiles, and
// the template file must still have the recorded hash, so upstream changes get reviewed.
func (p Project) replaceFile(name string, content []byte) error {
	if err := p.checkReplaceable(name); err != nil {
		return err
	}
	p[name] = content
	return nil
}

func (p Project) checkReplaceable(name string) error {
	old, exists := p[name]
	if !exists {
		return fmt.Errorf("%s: not in template", name)
	}
	want, ok := replacedFiles[name]
	if !ok {
		return fmt.Errorf("%s is replaced but not listed in replacedFiles", name)
	}
	sum := sha256.Sum256(old)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("template file %s changed (sha256 %s); review what replaces it, then update replacedFiles", name, got)
	}
	return nil
}

// replaceSpan replaces the text from start (which must occur once) up to and including the first
// end that follows it.
func (p Project) replaceSpan(file, start, end, new string) error {
	s := string(p[file])
	if n := strings.Count(s, start); n != 1 {
		return fmt.Errorf("%s: expected %q once, found it %d times", file, start, n)
	}
	i := strings.Index(s, start)
	after := i + len(start)
	j := strings.Index(s[after:], end)
	if j < 0 {
		return fmt.Errorf("%s: no %q after %q", file, end, start)
	}
	p[file] = []byte(s[:i] + new + s[after+j+len(end):])
	return nil
}

// airConfig fixes the template's air.toml, which only runs go build: without templ generate, a
// .templ edit rebuilds from stale *_templ.go files. It also turns on air's proxy, which reloads
// the browser like templ's does, on the same port.
func airConfig(port int) string {
	return fmt.Sprintf(`# Live reload with air (https://github.com/air-verse/air): make dev, or make air beside make dev-css.
# air only reads .air.toml on its own, so run it as air -c air.toml (the Makefile does).

[build]
  # templ generate first, so the build uses *_templ.go files that match the .templ files
  cmd = "templ generate && go build -o ./tmp/main ./cmd/server"
  entrypoint = ["./tmp/main"]

  # rebuild when Go or templ files change
  include_ext = ["go", "templ"]

  # the generated files change on every build; watching them would rebuild forever
  exclude_regex = [".*_templ.go"]

[proxy]
  # reloads the browser after each rebuild: open http://localhost:7331 (the app's dev cross-origin
  # protection already trusts this port). app_port must match Port in .env.
  enabled = true
  proxy_port = 7331
  app_port = %d
`, port)
}

func applyLiveReload(p Project, o options.Options) error {
	if err := p.replaceFile("air.toml", []byte(airConfig(o.Port))); err != nil {
		return err
	}
	// air only reads .air.toml by default, so without -c it ignores air.toml entirely
	if err := p.edits([]edit{
		{"Makefile", "(config in air.toml); run `make dev-css` beside it\n\tair\n", "(config in air.toml); run `make dev-css` beside it\n\tair -c air.toml\n"},
		{"AGENTS.md", "\nair                                              # live reload", "\nair -c air.toml                                  # live reload"},
	}); err != nil {
		return err
	}
	if o.LiveReload != options.Air {
		return nil
	}
	return p.edits([]edit{
		{"Makefile",
			"dev: ## Live reload: Tailwind and templ watchers; open the proxy at http://localhost:7331\n\t$(MAKE) -j2 dev-css dev-templ\n",
			"dev: ## Live reload with air (air.toml) and the Tailwind watcher; open the proxy at http://localhost:7331\n\t$(MAKE) -j2 dev-css air\n"},
		{"Makefile",
			"air: ## Alternative live reload with air (config in air.toml); run `make dev-css` beside it\n\tair -c air.toml\n",
			"air: ## Live reload with air alone (config in air.toml); make dev also runs the Tailwind watcher\n\tair -c air.toml\n"},
		{"README.md",
			"- Optional: [`air`](https://github.com/air-verse/air)\n",
			"- [`air`](https://github.com/air-verse/air) for live reload (`make dev`): `go install github.com/air-verse/air@latest`\n"},
	})
}

// e2eFiles are the Playwright tests and their harness.
var e2eFiles = []string{"cmd/server/e2e_helpers_test.go", "cmd/server/e2e_generic_test.go", "cmd/server/e2e_test.go"}

// removeE2E takes out the Playwright tests, their make targets and what the docs say about them.
func removeE2E(p Project) error {
	for _, f := range e2eFiles {
		if _, ok := p[f]; !ok {
			return fmt.Errorf("%s: not in template", f)
		}
		delete(p, f)
	}

	const gotthPlusTests = "https://github.com/owenwexler/gotth-plus/tree/main/cmd/server"
	if err := p.edits([]edit{
		// Makefile
		{"Makefile", "PLAYWRIGHT := go run github.com/mxschmitt/playwright-go/cmd/playwright\n\n", ""},
		{"Makefile", " test e2e-install vuln ", " test vuln "},
		{"Makefile", "the JS libraries, the test browser and a first build", "the JS libraries and a first build"},
		{"Makefile", "\t$(MAKE) js generate css e2e-install\n", "\t$(MAKE) js generate css\n"},
		{"Makefile", "vet: ## Vet everything, including the e2e tests\n\tgo vet -tags e2e ./...\n", "vet: ## Vet everything\n\tgo vet ./...\n"},
		{"Makefile", "e2e-install: ## Download the browser the end-to-end tests drive (once)\n\t$(PLAYWRIGHT) install chromium\n\n", ""},

		// tests
		{"cmd/server/testutil_test.go",
			"// afterTests run once, after every test in the package (the e2e build adds the shared browser's shutdown).",
			"// afterTests run once, after every test in the package."},

		// README
		{"README.md", "- Unit, integration and Playwright end-to-end tests\n", "- Unit and integration tests\n"},
		{"README.md", "make e2e-install   # once: downloads Chromium for Playwright-Go (`make setup` does this too)\n", ""},
		{"README.md", "                   # database) and end-to-end (a real browser)\n", "                   # database)\n"},
		{"README.md",
			"- the hello and counter tests in `cmd/server/integration_test.go` and `cmd/server/e2e_test.go` (keep `e2e_helpers_test.go` and `e2e_generic_test.go`)",
			"- the hello and counter tests in `cmd/server/integration_test.go`"},

		// AGENTS.md
		{"AGENTS.md", "go test -race -count=1 -tags e2e ./...          # everything, browser tests included (see Project-specific notes)\n", ""},
		{"AGENTS.md",
			"`make test`: unit, integration and end-to-end tests). The browser tests need `make e2e-install` once (`make setup` does it).",
			"`make test`: unit and integration tests). This project has no browser (E2E) tests."},

		// SECURITY.md
		{"SECURITY.md", "Unit tests for CSRF and the middleware; E2E checks that no third-party requests are made. |", "Unit tests for CSRF and the middleware. |"},
		{"SECURITY.md", "- `playwright-go` is a test-only dependency (behind the `e2e` build tag) and is not compiled into\n  the server binary.\n", ""},
		{"SECURITY.md",
			"- **No third-party requests:** the existing E2E check in `e2e_generic_test.go` catches a CDN script\n  or font sneaking in.\n",
			"- **No third-party requests:** this project has no browser tests, so nothing catches a CDN script or\n" +
				"  font sneaking in. Check the browser's network panel, or add GOTTH+'s E2E checks back\n" +
				"  (" + gotthPlusTests + ").\n"},
	}); err != nil {
		return err
	}

	spans := []struct{ file, start, end, new string }{
		{"Makefile", "# The e2e build tag adds the browser tests", "\tgo test -race -count=1 -tags e2e ./...\n",
			"test: generate css ## Unit and integration tests\n\tgo test -race -count=1 ./...\n"},
		{"Makefile", "\ncodegen:\n", "\n", ""},
		{"README.md", "The end-to-end tests are behind the `e2e` build tag", "otherwise it is skipped.\n",
			"This project was created without the Playwright end-to-end tests. GOTTH+ ships browser checks that\n" +
				"any app benefits from (no third-party requests, visible focus rings, accessible toasts, no\n" +
				"horizontal scroll, loading without JavaScript, an axe accessibility scan); consider adding them\n" +
				"back from " + gotthPlusTests + " before production.\n"},
		{"AGENTS.md", "; E2E tests (build tag `e2e`) drive Chromium", "and skips otherwise.", ". There are no browser (E2E) tests."},
	}
	for _, s := range spans {
		if err := p.replaceSpan(s.file, s.start, s.end, s.new); err != nil {
			return err
		}
	}
	return nil
}
