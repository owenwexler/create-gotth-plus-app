# GOTTH+ - run `make help` for the list of targets.

PLAYWRIGHT := go run github.com/mxschmitt/playwright-go/cmd/playwright

.DEFAULT_GOAL := help
.PHONY: help setup js generate css build run dev dev-templ dev-css air seed fmt vet test e2e-install vuln check clean

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

setup: ## First-time setup: .env, Go modules, the JS libraries, the test browser and a first build
	@test -f .env || (cp .env.example .env && echo "created .env from .env.example")
	go mod download
	$(MAKE) js generate css e2e-install

js: ## Download the latest htmx, its preload extension, Alpine (CSP build) and hx-optimistic into static/js
	./download-js.sh

generate: ## Regenerate *_templ.go from the .templ files
	templ generate

css: ## Build the minified Tailwind CSS
	tailwindcss -i static/input.css -o static/output.css --minify

build: generate css ## Build the server binary into ./tmp/main
	go build -o ./tmp/main ./cmd/server

run: generate css ## Run the server without live reload
	go run ./cmd/server

dev: ## Live reload: Tailwind and templ watchers; open the proxy at http://localhost:7331
	$(MAKE) -j2 dev-css dev-templ

dev-templ:
	templ generate --watch --cmd="go run ./cmd/server" --proxy="http://localhost:$$(sed -n 's/^Port=//p' .env | tr -d '"')"

dev-css:
	tailwindcss -i static/input.css -o static/output.css --watch

air: ## Alternative live reload with air (config in air.toml); run `make dev-css` beside it
	air

seed: ## Load seedData.json through the dev-only POST /seed route (the server must be running)
	./seed.sh

fmt: ## Format Go and templ files
	gofmt -w cmd internal
	templ fmt .

vet: ## Vet everything, including the e2e tests
	go vet -tags e2e ./...

# The e2e build tag adds the browser tests to the unit and integration tests, wherever they live, so this
# works for any layout. The browser tests need Chromium (`make e2e-install`, once); set AXE_CORE_JS to the
# path of an axe.min.js to include the accessibility scan.
test: generate css ## Unit, integration and end-to-end tests (needs `make js` and `make e2e-install` once)
	go test -race -count=1 -tags e2e ./...

e2e-install: ## Download the browser the end-to-end tests drive (once)
	$(PLAYWRIGHT) install chromium

vuln: ## Check dependencies and the standard library for known vulnerabilities
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

check: generate fmt vet test ## Everything that must pass before a commit

clean: ## Remove build output
	rm -rf tmp static/output.css

codegen:
	go run github.com/mxschmitt/playwright-go/cmd/playwright codegen "http://localhost:$$(sed -n 's/^Port=//p' .env | tr -d '"')"
