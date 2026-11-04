# create-gotth-plus-app - run `make help` for the list of targets.

.DEFAULT_GOAL := help
.PHONY: help build test test-short vet fmt sync-template try update-deps

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the CLI into ./bin
	go build -o ./bin/create-gotth-plus-app .

test: ## All tests, including generating a project and running go build/vet/test on it
	go test -race -count=1 ./...

test-short: ## Fast tests only (skips building a generated project)
	go test -short -count=1 ./...

vet: ## Vet the CLI
	go vet ./...

fmt: ## Format the CLI's Go files (the template is left alone)
	gofmt -w main.go scripts internal/cli internal/options internal/post internal/prereq internal/scaffold/*.go

sync-template: ## Copy ../gotth-plus (committed files at HEAD) into the embedded template
	./scripts/sync-template.sh

try: build ## Generate ./tmp/demo-app with defaults, to look at the output
	rm -rf tmp/demo-app && mkdir -p tmp && cd tmp && ../bin/create-gotth-plus-app --yes demo-app

update-deps: ## Refresh internal/scaffold/pins.go and _deps (go.mod/go.sum per choice); needs network
	go run ./scripts/update-deps
