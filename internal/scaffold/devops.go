package scaffold

import (
	"fmt"
	"strings"

	"github.com/owenwexler/create-gotth-plus-app/internal/options"
)

// ImageName turns a project name into a Docker image name: lowercase letters, digits, '.', '_' and '-'.
func ImageName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if n := strings.Trim(b.String(), ".-_"); n != "" {
		return n
	}
	return "app"
}

// dockerfile is a multi-stage build: the CSS and the server are built in the Go image, then copied
// into a distroless image that runs as the non-root user (SECURITY.md, "Containers and hosting").
func dockerfile(o options.Options, image string) string {
	cgo := o.NeedsCCompiler()
	sqlite := o.Database == options.SQLite

	runtime := "gcr.io/distroless/static-debian13:nonroot"
	cgoEnabled := "0"
	if cgo {
		// mattn/go-sqlite3 links against glibc, so the image needs it
		runtime = "gcr.io/distroless/base-debian13:nonroot"
		cgoEnabled = "1"
	}

	var b strings.Builder
	b.WriteString(`# syntax=docker/dockerfile:1
# Production image. The CSS and the server are built in the Go image, then copied into a minimal
# distroless image that runs as a non-root user.
#
#   make docker            # docker build -t ` + image + ` .
`)
	if sqlite {
		b.WriteString("#   docker run --rm -p 8080:8080 -v " + image + "-data:/data " + image + "\n")
	} else {
		b.WriteString("#   docker run --rm -p 8080:8080 -e DatabaseURL='...' " + image + "\n")
	}
	fmt.Fprintf(&b, `
FROM %s AS build
WORKDIR /src

# The Tailwind CSS standalone CLI, pinned and checked against its release checksum.
RUN set -eu; \
    case "$(uname -m)" in \
      x86_64) arch=x64; sum=%s ;; \
      aarch64) arch=arm64; sum=%s ;; \
      *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /usr/local/bin/tailwindcss \
      "https://github.com/tailwindlabs/tailwindcss/releases/download/%s/tailwindcss-linux-${arch}"; \
    echo "${sum}  /usr/local/bin/tailwindcss" | sha256sum -c -; \
    chmod +x /usr/local/bin/tailwindcss

COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Production serves the committed, reviewed copies of the browser libraries and never downloads
# them (SECURITY.md, "Frontend assets").
RUN for f in htmx.min.js alpine-csp.min.js hx-optimistic.min.js; do \
      test -f "static/js/$f" || { echo "static/js/$f is missing: run make js, review and commit static/js" >&2; exit 1; }; \
    done

RUN tailwindcss -i static/input.css -o static/output.css --minify
RUN CGO_ENABLED=%s go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
`, goImage, tailwindSHA256X64, tailwindSHA256Arm64, tailwindVersion, cgoEnabled)
	if sqlite {
		b.WriteString("# the SQLite database lives on a volume, so the rest of the filesystem can be read-only\nRUN mkdir -p /out/data\n")
	}

	fmt.Fprintf(&b, `
FROM %s
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /src/static /app/static
`, runtime)
	if sqlite {
		b.WriteString("COPY --from=build --chown=65532:65532 /out/data /data\n")
		b.WriteString("ENV GoEnv=production Port=8080 DatabasePath=/data/app.db\nVOLUME /data\n")
	} else {
		b.WriteString("# DatabaseURL comes from the platform's secret store at runtime, never from the image\n")
		b.WriteString("ENV GoEnv=production Port=8080\n")
	}
	b.WriteString(`EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
`)
	return b.String()
}

const dockerignore = `# Keep secrets, local data and build output out of the image's build context.
.env
.env.*
!.env.example
.git
tmp/
*.db
*.db-shm
*.db-wal
static/output.css
cmd/server/testdata/
`

