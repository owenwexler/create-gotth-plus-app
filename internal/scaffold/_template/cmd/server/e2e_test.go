//go:build e2e

package main

import (
	"strings"
	"sync"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// End-to-end tests for the example app, in a real browser against the real handler.
// They are behind the e2e build tag so the normal suite stays fast and needs no browser:
//
//	make e2e-install   # once: downloads the browser
//	make test          # runs these with the unit and integration tests
//
// The harness (newPage, newE2EApp, expect, assert, ...) is in e2e_helpers_test.go; app-independent
// checks (accessibility, layout, no third-party requests) are in e2e_generic_test.go.

func TestE2ESplashPage(t *testing.T) {
	page, consoleErrors := newPage(t)

	expect(t, assert.Page(page).ToHaveTitle("GOTTH+"))
	expect(t, assert.Locator(page.GetByRole("heading", playwright.PageGetByRoleOptions{Name: "GOTTH+"})).ToBeVisible())
	expect(t, assert.Locator(page.Locator("#greeting")).ToHaveText("Hello!"))
	expect(t, assert.Locator(page.Locator("#counter-value")).ToHaveText("0"))

	// the strict CSP must not block anything the page needs
	if errs := consoleErrors(); len(errs) > 0 {
		t.Errorf("console errors on load:\n%s", strings.Join(errs, "\n"))
	}
}

func TestE2EHelloForm(t *testing.T) {
	page, consoleErrors := newPage(t)

	greeting := page.Locator("#greeting")
	name := page.GetByLabel("Your name")
	submit := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Say Hello"})

	expect(t, name.Fill("Gopher"))
	expect(t, submit.Click())
	expect(t, assert.Locator(greeting).ToHaveText("Hello Gopher!"))

	// submitting with Enter works too, and a new name replaces the old greeting
	expect(t, name.Fill("Ada"))
	expect(t, name.Press("Enter"))
	expect(t, assert.Locator(greeting).ToHaveText("Hello Ada!"))
	expect(t, assert.Locator(page.Locator("#greeting")).ToHaveCount(1))

	// markup in a name is shown as text, never run
	expect(t, name.Fill("<b>Bold</b>"))
	expect(t, submit.Click())
	expect(t, assert.Locator(greeting).ToHaveText("Hello <b>Bold</b>!"))
	expect(t, assert.Locator(page.Locator("#greeting b")).ToHaveCount(0))

	// the globalState store counted those requests in and back out again
	state, err := page.Evaluate(`(() => {
		const { pendingRequests, loading, online } = Alpine.store('globalState')
		return [pendingRequests, loading, online].join(',')
	})()`)
	expect(t, err)
	if state != "0,false,true" {
		t.Errorf("globalState pendingRequests,loading,online = %v, want 0,false,true", state)
	}

	if errs := consoleErrors(); len(errs) > 0 {
		t.Errorf("console errors:\n%s", strings.Join(errs, "\n"))
	}
}

func TestE2EHelloFormValidation(t *testing.T) {
	page, _ := newPage(t)

	greeting := page.Locator("#greeting")
	name := page.GetByLabel("Your name")
	submit := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Say Hello"})

	expect(t, name.Fill("Gopher"))
	expect(t, submit.Click())
	expect(t, assert.Locator(greeting).ToHaveText("Hello Gopher!"))

	// an empty name shows an error toast and leaves the greeting alone
	expect(t, name.Fill(""))
	expect(t, submit.Click())

	toast := page.Locator("#toasts [role=alert]")
	expect(t, assert.Locator(toast).ToBeVisible())
	expect(t, assert.Locator(toast).ToContainText("Please enter your name"))
	expect(t, assert.Locator(greeting).ToHaveText("Hello Gopher!"))

	// clicking a toast dismisses it
	expect(t, toast.Click())
	expect(t, assert.Locator(toast).ToBeHidden())
}

func TestE2ECounter(t *testing.T) {
	page, consoleErrors := newPage(t)

	value := page.Locator("#counter-value")
	increment := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Increment"})
	decrement := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Decrement"})
	reset := page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Reset"})

	// the counter is Alpine only: it must never talk to the server
	var requests []string
	var mu sync.Mutex
	page.OnRequest(func(r playwright.Request) {
		mu.Lock()
		requests = append(requests, r.Method()+" "+r.URL())
		mu.Unlock()
	})

	expect(t, assert.Locator(value).ToHaveText("0"))

	for i := 0; i < 3; i++ {
		expect(t, increment.Click())
	}
	expect(t, assert.Locator(value).ToHaveText("3"))

	expect(t, decrement.Click())
	expect(t, assert.Locator(value).ToHaveText("2"))

	expect(t, reset.Click())
	expect(t, assert.Locator(value).ToHaveText("0"))

	expect(t, decrement.Click())
	expect(t, assert.Locator(value).ToHaveText("-1"))

	mu.Lock()
	defer mu.Unlock()
	if len(requests) > 0 {
		t.Errorf("the counter made requests: %v", requests)
	}
	if errs := consoleErrors(); len(errs) > 0 {
		t.Errorf("console errors:\n%s", strings.Join(errs, "\n"))
	}
}

// TestE2EHelloFormServerDown: a request that never reaches the server shows an error toast (app.js handles
// htmx:sendError), and the form works again once the server is back.
func TestE2EHelloFormServerDown(t *testing.T) {
	a := newE2EApp(t)
	a.open(t, "/")
	name := a.Page.GetByLabel("Your name")
	submit := a.Page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Say Hello"})
	a.failRoute(t, "**/hello")

	expect(t, name.Fill("Gopher"))
	expect(t, submit.Click())

	toast := a.Page.Locator("#toasts [role=alert]")
	expect(t, assert.Locator(toast).ToContainText("Could not reach the server"))
	expect(t, assert.Locator(a.Page.Locator("#greeting")).ToHaveText("Hello!"))

	expect(t, a.Page.Unroute("**/hello"))
	expect(t, submit.Click())
	expect(t, assert.Locator(a.Page.Locator("#greeting")).ToHaveText("Hello Gopher!"))
}
