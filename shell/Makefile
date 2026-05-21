# shell/ — Makefile for the aish shell binary module.
#
# Build, test, lint, and release the `aish` binary. Invoked by the
# top-level Makefile via `make -C shell <target>`; can also be used
# standalone (`cd shell && make test`).

MODULE      := github.com/convergent-systems-co/aish/shell
BINARY      := aish
CMD         := ./cmd/$(BINARY)
PKG_ALL     := ./...
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

DIST        := dist
PLATFORMS   := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

GO          ?= go
GOFLAGS     ?=

.DEFAULT_GOAL := help

# ---- Build ----
.PHONY: build
build: ## Build aish for the host platform into shell/dist/aish
	@mkdir -p $(DIST)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)
	@echo "→ shell/$(DIST)/$(BINARY) ($(VERSION))"

.PHONY: build-all
build-all: ## Cross-compile aish for darwin/linux/windows × amd64/arm64
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out=$(DIST)/$(BINARY)-$$os-$$arch$$ext; \
		echo "→ shell/$$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD) || exit 1; \
	done

# ---- v1.0-1 Windows-only cross-compile (additive over build-all) ----
#
# These targets force `-buildvcs=false` so cross-compiles work from a
# git-worktree (where the .git pointer breaks Go's VCS stamping).
# Host builds via `build`/`build-all` keep the default behavior.
WIN_GOFLAGS := -buildvcs=false

.PHONY: build-windows
build-windows: build-windows-amd64 build-windows-arm64 ## Cross-compile aish for windows/{amd64,arm64}

.PHONY: build-windows-amd64
build-windows-amd64: ## Cross-compile aish for windows/amd64
	@mkdir -p $(DIST)
	@out=$(DIST)/$(BINARY)-windows-amd64.exe; \
		echo "→ shell/$$out"; \
		GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) $(WIN_GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD)

.PHONY: build-windows-arm64
build-windows-arm64: ## Cross-compile aish for windows/arm64
	@mkdir -p $(DIST)
	@out=$(DIST)/$(BINARY)-windows-arm64.exe; \
		echo "→ shell/$$out"; \
		GOOS=windows GOARCH=arm64 $(GO) build $(GOFLAGS) $(WIN_GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD)

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

.PHONY: test-integration
test-integration: ## Run end-to-end integration tests against the aish binary
	$(GO) test $(GOFLAGS) -race -count=1 ./integration/...

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

# ---- CI gate ----
.PHONY: ci
ci: tidy fmt lint test ## Full pre-merge gate for the shell module
	@echo "✓ shell CI gate passed"

.PHONY: release
release: clean build-all ## Cross-compile binaries + SHA256 sums into shell/dist/
	@cd $(DIST) && \
		for f in $(BINARY)-*; do \
			(shasum -a 256 "$$f" 2>/dev/null || sha256sum "$$f") > "$$f.sha256"; \
		done
	@echo "→ release artifacts in shell/$(DIST)/"
	@ls -lh $(DIST)/

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DIST) coverage.out

.PHONY: help
help: ## List shell-module targets
	@awk 'BEGIN {FS = ":.*?## "; printf "shell/ targets:\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
