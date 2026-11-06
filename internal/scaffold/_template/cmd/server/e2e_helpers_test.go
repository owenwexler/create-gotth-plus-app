//go:build e2e

package main

// The end-to-end harness: the app on a random port, a browser page pointed at it, and helpers that any
// app's browser tests need. App-specific locators and actions belong in your own test files.

import (
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/config"
)

var (
	browserOnce sync.Once
	pw          *playwright.Playwright
	browser     playwright.Browser
	browserErr  error
)

func init() { afterTests = append(afterTests, stopBrowser) }

// sharedBrowser starts Playwright and Chromium once for the whole package. Each test still gets its own
// browser context (fresh cookies and storage), which is much faster than a browser per test.
func sharedBrowser(t *testing.T) playwright.Browser {
	t.Helper()

	browserOnce.Do(func() {
		if pw, browserErr = playwright.Run(); browserErr != nil {
			return
		}
		browser, browserErr = pw.Chromium.Launch()
	})
	if browserErr != nil {
		t.Fatalf("starting chromium (run `make e2e-install` first): %v", browserErr)
	}
	return browser
}

// stopBrowser shuts the shared browser down after the last test (see afterTests in testutil_test.go).
func stopBrowser() {
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
}

// e2eApp is the app running on a random port with an empty database, plus a browser page for it.
type e2eApp struct {
	URL  string
	DB   *gorm.DB
	Page playwright.Page
	// Context is this test's browser context; newTab opens more pages in it (same cookies).
	Context playwright.BrowserContext
	// Browser is shared by all tests; inContext uses it for pages with other options (no JS, a viewport, ...).
	Browser playwright.Browser

	mu            sync.Mutex
	consoleErrors []string
	requests      []recordedRequest
}

// recordedRequest is one request the page made, in order.
type recordedRequest struct {
	Method string
	URL    string
}

// newE2EApp starts the app and opens a fresh page, but does not navigate yet, so a test can seed data or
// set up routes first and then call open. Pass env to override the development config.
func newE2EApp(t *testing.T, env ...config.Env) *e2eApp {
	t.Helper()

	for _, lib := range []string{"htmx.min.js", "alpine-csp.min.js", "hx-optimistic.min.js"} {
		if _, err := os.Stat("static/js/" + lib); err != nil {
			t.Fatalf("static/js/%s is missing: run `make js` first", lib)
		}
	}
	if _, err := os.Stat("static/output.css"); err != nil {
		t.Fatal("static/output.css is missing: run `make css` first")
	}

	cfg := devEnv
	if len(env) > 0 {
		cfg = env[0]
	}
	handler, db := newTestApp(t, cfg)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	shared := sharedBrowser(t)
	browserContext, err := shared.NewContext()
	expect(t, err)
	t.Cleanup(func() { _ = browserContext.Close() })

	a := &e2eApp{URL: server.URL, DB: db, Context: browserContext, Browser: shared}
	a.Page = a.watch(t, browserContext)
	return a
}

// watch opens a page in the context that records console errors (where Content-Security-Policy
// violations and failed script loads show up) and requests.
func (a *e2eApp) watch(t *testing.T, browserContext playwright.BrowserContext) playwright.Page {
	t.Helper()

	page, err := browserContext.NewPage()
	expect(t, err)
	page.OnConsole(func(msg playwright.ConsoleMessage) {
		if msg.Type() == "error" {
			a.recordError(msg.Text())
		}
	})
	page.OnPageError(func(err error) { a.recordError(err.Error()) })
	page.OnRequest(func(r playwright.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.requests = append(a.requests, recordedRequest{Method: r.Method(), URL: r.URL()})
	})
	return page
}

// newPage starts the app and opens its home page; its second result returns the console errors so far.
func newPage(t *testing.T) (playwright.Page, func() []string) {
	t.Helper()

	a := newE2EApp(t)
	a.open(t, "/")
	return a.Page, a.ConsoleErrors
}

func (a *e2eApp) recordError(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.consoleErrors = append(a.consoleErrors, msg)
}

// ConsoleErrors returns the browser console errors and uncaught page errors seen so far.
func (a *e2eApp) ConsoleErrors() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.consoleErrors...)
}

