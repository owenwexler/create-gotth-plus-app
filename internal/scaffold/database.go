package scaffold

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// replacedFiles are template files that a database variant replaces completely, with the sha256 of
// the template version each replacement was written against. When gotth-plus changes one of them,
// generation fails until the replacement has been reviewed and the hash updated.
var replacedFiles = map[string]string{
	"internal/database/database.go": "5a9d9edb553e97024c64ae070dea83eb50d3dafdef01be5e86021621c2e62bff",
	"air.toml":                      "97d30276e3a1e649d03f82c483c8e18478821fbd23a83917dd991ebbf5d23579",
	"internal/app/app.go":           "df426233cacb86db8fe80499978f38ba38d7f0f690669925b367ea436784d368",
	"internal/view/pages.templ":     "1cbb98dfb1c14e4717c7fd6f4441ff9fbf42bf22e211fdc816ce1104100ecd56",
	"internal/view/pages_templ.go":  "7ea89245860b9237cbff19e4500987f16ae9014aff7737c461a32eafd8e83f05",
}

// DepsKey names the go.mod/go.sum pair in _deps for o: the choices that change the dependencies.
func DepsKey(o options.Options) string {
	key := "sqlite-cgo"
	switch {
	case o.Database.IsServer():
		key = string(o.Database)
	case o.SQLiteDriver == options.PureGo:
		key = "sqlite-purego"
	}
	if o.HasAuth() {
		key += "-auth" // scs
		if o.HasMethod(options.Password) {
			key += "-password"
		}
		if o.HasOAuth() {
			key += "-oauth" // x/oauth2 and go-oidc, whichever providers
		}
	}
	if !o.E2E {
		key += "-noe2e" // no playwright-go
	}
	return key
}

// DepsChoices returns one Options for each go.mod/go.sum pair in _deps.
func DepsChoices() []options.Options {
	var all []options.Options
	for _, db := range []struct {
		db     options.Database
		driver options.SQLiteDriver
	}{
		{options.SQLite, options.CGO},
		{options.SQLite, options.PureGo},
		{options.Postgres, options.PureGo},
		{options.MySQL, options.PureGo},
	} {
		// no auth, then each set of sign-in methods with its own dependencies
		authChoices := [][]options.AuthMethod{nil, {options.Password}, {options.Google, options.GitHub, options.Apple}, options.AuthMethodList}
		for _, methods := range authChoices {
			for _, e2e := range []bool{true, false} {
				o := options.Defaults()
				o.Database, o.SQLiteDriver, o.E2E = db.db, db.driver, e2e
				o.Axe = e2e
				if methods != nil {
					o.Auth, o.AuthMethods = options.BuiltinAuth, methods
				}
				all = append(all, o)
			}
		}
	}
	return all
}

// applyDatabase adjusts the project to the chosen database. It runs before the module is renamed,
// so it sees the template's own import paths. dbName is the database name for DatabaseURL.
func applyDatabase(p Project, o options.Options, templateModule, dbName string) error {
	switch {
	case o.Database.IsServer():
		return serverDatabase(p, o.Database, templateModule, dbName)
	case o.SQLiteDriver == options.PureGo:
		return pureGoSQLite(p)
	}
	return nil // CGO SQLite is the template as it is
}

