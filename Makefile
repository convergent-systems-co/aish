# aish monorepo — top-level Makefile
#
# Orchestrates per-module builds across the Go workspace and exposes
# /spawn board helpers. Each subdir listed in MODULES has its own
# Makefile; targets delegate via `$(MAKE) -C <mod>`.
#
# Run `make help` to list targets.

# ---- Modules in this workspace ----
# Append modules to MODULES as they come online (each adds its own go.mod).
# Order chosen so module-graph dependencies test first (libs before consumers).
MODULES := libs/proto shell plugins/cloud
# Future additions:
#   term plugins/ollama plugins/wasm plugins/remote
#   libs/cache libs/history

.DEFAULT_GOAL := help

# ---- Per-module delegation ----
.PHONY: build
build: ## Build every module's binary
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m build || exit 1; done

.PHONY: build-all
build-all: ## Cross-compile every module for the full platform matrix
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m build-all || exit 1; done

.PHONY: test
test: ## Test every module
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m test || exit 1; done

.PHONY: lint
lint: ## Lint every module
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m lint || exit 1; done

.PHONY: fmt
fmt: ## gofmt every module
	@for m in $(MODULES); do $(MAKE) --no-print-directory -C $$m fmt; done

.PHONY: tidy
tidy: ## go mod tidy across the workspace
	go work sync
	@for m in $(MODULES); do $(MAKE) --no-print-directory -C $$m tidy; done

.PHONY: ci
ci: ## Full pre-merge gate across all modules
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m ci || exit 1; done
	@echo "✓ monorepo CI gate passed"

.PHONY: pr-ready
pr-ready: ci ## Alias for ci, run before opening a PR

.PHONY: clean
clean: ## Clean every module's build artifacts and the top-level dist/
	@for m in $(MODULES); do $(MAKE) --no-print-directory -C $$m clean || true; done
	rm -rf dist

.PHONY: release
release: ## Cross-compile every module's release bundle
	@for m in $(MODULES); do echo "→ $$m"; $(MAKE) --no-print-directory -C $$m release || exit 1; done

# ---- v0.2-3 community-cache bundle ----
BUNDLE_DIR     := dist/community
BUNDLE_VERSION ?= 1

.PHONY: bundle
bundle: ## Build + sign the v0.2-3 community-cache bundle from data/community/seed.jsonl
	@echo "→ building community-cache bundle v$(BUNDLE_VERSION)"
	@mkdir -p $(BUNDLE_DIR)
	@cd shell && go run ./cmd/aish-community build \
		-seed ../data/community/seed.jsonl \
		-out ../$(BUNDLE_DIR) \
		-trust-anchors ../data/community/trust-anchors.toml \
		-version $(BUNDLE_VERSION)
	@echo "→ packaging tarball"
	@tar -C $(BUNDLE_DIR) -czf $(BUNDLE_DIR)/aish-community-bundle-v$(BUNDLE_VERSION).tar.gz \
		manifest.json bundle.db trust-anchors.toml
	@(cd $(BUNDLE_DIR) && (shasum -a 256 aish-community-bundle-v$(BUNDLE_VERSION).tar.gz 2>/dev/null || \
		sha256sum aish-community-bundle-v$(BUNDLE_VERSION).tar.gz) > aish-community-bundle-v$(BUNDLE_VERSION).tar.gz.sha256)
	@echo "→ bundle artifacts in $(BUNDLE_DIR)/"
	@ls -lh $(BUNDLE_DIR)/

# ---- /spawn board helpers (monorepo-wide) ----
.PHONY: spawn-available
spawn-available: ## List Backlog tasks from the project board (Pipeline lock)
	@python3 .artifacts/spawn/board.py available -v 2>/dev/null || \
		echo "board.py not configured (run .artifacts/spawn/setup-project.py first)"

.PHONY: spawn-status
spawn-status: ## Print Pipeline state of an issue: make spawn-status ISSUE=42
	@test -n "$(ISSUE)" || (echo "usage: make spawn-status ISSUE=<n>" && exit 2)
	@python3 .artifacts/spawn/board.py status $(ISSUE)

.PHONY: spawn-claim
spawn-claim: ## Claim Backlog -> In Plan: make spawn-claim ISSUE=42
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

# ---- v0.3-1 login-shell registration helpers ----
#
# install-shells prints (does NOT execute) the platform-specific
# instructions a user runs to register the locally-built aish as
# a login shell. We deliberately do NOT sudo or write to
# /etc/shells from the Makefile — privileged + destructive
# operations stay user-initiated per the project's autonomy
# rules.
.PHONY: install-shells
install-shells: ## Print platform-specific instructions to register aish as a login shell
	@uname_s=$$(uname -s 2>/dev/null || echo unknown); \
	bin=$$(pwd)/shell/dist/aish; \
	case "$$uname_s" in \
	  Darwin|Linux) \
	    echo "→ To register aish as a login shell on $$uname_s:"; \
	    echo ""; \
	    echo "  1. Build the binary if you haven't already:"; \
	    echo "       make -C shell build"; \
	    echo ""; \
	    echo "  2. Add aish to /etc/shells (requires sudo):"; \
	    echo "       echo $$bin | sudo tee -a /etc/shells"; \
	    echo ""; \
	    echo "  3. Set aish as your login shell:"; \
	    echo "       chsh -s $$bin"; \
	    echo ""; \
	    echo "  4. (Optional) Drop a starter RC file in place:"; \
	    echo "       mkdir -p \$$HOME/.aish && cp data/aish/aishrc.example \$$HOME/.aish/aishrc.toml"; \
	    echo ""; \
	    echo "Log out and back in to land in aish."; \
	    ;; \
	  *) \
	    echo "→ install-shells: $$uname_s is not a supported login-shell host."; \
	    echo "  aish login-shell capabilities currently target macOS and Linux."; \
	    ;; \
	esac

.PHONY: help
help: ## List monorepo targets
	@awk 'BEGIN {FS = ":.*?## "; printf "Monorepo targets:\n\n"} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
