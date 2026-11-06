package helper

import "testing"

func TestTernary(t *testing.T) {
	if got := Ternary(true, "yes", "no"); got != "yes" {
		t.Errorf("Ternary(true) = %q", got)
	}
	if got := Ternary(false, 1, 2); got != 2 {
		t.Errorf("Ternary(false) = %d", got)
	}
}

func TestCount(t *testing.T) {
	even := func(n int) bool { return n%2 == 0 }

	if got := Count([]int{1, 2, 3, 4, 6}, even); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
	if got := Count(nil, even); got != 0 {
		t.Errorf("Count(nil) = %d, want 0", got)
	}
}
