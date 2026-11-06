package config

import "testing"

func TestIsProduction(t *testing.T) {
	for goEnv, want := range map[string]bool{"production": true, "development": false, "": false, "Production": false} {
		if got := (Env{GoEnv: goEnv}).IsProduction(); got != want {
			t.Errorf("GoEnv=%q: IsProduction() = %v, want %v", goEnv, got, want)
		}
	}
}
