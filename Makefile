# ============================================================================
# Plux — Makefile
# ============================================================================
# The single entry point for development tasks (CI-002). CI runs the same
# targets, so a green `make check` locally means a green pipeline.
# Run `make help` for the list of targets.

# GNU Make 4 or later: 3.81 (GnuWin32's, macOS's /usr/bin/make) ignores
# .SHELLFLAGS, so a failing command in a pipe would pass silently.
ifeq ($(filter 4.% 5.%,$(MAKE_VERSION)),)
$(error GNU Make $(MAKE_VERSION) is too old: use 4 or later (Windows: winget install ezwinports.make; macOS: brew install make, then gmake))
endif

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
MAKEFLAGS += --no-print-directory

# ── Pinned tool versions ────────────────────────────────────────────────────
# Change these together with .github/workflows/*.yml, in one pull request
# (docs/engineering/ci.md §4). Go comes from the `toolchain` line in go.mod.
FLUTTER_VERSION       := 3.47.5
BUN_VERSION           := 1.3.11
GOLANGCI_LINT_VERSION := v2.14.0
GOVULNCHECK_VERSION   := v1.8.0
GITLEAKS_VERSION      := v8.30.1
ACTIONLINT_VERSION    := v1.7.12
PRE_COMMIT_VERSION    := 4.6.2
ZIZMOR_VERSION        := 1.30.1
REUSE_VERSION         := 6.2.0
BUF_VERSION           := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_CONNECT_GO_VERSION := v1.21.0
PROTOC_GEN_CONNECT_OPENAPI_VERSION := v0.27.3
SQLC_VERSION          := v1.31.1
# flatc is built from source at the commit of its release tag (ADR-0002).
FLATC_VERSION         := 25.9.23
FLATC_COMMIT          := 187240970746d00bbd26b0f5873ed54d2477f9f3

GO            := go
# Tools are built with the project's toolchain; a tool built with an older Go
# cannot analyse code that requires a newer one.
GO_TOOLCHAIN  := $(shell sed -n 's/^toolchain //p' backend/go.mod)
GO_INSTALL    := GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) install
TOOLS_BIN     := $(subst \,/,$(shell $(GO) env GOPATH | tr -d '\r'))/bin
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint
GOVULNCHECK   ?= $(TOOLS_BIN)/govulncheck
GITLEAKS      ?= $(TOOLS_BIN)/gitleaks
ACTIONLINT    ?= $(TOOLS_BIN)/actionlint
BUF           ?= $(TOOLS_BIN)/buf
SQLC          ?= $(TOOLS_BIN)/sqlc
COVGATE       := $(GO) run ./tools/cmd/covgate
BIN_DIR       := bin
FLATC_DIR     ?= $(HOME)/.cache/plux/flatc-$(FLATC_VERSION)
FLATC         ?= $(FLATC_DIR)/flatc

MK_FRAGMENTS := go codecs dart bench studio stack device repo

.PHONY: help setup hooks-install check check-changed test build gen-check clean \
	install-go-tools install-golangci-lint install-govulncheck install-gitleaks install-actionlint install-buf install-sqlc install-python-tools install-flatc

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Everyday

setup: install-go-tools install-python-tools install-flatc ## Install pinned tools and git hooks (run once after cloning)
	@command -v flutter >/dev/null || echo "! Install Flutter $(FLUTTER_VERSION): https://docs.flutter.dev/get-started/install"
	@command -v bun >/dev/null || echo "! Install Bun $(BUN_VERSION): https://bun.sh/docs/installation"
	"$(MAKE)" hooks-install

install-go-tools: install-golangci-lint install-govulncheck install-gitleaks install-actionlint install-buf install-sqlc ## Install the pinned Go-based tools

install-golangci-lint: ## Install the pinned golangci-lint
	$(GO_INSTALL) github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

install-govulncheck: ## Install the pinned govulncheck
	$(GO_INSTALL) golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

install-gitleaks: ## Install the pinned gitleaks
	$(GO_INSTALL) github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

install-actionlint: ## Install the pinned actionlint
	$(GO_INSTALL) github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

install-buf: ## Install buf and the protoc plugins the API contract needs (ADR-0005)
	$(GO_INSTALL) github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	$(GO_INSTALL) google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO_INSTALL) connectrpc.com/connect/cmd/protoc-gen-connect-go@$(PROTOC_GEN_CONNECT_GO_VERSION)
	$(GO_INSTALL) github.com/sudorandom/protoc-gen-connect-openapi@$(PROTOC_GEN_CONNECT_OPENAPI_VERSION)

