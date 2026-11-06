package escaper

import "testing"

func TestLike(t *testing.T) {
	cases := map[string]string{
		"plain": "plain",
		"100%":  `100\%`,
		"a_b":   `a\_b`,
		`a\b`:   `a\\b`,
	}
	for in, want := range cases {
		if got := Like(in); got != want {
			t.Errorf("Like(%q) = %q, want %q", in, got, want)
		}
	}
}
