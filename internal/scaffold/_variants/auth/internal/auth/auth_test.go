package auth

import "testing"

func TestKeyLimiter(t *testing.T) {
	l := NewKeyLimiter(1e9, 2) // practically no refill during the test
	if !l.Allow("Ann@example.com") || !l.Allow(" ann@example.com") {
		t.Fatal("first two attempts denied")
	}
	if l.Allow("ANN@EXAMPLE.COM") {
		t.Error("third attempt allowed, or keys are case-sensitive")
	}
	if !l.Allow("bob@example.com") {
		t.Error("another key is limited too")
	}
}

func TestSafeNext(t *testing.T) {
	for next, want := range map[string]string{
		"":                       "/home",
		"/account":               "/account",
		"/a?b=c#d":               "/a?b=c#d",
		"//evil.example":         "/home",
		`/\evil.example`:         "/home",
		"https://evil.example/x": "/home",
		"javascript:alert(1)":    "/home",
		"account":                "/home",
	} {
		if got := SafeNext(next, "/home"); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", next, got, want)
		}
	}
}