// pureGoSQLite swaps mattn/go-sqlite3 for glebarez/sqlite. Both are GORM drivers in a package named
// sqlite with the same Open function, so only the import and the docs change.
func pureGoSQLite(p Project) error {
	return p.edits([]edit{
		{"internal/database/database.go", "\t\"gorm.io/driver/sqlite\"\n", "\t\"github.com/glebarez/sqlite\"\n"},
		{"README.md", " and a C compiler (the SQLite driver uses CGO)", " (the SQLite driver is pure Go, so no C compiler is needed)"},
		{"AGENTS.md", "(`gorm.io/driver/sqlite`, CGO)", "(`github.com/glebarez/sqlite`, pure Go: no CGO, builds with `CGO_ENABLED=0`)"},
		{"SECURITY.md",
			"- `mattn/go-sqlite3` is a C library. Keep it updated and watch `govulncheck` for it.\n",
			"- The SQLite driver (`glebarez/sqlite` on `glebarez/go-sqlite`) is SQLite translated to Go, not the C\n" +
				"  library. SQLite's security fixes reach it only when it is retranslated, so keep it updated and\n" +
				"  watch `govulncheck` for it.\n"},
		{"SECURITY.md",
			"- `mattn/go-sqlite3` needs CGO, so the binary is not fully static. Use a base image with a\n" +
				"  compatible libc, or switch to `modernc.org/sqlite` (pure Go) if you want `CGO_ENABLED=0`.\n",
			"- The SQLite driver is pure Go, so the binary builds with `CGO_ENABLED=0` and runs on a\n" +
				"  distroless or scratch image.\n"},
	})
}

// server holds the text that differs between Postgres and MySQL.
type server struct {
	label      string // "Postgres"
	urlKind    string // "URL" or "DSN"
	idType     string // GORM column type for uuid.UUID
	driver     string // GORM driver module, for the docs
	exampleURL string // DatabaseURL in .env.example, with %s for the database name
	envComment string // comment above DatabaseURL in .env.example
	testPrivs  string // privileges the test user needs
	createDB   string // command that creates the development database, with %s for its name
	tlsHint    string // how to turn on TLS
}

var servers = map[options.Database]server{
	options.Postgres: {
		label:      "Postgres",
		urlKind:    "URL",
		idType:     "uuid",
		driver:     "gorm.io/driver/postgres",
		exampleURL: "postgres://localhost:5432/%s?sslmode=disable",
		envComment: "# Postgres connection URL: a local server or a cloud database. Without a user name, your OS user\n" +
			"# name is used. Anything that isn't localhost should use sslmode=verify-full.\n",
		testPrivs: "`CREATEDB`",
		createDB:  "createdb %s",
		tlsHint:   "`sslmode=verify-full`, with the server's CA certificate",
	},
	options.MySQL: {
		label:      "MySQL",
		urlKind:    "DSN",
		idType:     "char(36)",
		driver:     "gorm.io/driver/mysql",
		exampleURL: "root@tcp(localhost:3306)/%s?parseTime=true&charset=utf8mb4&loc=UTC",
		envComment: "# MySQL connection in go-sql-driver/mysql's DSN format (user:password@tcp(host:port)/database?...),\n" +
			"# for a local server or a cloud database. parseTime=true is required. Anything that isn't\n" +
			"# localhost should add tls=true.\n",
		testPrivs: "`CREATE` and `DROP`",
		createDB:  "mysql -u root -e 'CREATE DATABASE %s CHARACTER SET utf8mb4'",
		tlsHint:   "`tls=true`, or a TLS config registered with `mysql.RegisterTLSConfig`",
	},
}

// CreateDatabaseCommand is the command that creates a project's development database, or "" for
// SQLite (the file is created on first use).
func CreateDatabaseCommand(d options.Database, dbName string) string {
	s, ok := servers[d]
	if !ok {
		return ""
	}
	return fmt.Sprintf(s.createDB, dbName)
}

