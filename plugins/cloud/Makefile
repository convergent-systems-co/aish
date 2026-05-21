# plugins/cloud — Makefile for the aish-inference-cloud + aish-plugin binaries.
#
# Build, test, lint, and release both binaries:
#   - aish-inference-cloud — the cloud inference plugin (v0.1-3+)
#   - aish-plugin          — the v0.3-2 admin CLI for the plugin registry
#
# Invoked by the top-level Makefile via `make -C plugins/cloud <target>`;
# can also be used standalone (`cd plugins/cloud && make test`).

MODULE        := github.com/convergent-systems-co/aish/plugins/cloud
BINARY        := aish-inference-cloud
BINARY_ADMIN  := aish-plugin
CMD           := ./cmd/$(BINARY)
CMD_ADMIN     := ./cmd/$(BINARY_ADMIN)
PKG_ALL       := ./...
VERSION       := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS       := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

DIST        := dist
PLATFORMS   := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

GO          ?= go
GOFLAGS     ?= -buildvcs=false

.DEFAULT_GOAL := help

# ---- Build ----
.PHONY: build
build: ## Build both plugin binaries for the host platform
	@mkdir -p $(DIST)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)
	@echo "→ plugins/cloud/$(DIST)/$(BINARY) ($(VERSION))"
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY_ADMIN) $(CMD_ADMIN)
	@echo "→ plugins/cloud/$(DIST)/$(BINARY_ADMIN) ($(VERSION))"

.PHONY: build-all
build-all: ## Cross-compile both binaries for all supported platforms
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		out=$(DIST)/$(BINARY)-$$os-$$arch$$ext; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD) || exit 1; \
		out=$(DIST)/$(BINARY_ADMIN)-$$os-$$arch$$ext; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD_ADMIN) || exit 1; \
	done

# ---- v1.0-1 Windows-only cross-compile (additive over build-all) ----
.PHONY: build-windows
build-windows: build-windows-amd64 build-windows-arm64 ## Cross-compile cloud binaries for windows/{amd64,arm64}

.PHONY: build-windows-amd64
build-windows-amd64: ## Cross-compile both cloud binaries for windows/amd64
	@mkdir -p $(DIST)
	@out=$(DIST)/$(BINARY)-windows-amd64.exe; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD)
	@out=$(DIST)/$(BINARY_ADMIN)-windows-amd64.exe; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD_ADMIN)

.PHONY: build-windows-arm64
build-windows-arm64: ## Cross-compile both cloud binaries for windows/arm64
	@mkdir -p $(DIST)
	@out=$(DIST)/$(BINARY)-windows-arm64.exe; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=windows GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD)
	@out=$(DIST)/$(BINARY_ADMIN)-windows-arm64.exe; \
		echo "→ plugins/cloud/$$out"; \
		GOOS=windows GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $$out $(CMD_ADMIN)

.PHONY: install
install: ## Install both binaries into $$GOBIN
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD)
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD_ADMIN)

# ---- Quality ----
.PHONY: test
test: ## Run unit tests with race detector
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

.PHONY: tidy
tidy: ## go mod tidy + verify
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: ci
ci: tidy fmt lint test ## Full pre-merge gate
	@echo "✓ plugins/cloud CI gate passed"

.PHONY: release
release: clean build-all ## Cross-compile + SHA256 sums into dist/
	@cd $(DIST) && \
		for f in $(BINARY)-* $(BINARY_ADMIN)-*; do \
			(shasum -a 256 "$$f" 2>/dev/null || sha256sum "$$f") > "$$f.sha256"; \
		done
	@ls -lh $(DIST)/

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(DIST) coverage.out

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS = ":.*?## "; printf "plugins/cloud targets:\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
