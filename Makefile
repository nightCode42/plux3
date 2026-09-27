# ============================================================================
# Plux — Makefile
# ============================================================================
# The single entry point for development tasks (CI-002). CI runs the same
# targets, so a green `make check` locally means a green pipeline.
# Run `make help` for the list of targets.

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
# flatc is built from source at the commit of its release tag (ADR-0002).
FLATC_VERSION         := 25.9.23
FLATC_COMMIT          := 187240970746d00bbd26b0f5873ed54d2477f9f3

GO            := go
# How long `make go-fuzz` runs each fuzz target.
FUZZTIME      ?= 30s
# Tools are built with the project's toolchain; a tool built with an older Go
# cannot analyse code that requires a newer one.
GO_TOOLCHAIN  := $(shell sed -n 's/^toolchain //p' backend/go.mod)
GO_INSTALL    := GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) install
GO_MODULES    := backend tools
DART_PACKAGES := packages/plux_flutter packages/plux_widget_api
# Generated Dart code is verified by regeneration (CI-003), not by the formatter.
DART_SOURCES  := find packages -name '*.dart' ! -name '*.g.dart' ! -name '*_generated.dart' ! -path '*/build/*' -print0
# Everything `make gen` writes; `go-gen-check` fails if any of it changes.
GEN_PATHS     := backend tools docs/reference packages studio/packages schema
TOOLS_BIN     := $(subst \,/,$(shell $(GO) env GOPATH | tr -d '\r'))/bin
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint
GOVULNCHECK   ?= $(TOOLS_BIN)/govulncheck
GITLEAKS      ?= $(TOOLS_BIN)/gitleaks
ACTIONLINT    ?= $(TOOLS_BIN)/actionlint
REQTRACE      := $(GO) run ./tools/cmd/reqtrace
COVGATE       := $(GO) run ./tools/cmd/covgate
BIN_DIR       := bin
FLATC_DIR     ?= $(HOME)/.cache/plux/flatc-$(FLATC_VERSION)
FLATC         ?= $(FLATC_DIR)/flatc
FBS_SCHEMA    := schema/fbs/bundle.fbs
FBS_SECTIONS  := schema/fbs/sections/*.fbs
FBS_GO_DIR    := backend/internal/bundle/fbs
FBS_DART_DIR  := packages/plux_flutter/lib/src/bundle/fbs
HYGIENE_HOOKS := trailing-whitespace end-of-file-fixer mixed-line-ending check-yaml check-toml \
	check-json check-merge-conflict check-case-conflict check-added-large-files detect-private-key

# ── Reproducible build metadata (CI-006) ────────────────────────────────────
# Everything below is derived from the commit, never from the build machine
# or the wall clock, so two builds of one commit are byte-identical.
BACKEND_VERSION ?= $(shell git describe --tags --match 'backend/v*' --dirty 2>/dev/null | sed 's|^backend/v||' || true)
BACKEND_VERSION := $(or $(BACKEND_VERSION),dev)
COMMIT          ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
COMMIT_DATE     ?= $(shell TZ=UTC0 git log -1 --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd 2>/dev/null || echo unknown)
BUILDINFO_PKG   := github.com/nightCode42/plux3/backend/internal/buildinfo
GO_BUILD_FLAGS  := -trimpath -buildvcs=false -ldflags "-s -w -buildid= \
	-X $(BUILDINFO_PKG).version=$(BACKEND_VERSION) \
	-X $(BUILDINFO_PKG).commit=$(COMMIT) \
	-X $(BUILDINFO_PKG).commitDate=$(COMMIT_DATE)"

.PHONY: help setup hooks-install check test build gen gen-check clean \
	install-go-tools install-golangci-lint install-govulncheck install-gitleaks install-actionlint install-python-tools install-flatc \
	go-check go-fmt go-fmt-check go-lint go-tidy go-tidy-check go-gen-check registry-lock-check go-test go-test-race go-cover \
	go-determinism go-budgets go-fuzz go-vuln go-build go-reproducible \
	dart-check dart-get dart-lock-check dart-fmt dart-fmt-check dart-analyze dart-test dart-cover widgets-api widgets-api-check \
	studio-check studio-install studio-fmt studio-lint studio-typecheck studio-test studio-cover \
	release-notes repo-check spec-lint trace secrets workflows-lint reuse-lint hygiene

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Everyday

setup: install-go-tools install-python-tools install-flatc ## Install pinned tools and git hooks (run once after cloning)
	@command -v flutter >/dev/null || echo "! Install Flutter $(FLUTTER_VERSION): https://docs.flutter.dev/get-started/install"
	@command -v bun >/dev/null || echo "! Install Bun $(BUN_VERSION): https://bun.sh/docs/installation"
	$(MAKE) hooks-install

install-go-tools: install-golangci-lint install-govulncheck install-gitleaks install-actionlint ## Install the pinned Go-based tools

install-golangci-lint: ## Install the pinned golangci-lint
	$(GO_INSTALL) github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

install-govulncheck: ## Install the pinned govulncheck
	$(GO_INSTALL) golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

install-gitleaks: ## Install the pinned gitleaks
	$(GO_INSTALL) github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

install-actionlint: ## Install the pinned actionlint
	$(GO_INSTALL) github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

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

test: go-test dart-test studio-test ## Run the unit tests of every component

build: go-build ## Build every binary into bin/

# flatc writes the section accessors for Go and Dart from the one schema
# file, and the binary schema of each section kind, from which schemagen
# derives the verifier's layout tables (BND-001, BND-012).
gen: ## Regenerate all generated code and reference documents (CI-003)
	@"$(FLATC)" --version 2>/dev/null | grep -qx "flatc version $(FLATC_VERSION)" || { echo "✗ flatc $(FLATC_VERSION) not found at $(FLATC); run 'make install-flatc'" >&2; exit 1; }
	@bfbs=$$(mktemp -d); trap 'rm -rf "$$bfbs"' EXIT; \
	rm -rf $(FBS_GO_DIR) $(FBS_DART_DIR); \
	"$(FLATC)" --go -o $(dir $(FBS_GO_DIR)) $(FBS_SCHEMA); \
	"$(FLATC)" --dart -o $(FBS_DART_DIR) $(FBS_SCHEMA); \
	"$(FLATC)" --binary --schema -o "$$bfbs" $(FBS_SECTIONS); \
	$(GO) run ./tools/cmd/schemagen -root . -bfbs "$$bfbs"
	@for m in $(GO_MODULES); do (cd $$m && $(GO) generate ./...); done

gen-check: go-gen-check dart-lock-check studio-install ## Fail if generated code or lockfiles are not committed; run on a clean tree (CI-003)

clean: ## Remove build and coverage output
	rm -rf $(BIN_DIR) backend/coverage.out tools/coverage.out studio/coverage $(addsuffix /coverage,$(DART_PACKAGES)) reqtrace.md reqtrace.json

##@ Go (backend, tools)

go-check: go-fmt-check go-lint go-tidy-check go-gen-check registry-lock-check go-cover go-vuln go-build go-reproducible ## All Go gates

go-fmt: ## Format Go code (gofumpt, goimports)
	@for m in $(GO_MODULES); do (cd $$m && "$(GOLANGCI_LINT)" fmt); done

go-fmt-check: ## Fail if Go code is not formatted
	@for m in $(GO_MODULES); do (cd $$m && "$(GOLANGCI_LINT)" fmt --diff); done

go-lint: ## Lint Go code with golangci-lint
	@for m in $(GO_MODULES); do (cd $$m && "$(GOLANGCI_LINT)" run); done

go-tidy: ## Tidy go.mod and go.sum of every module
	@for m in $(GO_MODULES); do (cd $$m && $(GO) mod tidy); done

go-tidy-check: ## Fail if a go.mod or go.sum is not tidy
	@for m in $(GO_MODULES); do (cd $$m && $(GO) mod tidy -diff); done

# Verifies: CI-003.
go-gen-check: gen ## Fail if regenerating code changes any file (CI-003)
	@changed=$$(git status --porcelain -- $(GEN_PATHS)); if [ -n "$$changed" ]; then \
		echo "$$changed"; echo "✗ Generated code is out of date or uncommitted. Run 'make gen' and commit."; exit 1; fi

# The ref whose permanent-ID lock registry-lock-check compares against.
LOCK_BASE ?= origin/main

# Verifies: BND-011.
registry-lock-check: ## Fail if a permanent ID recorded in the lock at LOCK_BASE was changed or removed (BND-011)
	@if git cat-file -e "$(LOCK_BASE):schema/widgets/ids.lock.json" 2>/dev/null; then \
		base=$$(mktemp); trap 'rm -f "$$base"' EXIT; \
		git show "$(LOCK_BASE):schema/widgets/ids.lock.json" > "$$base"; \
		$(GO) run ./tools/cmd/schemagen -root . -lock-base "$$base"; \
	else echo "registry-lock-check: no lock at $(LOCK_BASE); nothing to compare"; fi

go-test: ## Run Go unit tests
	@for m in $(GO_MODULES); do (cd $$m && $(GO) test ./...); done

go-test-race: ## Run Go unit tests with the race detector (needs cgo)
	@for m in $(GO_MODULES); do (cd $$m && CGO_ENABLED=1 $(GO) test -race ./...); done

go-cover: ## Run Go tests with race detector and coverage; enforce floors (QA-001)
	@for m in $(GO_MODULES); do (cd $$m && CGO_ENABLED=1 $(GO) test -race -covermode=atomic -coverprofile=coverage.out ./...); done
	$(COVGATE) -kind go $(addsuffix /coverage.out,$(GO_MODULES))

# Verifies: CMP-002.
go-determinism: ## Compile the conformance vectors and projects and compare with the goldens byte for byte (CMP-002)
	cd backend && $(GO) test -count=1 -run 'Golden|Determinism|Repeated|Conformance' ./internal/pxl ./internal/bundle ./internal/compiler ./cmd/plux

go-budgets: ## Check the compiler's timing budgets on this machine (CMP-050, SCH-042)
	cd backend && PLUX_BUDGETS=1 $(GO) test -count=1 -v -run '^TestPerformanceBudgets$$' ./internal/compiler

go-fuzz: ## Run every Go fuzz target for FUZZTIME each (QA-004, CMP-052)
	@for m in $(GO_MODULES); do (cd $$m && for pkg in $$($(GO) list ./...); do \
		for target in $$($(GO) test -list '^Fuzz' $$pkg | grep '^Fuzz' || true); do \
			echo "── $$pkg $$target"; \
			$(GO) test -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZTIME) $$pkg; \
		done; done); done

go-vuln: ## Scan Go dependencies for known vulnerabilities
	@for m in $(GO_MODULES); do (cd $$m && "$(GOVULNCHECK)" ./...); done

go-build: ## Build reproducible binaries into bin/ (CI-006)
	@mkdir -p $(BIN_DIR)
	cd backend && CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o ../$(BIN_DIR)/ ./cmd/...

# Verifies: CI-006.
go-reproducible: ## Build twice with cold caches and fail unless the binaries are identical (CI-006)
	@a=$$(mktemp -d); b=$$(mktemp -d); trap 'rm -rf "$$a" "$$b"' EXIT; \
	for d in "$$a" "$$b"; do \
		(cd backend && GOCACHE="$$d/cache" CGO_ENABLED=0 $(GO) build $(GO_BUILD_FLAGS) -o "$$d/bin/" ./cmd/...); \
	done; \
	(cd "$$a/bin" && sha256sum *) > "$$a/sums"; \
	(cd "$$b/bin" && sha256sum *) > "$$b/sums"; \
	cat "$$a/sums"; \
	diff "$$a/sums" "$$b/sums" && echo "✓ Go binaries are reproducible"

##@ Dart and Flutter (packages, apps)

dart-check: dart-lock-check dart-fmt-check dart-analyze widgets-api-check dart-cover ## All Dart gates

dart-get: ## Resolve the pub workspace (updates pubspec.lock)
	flutter pub get

dart-lock-check: dart-get ## Fail if pubspec.lock is not in sync with the pubspecs (CI-003)
	@changed=$$(git status --porcelain -- pubspec.lock); if [ -n "$$changed" ]; then \
		echo "$$changed"; echo "✗ pubspec.lock is out of date or uncommitted. Run 'make dart-get' and commit."; exit 1; fi

dart-fmt: ## Format hand-written Dart code
	$(DART_SOURCES) | xargs -0 dart format

dart-fmt-check: ## Fail if hand-written Dart code is not formatted
	$(DART_SOURCES) | xargs -0 dart format --output=none --set-exit-if-changed

dart-analyze: ## Analyze Dart code with strict rules; infos are fatal
	flutter analyze --fatal-infos --fatal-warnings

dart-test: ## Run Dart and Flutter tests
	@for p in $(DART_PACKAGES); do (cd $$p && flutter test); done

dart-cover: ## Run Dart tests with coverage and enforce floors (QA-001)
	@for p in $(DART_PACKAGES); do (cd $$p && flutter test --coverage); done
	$(COVGATE) -kind dart $(addsuffix /coverage/lcov.info,$(DART_PACKAGES))

WIDGETS_API := cd packages/plux_widget_api && dart run bin/extract.dart --root ../.. --flutter-version $(FLUTTER_VERSION)

widgets-api: ## Snapshot the pinned Flutter SDK's constructors and enums for the coverage table; then run 'make gen' (WGT-003)
	$(WIDGETS_API)

# Verifies: WGT-003.
widgets-api-check: ## Fail if schema/widgets/flutter-api.json does not match the pinned Flutter SDK (WGT-003)
	$(WIDGETS_API) --check

##@ Studio (Bun, TypeScript)

studio-check: studio-install studio-lint studio-typecheck studio-cover ## All Studio gates

studio-install: ## Install Studio dependencies exactly as locked
	cd studio && bun install --frozen-lockfile

studio-fmt: ## Format and fix Studio code with Biome
	cd studio && bunx biome check --write .

studio-lint: ## Lint and format-check Studio code with Biome
	cd studio && bunx biome ci --colors=off .

studio-typecheck: ## Type-check Studio code
	cd studio && bunx tsc -p tsconfig.json

studio-test: ## Run Studio tests
	cd studio && bun test

studio-cover: studio-test ## Enforce Studio coverage floors (QA-001)
	$(COVGATE) -kind studio studio/coverage/lcov.info

##@ Releases (CI-008)

# Releasable components and their directories. Tags are <component>/v<semver>.
COMPONENT ?=
component_path = $(if $(filter backend,$(1)),backend,$(if $(filter plux_flutter,$(1)),packages/plux_flutter,$(if $(filter studio,$(1)),studio,)))

release-notes: ## Print release notes for COMPONENT (backend, plux_flutter, studio) since its last tag
	@test -n "$(call component_path,$(COMPONENT))" || { echo "✗ COMPONENT must be backend, plux_flutter or studio" >&2; exit 2; }
	@git cliff --config cliff.toml --include-path "$(call component_path,$(COMPONENT))/**" \
		--tag-pattern "^$(COMPONENT)/v" $(if $(TAG),--tag "$(TAG)" --unreleased,--unreleased)

##@ Repository

repo-check: spec-lint trace workflows-lint reuse-lint hygiene ## Specification, traceability, workflows, licensing, hygiene

spec-lint: ## Check the specification's internal consistency (QA-073)
	$(REQTRACE) lint

# Verifies: QA-070.
trace: ## Generate the traceability report; fail on violations (QA-070)
	$(REQTRACE) report -strict -md reqtrace.md -json reqtrace.json

secrets: ## Scan the full git history for secrets
	"$(GITLEAKS)" git --config=.gitleaks.toml --redact --verbose

# zizmor runs its online audits (impostor commits, known-vulnerable actions)
# when a GitHub token is available, as in CI; otherwise it runs offline.
ZIZMOR_FLAGS ?= $(if $(GH_TOKEN),,--offline)

workflows-lint: ## Lint GitHub Actions workflows (actionlint, zizmor)
	"$(ACTIONLINT)"
	zizmor $(ZIZMOR_FLAGS) --min-severity=low .github/workflows

reuse-lint: ## Check licensing and copyright information (REUSE)
	reuse lint

hygiene: ## Run the file hygiene hooks on every file
	@for hook in $(HYGIENE_HOOKS); do pre-commit run "$$hook" --all-files; done