func serverDatabase(p Project, d options.Database, mod, dbName string) error {
	s := servers[d]

	// database.go is replaced, and dbtest added, from _variants/<database>
	if _, err := p.overlay(string(d)); err != nil {
		return err
	}

	envBlock := s.envComment +
		"DatabaseURL=" + fmt.Sprintf(s.exampleURL, dbName) + "\n\n" +
		"# Optional. make test creates a throwaway database for each test on this server and drops it\n" +
		"# afterwards, so the user needs " + strings.Trim(s.testPrivs, "`") + ". Defaults to DatabaseURL. Never point it at production.\n" +
		"TestDatabaseURL=\n"

	readmeSection := "### Database\n\n" +
		"The app uses " + s.label + ". Point `DatabaseURL` in `.env` at a local server or a cloud database, and create the\n" +
		"database first:\n\n" +
		"```bash\n" + fmt.Sprintf(s.createDB, dbName) + "\n```\n\n" +
		"`make seed` creates the tables (`AutoMigrate`, development only) and loads `seedData.json`.\n\n" +
		"The tests need a running " + s.label + " server too. Each test creates its own throwaway database on the\n" +
		"server in `TestDatabaseURL` (or `DatabaseURL` when that is empty) and drops it afterwards, so that\n" +
		"user needs " + s.testPrivs + ". Never point it at a production server.\n\n"

	if d == options.MySQL {
		if err := mysqlLikeEdits(p); err != nil {
			return err
		}
	}

	return p.edits([]edit{
		// configuration: a required connection string instead of an optional file path
		{"internal/config/config.go",
			"\t// DatabasePath is the SQLite file. Defaults to app.db.\n\tDatabasePath string\n",
			"\t// DatabaseURL is the " + s.label + " connection " + s.urlKind + ". Required.\n\tDatabaseURL string\n"},
		{"internal/config/config.go", "\t\tDatabasePath: os.Getenv(\"DatabasePath\"),\n", "\t\tDatabaseURL: os.Getenv(\"DatabaseURL\"),\n"},
		{"internal/config/config.go",
			"\tif env.DatabasePath == \"\" {\n\t\tenv.DatabasePath = \"app.db\"\n\t}\n",
			"\tif env.DatabaseURL == \"\" {\n\t\tpanic(\"DatabaseURL must be set\")\n\t}\n"},
		{"cmd/server/main.go", "database.ConnectDb(env.DatabasePath)", "database.ConnectDb(env.DatabaseURL)"},
		{".env.example", "# Optional. SQLite database file; defaults to app.db.\nDatabasePath=app.db\n", envBlock},
		{".gitignore", "\n# DB\n*.db\n*.db-shm\n*.db-wal\n", ""},

		// uuid.UUID has no column type of its own; SQLite takes anything, servers need one
		{"internal/model/model.go", "\tID        uuid.UUID\n", "\tID        uuid.UUID `gorm:\"type:" + s.idType + "\"`\n"},

		// tests: a throwaway database on the server instead of a temporary SQLite file
		{"cmd/server/testutil_test.go",
			"\tdb, err := database.ConnectDb(filepath.Join(t.TempDir(), \"test.db\"))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tt.Cleanup(func() { closeDB(db) })\n",
			"\t// a fresh database on the test server, dropped when the test ends\n\tdb := dbtest.Open(t)\n"},
		{"cmd/server/testutil_test.go", "\"" + mod + "/internal/database\"\n", "\"" + mod + "/internal/database/dbtest\"\n"},
		{"internal/repository/repository_test.go",
			"\tdb, err := database.ConnectDb(filepath.Join(t.TempDir(), \"test.db\"))\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n",
			"\tdb := dbtest.Open(t)\n"},
		{"internal/repository/repository_test.go", "\t\"path/filepath\"\n", ""},
		{"internal/repository/repository_test.go", "\"" + mod + "/internal/database\"\n", "\"" + mod + "/internal/database/dbtest\"\n"},

		// docs
		{"README.md", "on SQLite and GORM", "on " + s.label + " and GORM"},
		{"README.md", " and a C compiler (the SQLite driver uses CGO)", ", and a " + s.label + " server (local or in the cloud)"},
		{"README.md", "  database/         SQLite connection (GORM)\n", "  database/         " + s.label + " connection pool (GORM); dbtest/ makes throwaway test databases\n"},
		{"README.md", "throwaway SQLite\n", "throwaway " + s.label + "\n"},
		{"README.md", "### The JavaScript libraries\n", readmeSection + "### The JavaScript libraries\n"},
		{"AGENTS.md", "`DatabasePath`, `SeedAdminKey`", "`DatabaseURL`, `TestDatabaseURL`, `SeedAdminKey`"},
		{"AGENTS.md",
			"- Data: SQLite (`app.db`) via GORM (`gorm.io/driver/sqlite`, CGO).",
			"- Data: " + s.label + " via GORM (`" + s.driver + "`); the pool is configured in `internal/database`. `DatabaseURL` is required."},
		{"AGENTS.md", "on a throwaway SQLite file;",
			"on a throwaway " + s.label + " database (`internal/database/dbtest`: one per test, created on `TestDatabaseURL` or `DatabaseURL` and dropped afterwards, so a " + s.label + " server must be running);"},
		{"SECURITY.md", "### Data: SQLite and GORM", "### Data: " + s.label + " and GORM"},
		{"SECURITY.md",
			"- `mattn/go-sqlite3` is a C library. Keep it updated and watch `govulncheck` for it.\n",
			"- **Connect over TLS** to any server that isn't localhost: " + s.tlsHint + ". Plain connections are\n" +
				"  only for a local server.\n" +
				"- **Least privilege:** the app logs in as its own user with only the privileges it needs, never\n" +
				"  as a superuser or root. The tests need " + s.testPrivs + "; keep that user away from production.\n"},
		{"SECURITY.md",
			"- [ ] `DatabasePath` points outside `static/` and outside any directory the web server serves.\n" +
				"      The database file and its `-wal`/`-shm` files are readable only by the app's user (`chmod 600`).\n",
			"- [ ] `DatabaseURL` comes from the secret store, connects over TLS, and logs in as a user with\n" +
				"      only the privileges the app needs. `TestDatabaseURL` is not set in production.\n"},
		{"SECURITY.md",
			"- `mattn/go-sqlite3` needs CGO, so the binary is not fully static. Use a base image with a\n" +
				"  compatible libc, or switch to `modernc.org/sqlite` (pure Go) if you want `CGO_ENABLED=0`.\n" +
				"- Read-only root filesystem, with a single writable volume for the SQLite file.\n",
			"- The " + s.label + " driver is pure Go, so the binary builds with `CGO_ENABLED=0` and runs on a\n" +
				"  distroless or scratch image.\n" +
				"- Read-only root filesystem: the data lives in " + s.label + ", so the app needs no writable volume.\n"},
	})
}