// Requests returns every request the page has made so far, in order.
func (a *e2eApp) Requests() []recordedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedRequest(nil), a.requests...)
}

// countRequests counts the requests so far whose method matches and whose URL contains path.
func (a *e2eApp) countRequests(method, path string) int {
	n := 0
	for _, r := range a.Requests() {
		if r.Method == method && strings.Contains(r.URL, path) {
			n++
		}
	}
	return n
}

// open loads a path on the app, such as "/" or "/?q=x".
func (a *e2eApp) open(t *testing.T, path string) {
	t.Helper()

	if _, err := a.Page.Goto(a.URL + path); err != nil {
		t.Fatal(err)
	}
}

func (a *e2eApp) reload(t *testing.T) {
	t.Helper()

	if _, err := a.Page.Reload(); err != nil {
		t.Fatal(err)
	}
}

func (a *e2eApp) setViewport(t *testing.T, width, height int) {
	t.Helper()
	expect(t, a.Page.SetViewportSize(width, height))
}

// newTab opens a second page in the same browser context (same cookies) and loads path in it.
func (a *e2eApp) newTab(t *testing.T, path string) *e2eApp {
	t.Helper()

	tab := &e2eApp{URL: a.URL, DB: a.DB, Context: a.Context, Browser: a.Browser}
	tab.Page = tab.watch(t, a.Context)
	tab.open(t, path)
	return tab
}

// inContext opens a page in a new browser context with other options (no JavaScript, a CSP bypass, a
// viewport, ...) against the same app and database. The page is not loaded yet.
func (a *e2eApp) inContext(t *testing.T, options playwright.BrowserNewContextOptions) *e2eApp {
	t.Helper()

	browserContext, err := a.Browser.NewContext(options)
	expect(t, err)
	t.Cleanup(func() { _ = browserContext.Close() })

	other := &e2eApp{URL: a.URL, DB: a.DB, Context: browserContext, Browser: a.Browser}
	other.Page = other.watch(t, browserContext)
	return other
}

// delayRoute holds every request matching pattern (a glob such as "**/hello") for d before sending it on,
// so in-flight states (spinners, disabled buttons, optimistic updates) can be observed.
func (a *e2eApp) delayRoute(t *testing.T, pattern string, d time.Duration) {
	t.Helper()

	expect(t, a.Page.Route(pattern, func(route playwright.Route) {
		time.Sleep(d)
		_ = route.Continue()
	}))
}

// failRoute makes every request matching pattern fail at the network level, as if the server were down.
// Undo it with a.Page.Unroute(pattern) only once the failed request has finished (wait for its error
// toast): unrouting while a request is still being routed races inside playwright-go and can let it through.
func (a *e2eApp) failRoute(t *testing.T, pattern string) {
	t.Helper()

	expect(t, a.Page.Route(pattern, func(route playwright.Route) {
		_ = route.Abort("connectionrefused")
	}))
}

// markPage tags the current document; expectNotReloaded then proves an action updated the page in place.
func (a *e2eApp) markPage(t *testing.T) {
	t.Helper()

	_, err := a.Page.Evaluate(`window.__e2eMarker = true`)
	expect(t, err)
}

func (a *e2eApp) expectNotReloaded(t *testing.T) {
	t.Helper()

	marked, err := a.Page.Evaluate(`window.__e2eMarker === true`)
	expect(t, err)
	if marked != true {
		t.Error("the page reloaded")
	}
}

func (a *e2eApp) expectNoConsoleErrors(t *testing.T) {
	t.Helper()

	if errs := a.ConsoleErrors(); len(errs) > 0 {
		t.Errorf("console errors:\n%s", strings.Join(errs, "\n"))
	}
}

// eventually polls cond for up to five seconds. Use it for state outside the page, such as the database,
// which web-first assertions can't wait on.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// expect fails the test if a Playwright call returned an error. Every action in playwright-go returns one,
// so wrap them: expect(t, locator.Click()).
func expect(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// assert builds web-first assertions that retry for up to 5 seconds:
// expect(t, assert.Locator(loc).ToHaveText("x")).
var assert = playwright.NewPlaywrightAssertions(5000)
