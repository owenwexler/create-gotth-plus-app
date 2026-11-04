//go:build e2e

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mxschmitt/playwright-go"

	"github.com/owenwexler/gotth-plus/internal/auth"
	"github.com/owenwexler/gotth-plus/internal/model"
	"github.com/owenwexler/gotth-plus/internal/repository"
)

// End-to-end tests for sign-in, in a real browser: htmx's handling of HX-Redirect and of the error
// toasts is what these check, on top of the integration tests in auth_test.go.

// addUser creates a confirmed account directly in the test database.
func addUser(t *testing.T, a *e2eApp, email, password string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	expect(t, err)
	now := time.Now()
	user := model.User{ID: uuid.New(), Email: email, PasswordHash: hash, EmailVerifiedAt: &now, SessionVersion: 1, CreatedAt: now, UpdatedAt: now}
	expect(t, repository.NewUserRepository(a.DB).Create(context.Background(), &user))
}

func TestE2ESignInAndOut(t *testing.T) {
	a := newE2EApp(t)
	addUser(t, a, "ann@example.com", testPassword)
	page := a.Page

	// a page that needs a session sends the browser to sign in, and back afterwards
	a.open(t, "/account")
	expect(t, assert.Page(page).ToHaveURL(a.URL+"/login?next=%2Faccount"))

	email := page.GetByLabel("Email")
	password := page.GetByLabel("Password")
	signIn := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Sign in"})

	// a wrong password shows a toast and leaves the form, with what was typed, in place
	expect(t, email.Fill("ann@example.com"))
	expect(t, password.Fill("not the right password at all"))
	expect(t, signIn.Click())
	expect(t, assert.Locator(page.GetByRole("alert")).ToContainText("The email or password is incorrect."))
	expect(t, assert.Locator(email).ToHaveValue("ann@example.com"))

	expect(t, password.Fill(testPassword))
	expect(t, signIn.Click())
	expect(t, assert.Page(page).ToHaveURL(a.URL+"/account"))
	expect(t, assert.Locator(page.GetByText("ann@example.com")).ToBeVisible())

	// the home page links to the account now
	a.open(t, "/")
	expect(t, assert.Locator(page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "Account"})).ToBeVisible())

	a.open(t, "/account")
	expect(t, page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Sign out", Exact: playwright.Bool(true)}).Click())
	expect(t, assert.Page(page).ToHaveURL(a.URL+"/"))
	expect(t, assert.Locator(page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "Sign in"})).ToBeVisible())

	// the browser logs the wrong password's 401 itself; anything else (a CSP violation, a script error) fails
	for _, e := range a.ConsoleErrors() {
		if !strings.Contains(e, "status of 401") {
			t.Errorf("console error: %s", e)
		}
	}
}

// TestE2ESignedOutHTMXRequestNavigates: when a session ends, the next htmx request (here, the sign
// out button on a stale account page) follows HX-Redirect to the sign-in page instead of failing.
func TestE2ESignedOutHTMXRequestNavigates(t *testing.T) {
	a := newE2EApp(t)
	addUser(t, a, "ann@example.com", testPassword)
	page := a.Page

	a.open(t, "/login")
	expect(t, page.GetByLabel("Email").Fill("ann@example.com"))
	expect(t, page.GetByLabel("Password").Fill(testPassword))
	expect(t, page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Sign in"}).Click())
	expect(t, assert.Page(page).ToHaveURL(a.URL+"/account"))

	// the session disappears behind the page's back (signed out in another tab, or it expired)
	expect(t, a.Context.ClearCookies(playwright.BrowserContextClearCookiesOptions{Name: "session"}))
	expect(t, page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Sign out everywhere"}).Click())
	expect(t, assert.Page(page).ToHaveURL(a.URL+"/login"))
}

// TestE2EAuthPagesHaveNoSeriousAxeViolations scans the sign-in pages like the home page is scanned.
func TestE2EAuthPagesHaveNoSeriousAxeViolations(t *testing.T) {
	axePath := os.Getenv("AXE_CORE_JS")
	if axePath == "" {
		axePath = "cmd/server/testdata/axe.min.js"
	}
	if _, err := os.Stat(axePath); err != nil {
		t.Skipf("axe-core not found at %s: set AXE_CORE_JS or copy axe.min.js there", axePath)
	}

	a := newE2EApp(t)
	scan := a.inContext(t, playwright.BrowserNewContextOptions{BypassCSP: playwright.Bool(true)})
	for _, path := range []string{"/login", "/signup", "/forgot-password", "/reset-password?token=x", "/verify-email?token=x"} {
		scan.open(t, path)
		expect(t, scan.Page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateNetworkidle}))
		_, err := scan.Page.AddScriptTag(playwright.PageAddScriptTagOptions{Path: playwright.String(axePath)})
		expect(t, err)
		result, err := scan.Page.Evaluate(`axe.run().then(r => r.violations
			.filter(v => v.impact === 'serious' || v.impact === 'critical')
			.map(v => v.impact + ' ' + v.id + ': ' + v.help + ' (' + v.nodes.length + ' elements)'))`)
		expect(t, err)
		if violations := result.([]any); len(violations) > 0 {
			lines := make([]string, len(violations))
			for i, v := range violations {
				lines[i] = fmt.Sprint(v)
			}
			t.Errorf("%s: axe found serious or critical violations:\n%s", path, strings.Join(lines, "\n"))
		}
	}
}
