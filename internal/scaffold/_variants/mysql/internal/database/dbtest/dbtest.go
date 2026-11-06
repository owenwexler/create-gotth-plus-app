// Package dbtest gives each test its own throwaway MySQL database, created on the server in
// TestDatabaseURL (or DatabaseURL) and dropped when the test ends.
package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"gorm.io/gorm"

	"github.com/owenwexler/gotth-plus/internal/database"
)

// Open creates an empty database and returns a connection to it. The database is dropped and the
// connection closed when the test ends. The server needs a user that may create and drop databases;
// don't point TestDatabaseURL at a production server.
func Open(t testing.TB) *gorm.DB {
	t.Helper()

	cfg, err := mysql.ParseDSN(serverDSN(t))
	if err != nil {
		t.Fatalf("TestDatabaseURL (or DatabaseURL) is not a valid MySQL DSN: %v", err)
	}
	redacted := *cfg
	redacted.Passwd = "xxxxx"

	// connect to the server without selecting a database
	serverCfg := cfg.Clone()
	serverCfg.DBName = ""
	admin, err := database.ConnectDb(serverCfg.FormatDSN())
	if err != nil {
		t.Fatalf("tests need a MySQL server: %v (TestDatabaseURL or DatabaseURL is %s)", err, redacted.FormatDSN())
	}

	name := "test_" + randomHex(t)
	// name is generated above (letters, digits, underscore), so it is safe to put into the SQL
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		closeDB(admin)
		t.Fatalf("create test database (the user needs CREATE and DROP): %v", err)
	}

	testCfg := cfg.Clone()
	testCfg.DBName = name
	db, err := database.ConnectDb(testCfg.FormatDSN())
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}

	t.Cleanup(func() {
		closeDB(db)
		if err := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`").Error; err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
		closeDB(admin)
	})
	return db
}

// serverDSN is TestDatabaseURL, else DatabaseURL, from the environment or else from the project's .env.
func serverDSN(t testing.TB) string {
	t.Helper()
	for _, key := range []string{"TestDatabaseURL", "DatabaseURL"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}

	env, err := godotenv.Read(filepath.Join(projectRoot(t), ".env"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read .env: %v", err)
	}
	for _, key := range []string{"TestDatabaseURL", "DatabaseURL"} {
		if v := env[key]; v != "" {
			return v
		}
	}
	t.Fatal("tests need a MySQL server: set DatabaseURL (or TestDatabaseURL) in .env or the environment")
	return ""
}

// projectRoot is the nearest directory above the working directory that has a go.mod.
func projectRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

func randomHex(t testing.TB) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
