package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
)

type Env struct {
	GoEnv string
	Port  string
	// DatabasePath is the SQLite file. Defaults to app.db.
	DatabasePath string
	// SeedAdminKey optionally protects POST /seed in development. Never used in production.
	SeedAdminKey string
}

// IsProduction reports whether the app runs in production mode.
func (e Env) IsProduction() bool {
	return e.GoEnv == "production"
}

func GetEnv() Env {
	// .env is for local development; in production the platform provides the environment, so a
	// missing file is fine. Anything else (e.g. a malformed file) is not.
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		panic(err)
	}

	env := Env{
		GoEnv:        os.Getenv("GoEnv"),
		Port:         os.Getenv("Port"),
		DatabasePath: os.Getenv("DatabasePath"),
		SeedAdminKey: os.Getenv("SeedAdminKey"),
	}

	// fail fast: an unknown mode must never silently behave like development
	if env.GoEnv != "development" && env.GoEnv != "production" {
		panic(fmt.Sprintf(`GoEnv must be "development" or "production", got %q`, env.GoEnv))
	}

	if env.Port == "" {
		panic("Port must be set")
	}

	if env.DatabasePath == "" {
		env.DatabasePath = "app.db"
	}

	return env
}
