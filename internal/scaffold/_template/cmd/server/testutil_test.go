package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/app"
	"github.com/owenwexler/gotth-plus/internal/config"
	"github.com/owenwexler/gotth-plus/internal/database"
	"github.com/owenwexler/gotth-plus/internal/model"
)

// TestMain moves to the project root, because the server reads static/ and seedData.json relative
// to its working directory.
func TestMain(m *testing.M) {
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		fmt.Fprintln(os.Stderr, "chdir to project root:", err)
		os.Exit(1)
	}
	code := m.Run()
	for _, cleanup := range afterTests {
		cleanup()
	}
	os.Exit(code)
}

// afterTests run once, after every test in the package (the e2e build adds the shared browser's shutdown).
var afterTests []func()

// newTestApp builds the real handler (every route behind every middleware) on a throwaway database.
func newTestApp(t *testing.T, env config.Env) (http.Handler, *gorm.DB) {
	t.Helper()

	db, err := database.ConnectDb(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeDB(db) })

	// only the seed handler migrates in the app, so a fresh test database needs every model migrated here.
	// Add new models to this list.
	if err := db.AutoMigrate(&model.Example{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	return app.NewHandler(env, db), db
}

var devEnv = config.Env{GoEnv: "development", Port: "0"}
