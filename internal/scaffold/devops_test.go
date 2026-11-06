package scaffold

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

func TestDockerfile(t *testing.T) {
	tests := []struct {
		name          string
		edit          func(o *options.Options)
		want, notWant []string
	}{
		{"pure-Go SQLite", func(o *options.Options) {},
			[]string{"FROM " + goImage + " AS build", "CGO_ENABLED=0", "FROM gcr.io/distroless/static-debian13:nonroot", "VOLUME /data", "DatabasePath=/data/app.db", "USER 65532:65532", tailwindSHA256X64, tailwindSHA256Arm64},
			nil},
		{"CGO SQLite", func(o *options.Options) { o.SQLiteDriver = options.CGO },
			[]string{"CGO_ENABLED=1", "FROM gcr.io/distroless/base-debian13:nonroot", "VOLUME /data"},
			[]string{"static-debian13"}},
		{"Postgres", func(o *options.Options) { o.Database = options.Postgres },
			[]string{"CGO_ENABLED=0", "static-debian13", "-e DatabaseURL='...'"},
			[]string{"VOLUME", "DatabasePath"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := testOptions()
			tt.edit(&o)
			p := mustBuild(t, o, "/work/demo-app")
			for _, w := range tt.want {
				mustContain(t, p, "Dockerfile", w)
			}
			for _, w := range tt.notWant {
				if strings.Contains(string(p["Dockerfile"]), w) {
					t.Errorf("Dockerfile contains %q", w)
				}
			}
			mustContain(t, p, ".dockerignore", "\n.env\n")
			mustContain(t, p, "Makefile", "\tdocker build -t demo-app .\n")
			mustContain(t, p, "Makefile", " check clean docker\n")
			mustContain(t, p, "README.md", "### Docker")
			mustContain(t, p, "AGENTS.md", "- Deploy and CI: `Dockerfile`")
		})
	}

	o := testOptions()
	o.Docker = false
	p := mustBuild(t, o, "/work/demo-app")
	for _, f := range []string{"Dockerfile", ".dockerignore"} {
		if _, ok := p[f]; ok {
			t.Errorf("%s written with --docker=false", f)
		}
	}
	if strings.Contains(string(p["Makefile"]), "docker") {
		t.Error("Makefile has a docker target with --docker=false")
	}
}

var pinnedAction = regexp.MustCompile(`uses: [\w-]+/[\w-]+@[0-9a-f]{40} # v\d+\.\d+\.\d+\n`)

func TestWorkflow(t *testing.T) {
	ci := func(edit func(o *options.Options)) string {
		o := testOptions()
		edit(&o)
		return string(mustBuild(t, o, "/work/demo-app")[".github/workflows/ci.yml"])
	}

	w := ci(func(o *options.Options) {})
	for _, want := range []string{"permissions:\n  contents: read\n", "persist-credentials: false", "go-version-file: go.mod", "go mod verify", "git diff --exit-code", "make vet", "make test", "make vuln", "playwright install --with-deps chromium", "\n  docker:\n", tailwindSHA256X64} {
		if !strings.Contains(w, want) {
			t.Errorf("workflow is missing %q", want)
		}
	}
	for _, line := range strings.Split(w, "\n") {
		if strings.Contains(line, "uses:") && !pinnedAction.MatchString(strings.TrimSpace(line)+"\n") {
			t.Errorf("action not pinned to a commit: %s", line)
		}
	}
	if strings.Contains(w, "services:") {
		t.Error("SQLite workflow has a database service")
	}

	if w := ci(func(o *options.Options) { o.Database = options.Postgres }); !strings.Contains(w, "image: "+postgresImage) || !strings.Contains(w, "TestDatabaseURL: postgres://") {
		t.Error("Postgres workflow has no Postgres service")
	}
	if w := ci(func(o *options.Options) { o.Database = options.MySQL }); !strings.Contains(w, "image: "+mysqlImage) || !strings.Contains(w, "TestDatabaseURL: root:root@tcp(127.0.0.1:3306)/") {
		t.Error("MySQL workflow has no MySQL service")
	}
	if w := ci(func(o *options.Options) { o.E2E, o.Axe = false, false }); strings.Contains(w, "playwright") {
		t.Error("workflow installs a browser without the E2E tests")
	}
	if w := ci(func(o *options.Options) { o.Docker = false }); strings.Contains(w, "docker build") {
		t.Error("workflow builds an image without a Dockerfile")
	}
	if w := ci(func(o *options.Options) { o.CommitJS = false }); !strings.Contains(w, "run: ./download-js.sh") {
		t.Error("docker job can't build without committed JS")
	}

	o := testOptions()
	o.CI = false
	if _, ok := mustBuild(t, o, "/work/demo-app")[".github/workflows/ci.yml"]; ok {
		t.Error("workflow written with --ci=false")
	}
}

func TestImageName(t *testing.T) {
	for in, want := range map[string]string{"my-app": "my-app", "Bob's App": "bob-s-app", "---": "app", "a.b_c": "a.b_c"} {
		if got := ImageName(in); got != want {
			t.Errorf("ImageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRepoWorkflowUsesPins: this repository's own CI uses the same pinned actions, images and
// Tailwind build as the projects it generates, so make update-deps (pins.go) and the workflow
// can't drift apart unnoticed.
func TestRepoWorkflowUsesPins(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	w := string(b)
	for _, want := range []string{
		"uses: " + checkoutAction,
		"uses: " + setupGoAction,
		"image: " + postgresImage,
		"image: " + mysqlImage,
		"/download/" + tailwindVersion + "/tailwindcss-linux-x64",
		tailwindSHA256X64,
	} {
		if !strings.Contains(w, want) {
			t.Errorf(".github/workflows/ci.yml doesn't use %q (internal/scaffold/pins.go): update the workflow", want)
		}
	}
	for _, line := range strings.Split(w, "\n") {
		if strings.Contains(line, "uses:") && !pinnedAction.MatchString(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))+"\n") {
			t.Errorf("action not pinned to a commit: %s", line)
		}
	}
}
