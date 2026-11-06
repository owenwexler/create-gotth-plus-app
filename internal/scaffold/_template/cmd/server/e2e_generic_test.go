//go:build e2e

package main

// Checks that hold for any app built on this template, run against the home page. Keep them when you
// replace the example app; point them at more pages as your app grows.

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

// TestE2EPreloadExtension: htmx's preload extension loads under the CSP and fetches a link marked
// preload="mouseover" while the pointer rests on it, before any click. Nothing in the template uses
// preloading, so the test adds a link like one an app would mark.
func TestE2EPreloadExtension(t *testing.T) {
	a := newE2EApp(t)
	a.open(t, "/")
	// /healthz is a cheap GET without side effects, the kind of request preloading is for
	_, err := a.Page.Evaluate(`(() => {
		const link = document.createElement('a')
		link.id = 'preload-probe'
		link.href = '/healthz'
		link.textContent = 'Preload probe'
		link.setAttribute('preload', 'mouseover')
		document.body.prepend(link)
		htmx.process(link)
	})()`)
	expect(t, err)

	before := a.countRequests("GET", "/healthz")
	expect(t, a.Page.Locator("#preload-probe").Hover())
	eventually(t, "the hovered link is preloaded", func() bool { return a.countRequests("GET", "/healthz") > before })
	a.expectNoConsoleErrors(t)
}

// TestE2ENoThirdPartyRequests: everything (scripts, styles, fonts) is self-hosted, and nothing errors.
func TestE2ENoThirdPartyRequests(t *testing.T) {
	a := newE2EApp(t)

	a.open(t, "/")
	expect(t, a.Page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{State: playwright.LoadStateNetworkidle}))

	app, err := url.Parse(a.URL)
	expect(t, err)
	for _, r := range a.Requests() {
		u, err := url.Parse(r.URL)
		if err != nil || (u.Scheme != "data" && u.Host != app.Host) {
			t.Errorf("request to another host: %s %s", r.Method, r.URL)
		}
	}
	a.expectNoConsoleErrors(t)
}

// TestE2EToastRegionIsLiveRegion: screen readers announce toasts. The container has to be the live
// region, because a live region added together with its content is often not announced.
func TestE2EToastRegionIsLiveRegion(t *testing.T) {
	a := newE2EApp(t)

	a.open(t, "/")

	expect(t, assert.Locator(a.Page.Locator("#toasts")).ToHaveAttribute("aria-live", "polite"))
}

// focusedControl describes document.activeElement: an id (or tag and text) and whether a focus indicator
// (an outline or a box shadow) is drawn.
const focusedControl = `(() => {
	const el = document.activeElement
	if (!el || el === document.body) return null
	const s = getComputedStyle(el)
	const ring = (s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0) || s.boxShadow !== 'none'
	const key = el.id || (el.tagName + ':' + (el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 30))
	return { key, ring }
})()`

// TestE2EKeyboardFocusIsVisible tabs through every control on the page: each one must show where the
// focus is. (Tailwind v4 trap: focus:outline-none next to a focus-visible:outline-* ring hides the ring.)
func TestE2EKeyboardFocusIsVisible(t *testing.T) {
	a := newE2EApp(t)
	a.open(t, "/")

	reached := map[string]bool{}
	var noRing []string
	for i := 0; i < 60; i++ {
		expect(t, a.Page.Keyboard().Press("Tab"))
		got, err := a.Page.Evaluate(focusedControl)
		expect(t, err)
		if got == nil {
			continue
		}
		control := got.(map[string]any)
		key := fmt.Sprint(control["key"])
		if reached[key] {
			break // wrapped around
		}
		reached[key] = true
		if control["ring"] != true {
			noRing = append(noRing, key)
		}
	}

	if len(reached) == 0 {
		t.Fatal("Tab never focused anything")
	}
	if len(noRing) > 0 {
		t.Errorf("no visible focus indicator on: %s", strings.Join(noRing, ", "))
	}
}

// TestE2ENoHorizontalScroll: the layout fits a phone and a desktop without sideways scrolling.
func TestE2ENoHorizontalScroll(t *testing.T) {
	for _, width := range []int{375, 1280} {
		t.Run(fmt.Sprintf("%dpx", width), func(t *testing.T) {
			a := newE2EApp(t)
			a.setViewport(t, width, 800)
			a.open(t, "/")

			overflow, err := a.Page.Evaluate(`document.documentElement.scrollWidth - document.documentElement.clientWidth`)
			expect(t, err)
			if px, _ := overflow.(int); px > 0 {
				t.Errorf("page scrolls horizontally by %dpx at %dpx wide", px, width)
			}
		})
	}
}

// TestE2EPageLoadsWithoutJavaScript: the server-rendered page still arrives and has a title. Anything that
// needs htmx or Alpine will not work; that is expected.
func TestE2EPageLoadsWithoutJavaScript(t *testing.T) {
	a := newE2EApp(t)
	noJS := a.inContext(t, playwright.BrowserNewContextOptions{JavaScriptEnabled: playwright.Bool(false)})

	resp, err := noJS.Page.Goto(a.URL)
	expect(t, err)

	if resp.Status() != 200 {
		t.Errorf("status %d, want 200", resp.Status())
	}
	expect(t, assert.Page(noJS.Page).ToHaveTitle(regexp.MustCompile(`\S`)))
}

// TestE2EAxeFindsNoSeriousViolations runs the axe-core accessibility scanner. axe is not vendored: point
// AXE_CORE_JS at an axe.min.js, or copy one to cmd/server/testdata/axe.min.js, to run it.
func TestE2EAxeFindsNoSeriousViolations(t *testing.T) {
	axePath := os.Getenv("AXE_CORE_JS")
	if axePath == "" {
		axePath = "cmd/server/testdata/axe.min.js"
	}
	if _, err := os.Stat(axePath); err != nil {
		t.Skipf("axe-core not found at %s: set AXE_CORE_JS or copy axe.min.js there", axePath)
	}

	a := newE2EApp(t)
	// the CSP only allows the app's own scripts, so this context bypasses it to inject axe
	scan := a.inContext(t, playwright.BrowserNewContextOptions{BypassCSP: playwright.Bool(true)})
	scan.open(t, "/")
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
		t.Errorf("axe found serious or critical violations:\n%s", strings.Join(lines, "\n"))
	}
}
