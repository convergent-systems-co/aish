# libs/proto — Makefile
#
# The Brand-Atoms shell-brand schema published as a reusable Go module.
# Build/test targets mirror the shell module's shape so the top-level
# Makefile orchestrator can delegate uniformly.

MODULE  := github.com/convergent-systems-co/aish/libs/proto
PKG_ALL := ./...
GO      ?= go
GOFLAGS ?=

.DEFAULT_GOAL := help

.PHONY: build
build: ## Verify the module compiles (no binary)
	$(GO) build $(GOFLAGS) $(PKG_ALL)

.PHONY: build-all
build-all: build ## Alias for build (no per-arch binaries for a lib module)

.PHONY: test
test: ## Run all unit tests with race detector
	$(GO) test $(GOFLAGS) -race -count=1 $(PKG_ALL)

.PHONY: lint
lint: ## go vet
	$(GO) vet $(PKG_ALL)

.PHONY: fmt
fmt: ## gofmt -w
	$(GO) fmt $(PKG_ALL)

.PHONY: tidy
tidy: ## go mod tidy + verify
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: ci
ci: tidy fmt lint test ## Full pre-merge gate for this module
	@echo "✓ libs/proto CI gate passed"

.PHONY: clean
clean: ## Nothing to clean (no build artifacts)
	@true

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS = ":.*?## "; printf "libs/proto targets:\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
