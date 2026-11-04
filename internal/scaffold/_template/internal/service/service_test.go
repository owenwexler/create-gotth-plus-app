package service

import "testing"

func TestGreet(t *testing.T) {
	svc := NewGreetingService()

	for name, want := range map[string]string{
		"":       "Hello!",
		"   ":    "Hello!",
		"Gopher": "Hello Gopher!",
		" Ada ":  "Hello Ada!",
	} {
		if got := svc.Greet(name); got != want {
			t.Errorf("Greet(%q) = %q, want %q", name, got, want)
		}
	}
}
