# aish — Makefile
#
# Drives builds, tests, lint, cross-compile releases, and /spawn board ops.
# Run `make help` to list targets.

# ---- Project metadata ----
MODULE      := github.com/convergent-systems-co/aish
BINARY      := aish
CMD         := ./cmd/$(BINARY)
PKG_ALL     := ./...
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

# ---- Cross-compile matrix ----
DIST        := dist
PLATFORMS   := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

# ---- Tooling ----
GO          ?= go
GOFLAGS     ?=

.DEFAULT_GOAL := help

# ---- Build ----
.PHONY: build
build: ## Build aish for the host platform into dist/aish
	@mkdir -p $(DIST)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)
	@echo "→ $(DIST)/$(BINARY) ($(VERSION))"

.PHONY: build-all
build-all: ## Cross-compile aish for darwin/linux × amd64/arm64
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$(DIST)/$(BINARY)-$$os-$$arch; \
		echo "→ $$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD) || exit 1; \
	done

.PHONY: install
install: ## Install aish into $$GOBIN (or GOPATH/bin)
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD)

.PHONY: run
run: build ## Build then run aish (dev iteration)
	$(DIST)/$(BINARY)

# ---- Quality ----
.PHONY: test
test: ## Run all unit tests with race detector
	$(GO) test $(GOFLAGS) -race -count=1 $(PKG_ALL)

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	$(GO) test $(GOFLAGS) -race -count=1 -coverprofile=coverage.out $(PKG_ALL)
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## go vet + (optional) golangci-lint
	$(GO) vet $(PKG_ALL)
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "(golangci-lint not installed — skipping)"

.PHONY: fmt
fmt: ## Format Go source
	$(GO) fmt $(PKG_ALL)
	@command -v goimports >/dev/null 2>&1 && goimports -w . || true

.PHONY: tidy
tidy: ## go mod tidy + verify
	$(GO) mod tidy
	$(GO) mod verify

# ---- CI / PR gate ----
.PHONY: ci
ci: tidy fmt lint test ## Full pre-merge gate
	@echo "✓ CI gate passed"

.PHONY: pr-ready
pr-ready: ci ## Alias for ci; run before opening a PR
	@echo "Ready to open PR."

# ---- Release ----
.PHONY: release
release: clean build-all ## Cross-compile binaries + SHA256 sums into dist/
	@cd $(DIST) && \
		for f in $(BINARY)-*; do \
			(shasum -a 256 "$$f" 2>/dev/null || sha256sum "$$f") > "$$f.sha256"; \
		done
	@echo "→ release artifacts in $(DIST)/"
	@ls -lh $(DIST)/

# ---- /spawn board helpers ----
.PHONY: spawn-available
spawn-available: ## List Backlog issues (Pipeline lock filter)
	@python3 .artifacts/spawn/board.py available -v 2>/dev/null || \
		echo "board.py not configured (run .artifacts/spawn/setup-project.py first)"

.PHONY: spawn-status
spawn-status: ## Print Pipeline state of an issue: make spawn-status ISSUE=42
	@test -n "$(ISSUE)" || (echo "usage: make spawn-status ISSUE=<n>" && exit 2)
	@python3 .artifacts/spawn/board.py status $(ISSUE)

.PHONY: spawn-claim
spawn-claim: ## Claim Backlog issue -> In Plan: make spawn-claim ISSUE=42
	@test -n "$(ISSUE)" || (echo "usage: make spawn-claim ISSUE=<n>" && exit 2)
	@python3 .artifacts/spawn/board.py claim $(ISSUE)

.PHONY: spawn-transition
spawn-transition: ## Advance Pipeline: make spawn-transition ISSUE=42 TO=Coding
	@test -n "$(ISSUE)" || (echo "usage: make spawn-transition ISSUE=<n> TO=<state>" && exit 2)
	@test -n "$(TO)" || (echo "usage: make spawn-transition ISSUE=<n> TO=<state>" && exit 2)
	@python3 .artifacts/spawn/board.py transition $(ISSUE) --to "$(TO)"

.PHONY: spawn-release
spawn-release: ## Reset Pipeline to Backlog: make spawn-release ISSUE=42
	@test -n "$(ISSUE)" || (echo "usage: make spawn-release ISSUE=<n>" && exit 2)
	@python3 .artifacts/spawn/board.py release $(ISSUE)

# ---- Housekeeping ----
.PHONY: clean
clean: ## Remove build artifacts and coverage files
	rm -rf $(DIST) coverage.out

.PHONY: help
help: ## List Makefile targets
	@awk 'BEGIN {FS = ":.*?## "; printf "Targets (run \"make <target>\"):\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