// workflow is the GitHub Actions CI: what make check runs, plus checks that generated code and
// formatting are committed, that dependencies are verified, and that there are no known
// vulnerabilities. Actions are pinned by commit.
func workflow(o options.Options) string {
	var b strings.Builder
	b.WriteString(`name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest
    timeout-minutes: 30
`)
	switch o.Database {
	case options.Postgres:
		fmt.Fprintf(&b, `    services:
      postgres:
        image: %s
        env:
          POSTGRES_PASSWORD: postgres
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U postgres"
          --health-interval 5s --health-timeout 5s --health-retries 20
    env:
      # each test creates and drops its own database on this server
      TestDatabaseURL: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
`, postgresImage)
	case options.MySQL:
		fmt.Fprintf(&b, `    services:
      mysql:
        image: %s
        env:
          MYSQL_ROOT_PASSWORD: root
        ports: ["3306:3306"]
        options: >-
          --health-cmd "mysqladmin ping -h 127.0.0.1 -proot"
          --health-interval 5s --health-timeout 5s --health-retries 30
    env:
      # each test creates and drops its own database on this server
      TestDatabaseURL: root:root@tcp(127.0.0.1:3306)/?parseTime=true&charset=utf8mb4&loc=UTC
`, mysqlImage)
	}

	fmt.Fprintf(&b, `    steps:
      - uses: %s
        with:
          persist-credentials: false
      - uses: %s
        with:
          go-version-file: go.mod

      - name: Install templ (the version in go.mod)
        run: go install "github.com/a-h/templ/cmd/templ@$(go list -m -f '{{.Version}}' github.com/a-h/templ)"
      - name: Install the Tailwind CSS standalone CLI
        run: |
          curl -fsSL -o "$RUNNER_TEMP/tailwindcss" \
            https://github.com/tailwindlabs/tailwindcss/releases/download/%s/tailwindcss-linux-x64
          echo "%s  $RUNNER_TEMP/tailwindcss" | sha256sum -c -
          chmod +x "$RUNNER_TEMP/tailwindcss"
          echo "$RUNNER_TEMP" >> "$GITHUB_PATH"

      - name: Dependencies match go.sum
        run: go mod verify
      - name: Generated code and formatting are committed
        run: |
          templ generate
          templ fmt .
          gofmt -w cmd internal
          git diff --exit-code
      - name: Vet
        run: make vet
`, checkoutAction, setupGoAction, tailwindVersion, tailwindSHA256X64)
	if o.E2E {
		b.WriteString(`      - name: JS libraries (downloaded only if they are not committed)
        run: test -f static/js/htmx.min.js || make js
      - name: Chromium for the E2E tests
        run: go run github.com/mxschmitt/playwright-go/cmd/playwright install --with-deps chromium
`)
	}
	b.WriteString(`      - name: Test
        run: make test
      - name: Known vulnerabilities
        run: make vuln
`)

	if o.Docker {
		fmt.Fprintf(&b, `
  docker:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: %s
        with:
          persist-credentials: false
`, checkoutAction)
		if !o.CommitJS {
			b.WriteString(`      # CI only checks that the image builds. Images you deploy must use committed, reviewed copies.
      - name: JS libraries (not committed in this project)
        run: ./download-js.sh
`)
		}
		b.WriteString(`      - name: The production image builds
        run: docker build -t ci-image .
`)
	}
	return b.String()
}

// applyDevOps adds the Dockerfile and the CI workflow, and says so in the docs.
func applyDevOps(p Project, o options.Options, name string) error {
	image := ImageName(name)
	if o.Docker {
		p["Dockerfile"] = []byte(dockerfile(o, image))
		p[".dockerignore"] = []byte(dockerignore)

		run := "docker run --rm -p 8080:8080 -v " + image + "-data:/data " + image
		data := "The SQLite database is on the `/data` volume (`DatabasePath=/data/app.db`); back the volume up."
		if o.Database.IsServer() {
			run = "docker run --rm -p 8080:8080 -e DatabaseURL='...' " + image
			data = "Pass `DatabaseURL` at runtime from the platform's secret store; it is never baked into the image."
		}
		section := "\n### Docker\n\n" +
			"```bash\nmake docker   # docker build -t " + image + " .\n" + run + "\n```\n\n" +
			"The `Dockerfile` builds the CSS and the server, then copies them into a distroless image that runs as\n" +
			"a non-root user with `GoEnv=production` on port 8080. " + data + "\n\n" +
			"The image creates no tables (`AutoMigrate` only runs in the development seed route), so run your\n" +
			"reviewed migrations before the first start. It also needs the JS libraries committed in `static/js`.\n" +
			"Fly.io, Railway, Render and most container platforms build this `Dockerfile` as it is; point their\n" +
			"health check at `/healthz`.\n"

		if err := p.edits([]edit{
			{"README.md", "- See `AGENTS.md` for the full conventions and security checklist.\n",
				"- See `AGENTS.md` for the full conventions and security checklist.\n" + section},
			{"SECURITY.md", "- Multi-stage build; final image distroless or minimal, running as a **non-root** user.\n",
				"- Multi-stage build; final image distroless or minimal, running as a **non-root** user. The\n  `Dockerfile` does this.\n"},
			{"Makefile", " check clean\n", " check clean docker\n"},
		}); err != nil {
			return err
		}
		p["Makefile"] = append(p["Makefile"], []byte("\ndocker: ## Build the production image (Dockerfile)\n\tdocker build -t "+image+" .\n")...)
	}

	if o.CI {
		p[".github/workflows/ci.yml"] = []byte(workflow(o))
	}

	var notes []string
	if o.Docker {
		notes = append(notes, "`Dockerfile` builds a distroless, non-root production image (`make docker`); it needs `static/js/*.min.js` committed.")
	}
	if o.CI {
		notes = append(notes, "`.github/workflows/ci.yml` runs `go mod verify`, checks that `templ generate`/`templ fmt`/`gofmt` change nothing, then `make vet`, `make test` and `make vuln`"+map[bool]string{true: ", and builds the Docker image.", false: "."}[o.Docker])
	}
	if len(notes) == 0 {
		return nil
	}
	return p.replaceOnce("AGENTS.md", "- Before finishing: `make check`",
		"- Deploy and CI: "+strings.Join(notes, " ")+"\n- Before finishing: `make check`")
}
