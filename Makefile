# Developer entry points. `make help` lists them. Tool versions live in
# .mise.toml; the pre-tag gate is scripts/gate.sh (`make gate`).

GO       ?= go
LINT     ?= golangci-lint
BIN      ?= bin
BASE     ?= main
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X github.com/noviopenworks/homonto/internal/cli.Version=$(VERSION) \
            -X github.com/noviopenworks/homonto/internal/ontocli.Version=$(VERSION) \
            -X github.com/noviopenworks/homonto/internal/tocli.Version=$(VERSION)

# Go files changed on this branch plus uncommitted work, relative to $(BASE).
CHANGED_GO = $(shell { git diff --name-only $(BASE)...HEAD; git diff --name-only HEAD; \
               git ls-files --others --exclude-standard; } 2>/dev/null | grep '\.go$$' | sort -u)

.DEFAULT_GOAL := help
.PHONY: help build install fmt vet lint tidy test race test-changed cover check \
        docs-check vuln e2e gate index affected clean

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-13s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build homonto, onto, and to into ./bin (version-stamped)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/homonto .
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/onto ./cmd/onto
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/to ./cmd/to

install: ## go install all three binaries (version-stamped)
	$(GO) install -ldflags "$(LDFLAGS)" . ./cmd/onto ./cmd/to

fmt: ## Format Go sources in place
	gofmt -w .

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint (config: .golangci.yml)
	$(LINT) run ./...

tidy: ## go mod tidy
	$(GO) mod tidy

test: ## Unit tests
	$(GO) test ./...

race: ## Unit tests under the race detector
	$(GO) test -race ./... -timeout=20m

test-changed: ## Test only the packages with Go changes vs $(BASE)
	@pkgs="$$(for f in $(CHANGED_GO); do [ -e "$$f" ] && echo ./$$(dirname $$f); done | sort -u)"; \
	if [ -z "$$pkgs" ]; then echo "no changed Go packages"; else echo "$$pkgs" | xargs $(GO) test; fi

cover: ## Coverage profile to cover.out, then print the total
	$(GO) test -coverprofile=cover.out ./... >/dev/null
	$(GO) tool cover -func=cover.out | tail -1

check: ## Fast pre-push check: gofmt, vet, lint, docs, tests
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run: make fmt"; exit 1; }
	$(GO) vet ./...
	$(LINT) run ./...
	./scripts/agents-doc-check.sh
	$(GO) test ./...

docs-check: ## Agent docs and catalog reference only things that exist
	./scripts/agents-doc-check.sh

vuln: ## govulncheck under the pinned toolchain
	GOTOOLCHAIN=$$(sed -n 's/^toolchain //p' go.mod) $(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

e2e: ## Triple-binary Docker E2E suites (needs Docker)
	./scripts/docker-test.sh

gate: ## The full pre-tag gate, identical to CI (needs Docker; slow)
	./scripts/gate.sh

index: ## Build or refresh the CodeGraph index (ADR 0066)
	@if ls -A .codegraph 2>/dev/null | grep -qv '^\.gitignore$$'; \
	then codegraph sync; else codegraph init; fi

affected: ## Tests CodeGraph links to the Go files changed vs $(BASE)
	@if [ -z "$(CHANGED_GO)" ]; then echo "no changed Go files"; \
	else codegraph affected --quiet $(CHANGED_GO); fi

clean: ## Remove build and coverage output
	rm -rf $(BIN) dist cover.out