// mysqlLikeEdits: MySQL already uses \ as its LIKE escape character, and in MySQL's string syntax
// ESCAPE '\' is not even valid SQL, so the advice to add that clause is changed.
func mysqlLikeEdits(p Project) error {
	return p.edits([]edit{
		{"internal/escaper/escaper.go", "Pair it with `ESCAPE '\\'` in the SQL.", "MySQL uses it by default, so MySQL queries need no ESCAPE clause."},
		{"internal/escaper/escaper.go", "The query must declare `ESCAPE '\\'`.", "MySQL's default LIKE escape is \\, so no ESCAPE clause is needed."},
		{"SECURITY.md", "with `escaper.Like` and `ESCAPE '\\'`,", "with `escaper.Like` (MySQL's default LIKE escape is `\\`, so add no `ESCAPE` clause),"},
	})
}

// edit is one replaceOnce.
type edit struct{ file, old, new string }

func (p Project) edits(es []edit) error {
	for _, e := range es {
		if err := p.replaceOnce(e.file, e.old, e.new); err != nil {
			return err
		}
	}
	return nil
}

// overlay copies _variants/<name> over the project and returns the paths it wrote. A file that
// replaces a template file must be listed in replacedFiles, and the template file must still have
// the recorded hash.
func (p Project) overlay(name string) ([]string, error) {
	root := "_variants/" + name
	var written []string
	err := fs.WalkDir(embedded, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, root+"/")
		if _, exists := p[rel]; exists {
			if err := p.checkReplaceable(rel); err != nil {
				return fmt.Errorf("variant %s: %w", name, err)
			}
		}
		b, err := fs.ReadFile(embedded, path)
		if err != nil {
			return err
		}
		p[rel] = b
		written = append(written, rel)
		return nil
	})
	return written, err
}