install-sqlc: ## Install the pinned sqlc, which type-checks the server's SQL (ADR-0007)
	$(GO_INSTALL) github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

PYTHON_TOOLS := pre-commit==$(PRE_COMMIT_VERSION) zizmor==$(ZIZMOR_VERSION) reuse==$(REUSE_VERSION)

# pipx where available (required on PEP 668 "externally managed" systems such as Ubuntu 23.04+), pip --user otherwise.
install-python-tools: ## Install the pinned pre-commit, zizmor and reuse
	@if command -v pipx >/dev/null; then \
		for t in $(PYTHON_TOOLS); do pipx install --force "$$t" || exit 1; done; \
	else \
		python3 -m pip install --user --quiet $(PYTHON_TOOLS) || { echo "✗ pip refused; install pipx (e.g. apt install pipx) and rerun" >&2; exit 1; }; \
	fi

# Builds only the flatc target (needs git, CMake and a C++ compiler); the
# tag is checked against the pinned commit before anything is built.
install-flatc: ## Build the pinned flatc from source into FLATC_DIR (ADR-0002)
	@if "$(FLATC)" --version 2>/dev/null | grep -qx "flatc version $(FLATC_VERSION)"; then echo "flatc $(FLATC_VERSION): $(FLATC)"; exit 0; fi; \
	for tool in git cmake c++; do command -v $$tool >/dev/null || { \
		echo "✗ install-flatc needs $$tool: sudo apt-get install -y git cmake g++ (Debian/Ubuntu/WSL) or brew install cmake (macOS)" >&2; exit 1; }; done; \
	src=$$(mktemp -d); trap 'rm -rf "$$src"' EXIT; \
	git -c advice.detachedHead=false clone --quiet --depth 1 --branch "v$(FLATC_VERSION)" https://github.com/google/flatbuffers.git "$$src"; \
	if [ "$$(git -C "$$src" rev-parse HEAD)" != "$(FLATC_COMMIT)" ]; then echo "✗ flatc tag v$(FLATC_VERSION) is not commit $(FLATC_COMMIT)" >&2; exit 1; fi; \
	cmake -S "$$src" -B "$$src/build" -DCMAKE_BUILD_TYPE=Release \
		-DFLATBUFFERS_BUILD_TESTS=OFF -DFLATBUFFERS_BUILD_FLATLIB=OFF -DFLATBUFFERS_BUILD_FLATHASH=OFF >/dev/null; \
	cmake --build "$$src/build" --target flatc --parallel >/dev/null; \
	mkdir -p "$(FLATC_DIR)"; cp "$$src/build/flatc" "$(FLATC)"; "$(FLATC)" --version

hooks-install: ## Register the git hooks (pre-commit, commit-msg, pre-push)
	pre-commit install --hook-type pre-commit --hook-type commit-msg --hook-type pre-push

check: repo-check go-check dart-check studio-check ## Run every quality gate CI runs
	@echo "✓ All checks passed"

# What check-changed compares the working tree with: by default the merge
# base with origin/main, as CI compares a pull request with its base.
CHANGED_BASE ?= $(shell git merge-base origin/main HEAD 2>/dev/null)
AFFECTED     := $(GO) run ./tools/cmd/affected

check-changed: ## Run the gates of the jobs CI would select for your changes since CHANGED_BASE (ADR-0043)
	@test -n "$(CHANGED_BASE)" || { echo "✗ no merge base with origin/main: run 'git fetch origin main' or set CHANGED_BASE" >&2; exit 2; }
	@targets=$$($(AFFECTED) -base "$(CHANGED_BASE)" -make) && echo "→ make $$targets" && "$(MAKE)" $$targets

test: go-test dart-test studio-test ## Run the unit tests of every component

build: go-build ## Build every binary into bin/

gen-check: go-gen-check dart-lock-check studio-install ## Fail if generated code or lockfiles are not committed; run on a clean tree (CI-003)

clean: ## Remove build and coverage output
	rm -rf $(BIN_DIR) backend/coverage.out tools/coverage.out studio/coverage $(addsuffix /coverage,$(DART_PACKAGES)) reqtrace.md reqtrace.json

# The fragments: one per area, so a change to the recipes of one area runs
# only that area's CI jobs (ci/affected.json, ADR-0043). A change to this
# file runs every job.
include $(addprefix mk/,$(addsuffix .mk,$(MK_FRAGMENTS)))
