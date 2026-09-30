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
BUF_VERSION           := v1.73.0
PROTOC_GEN_GO_VERSION := v1.36.12
PROTOC_GEN_CONNECT_GO_VERSION := v1.21.0
PROTOC_GEN_CONNECT_OPENAPI_VERSION := v0.27.3
SQLC_VERSION          := v1.31.1
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
DART_PACKAGES := packages/plux_devtools packages/plux_flutter packages/plux_svgc packages/plux_widget_api apps/starter test/bench/runtime
# Generated Dart code is verified by regeneration (CI-003), not by the formatter.
DART_SOURCES  := find packages apps test/bench test/size -name '*.dart' ! -name '*.g.dart' ! -name '*_generated.dart' ! -path '*/build/*' ! -path '*/.dart_tool/*' -print0
# Everything `make gen` writes; `go-gen-check` fails if any of it changes.
GEN_PATHS     := backend tools docs/reference packages studio/packages schema
TOOLS_BIN     := $(subst \,/,$(shell $(GO) env GOPATH | tr -d '\r'))/bin
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint
GOVULNCHECK   ?= $(TOOLS_BIN)/govulncheck
GITLEAKS      ?= $(TOOLS_BIN)/gitleaks
ACTIONLINT    ?= $(TOOLS_BIN)/actionlint
BUF           ?= $(TOOLS_BIN)/buf
SQLC          ?= $(TOOLS_BIN)/sqlc
PROTO_DIR     := proto
PROTO_GO_DIR  := backend/internal/pluxv1
PROTO_API_DIR := docs/reference/api
# The base of `buf breaking`: the last tagged release of the contract
# (SRV-000). Override to compare with another ref.
PROTO_BASE    ?= $(shell git describe --tags --match 'backend/v*' --abbrev=0 2>/dev/null)
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

.PHONY: help setup hooks-install check test build gen gen-check clean wasm-codecs wasm-codecs-check \
	install-go-tools install-golangci-lint install-govulncheck install-gitleaks install-actionlint install-buf install-sqlc install-python-tools install-flatc \
	go-check proto proto-check proto-lint proto-format-check proto-breaking sqlc sqlc-check go-fmt go-fmt-check go-lint go-tidy go-tidy-check go-gen-check registry-lock-check go-test go-test-race go-cover \
	go-determinism go-budgets go-fuzz currencies-check go-vuln go-build go-reproducible \
	dart-check dart-get dart-lock-check dart-fmt dart-fmt-check dart-analyze dart-test dart-cover widgets-api widgets-api-check \
	studio-check studio-install studio-fmt studio-lint studio-typecheck studio-test studio-cover \
	compose-secrets compose-up compose-down compose-seed dev dev-starter dev-app e2e-starter compat compose-test image-check \
	bench-runtime bench-runtime-ab bench-sync size-android size-ios docs-site \
	release-binaries release-notes repo-check spec-lint trace secrets workflows-lint reuse-lint hygiene

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage: make \033[36m<target>\033[0m\n"} \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Everyday

setup: install-go-tools install-python-tools install-flatc ## Install pinned tools and git hooks (run once after cloning)
	@command -v flutter >/dev/null || echo "! Install Flutter $(FLUTTER_VERSION): https://docs.flutter.dev/get-started/install"
	@command -v bun >/dev/null || echo "! Install Bun $(BUN_VERSION): https://bun.sh/docs/installation"
	$(MAKE) hooks-install

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

test: go-test dart-test studio-test ## Run the unit tests of every component

build: go-build ## Build every binary into bin/

# flatc writes the section accessors for Go and Dart from the one schema
# file, and the binary schema of each section kind, from which schemagen
# derives the verifier's layout tables (BND-001, BND-012).
# flatc's Dart output applies `!` to values that cannot be null, which the
# analysis pub.dev scores (RT-001) reports; it is told to ignore that.
gen: ## Regenerate all generated code and reference documents (CI-003)
	@"$(FLATC)" --version 2>/dev/null | grep -qx "flatc version $(FLATC_VERSION)" || { echo "✗ flatc $(FLATC_VERSION) not found at $(FLATC); run 'make install-flatc'" >&2; exit 1; }
	@bfbs=$$(mktemp -d); trap 'rm -rf "$$bfbs"' EXIT; \
	rm -rf $(FBS_GO_DIR) $(FBS_DART_DIR); \
	"$(FLATC)" --go -o $(dir $(FBS_GO_DIR)) $(FBS_SCHEMA); \
	"$(FLATC)" --dart -o $(FBS_DART_DIR) $(FBS_SCHEMA); \
	sed -i.bak 's|^// ignore_for_file: unused_import,|// ignore_for_file: unnecessary_non_null_assertion, unused_import,|' $(FBS_DART_DIR)/*_generated.dart && rm -f $(FBS_DART_DIR)/*.bak; \
	"$(FLATC)" --binary --schema -o "$$bfbs" $(FBS_SECTIONS); \
	$(GO) run ./tools/cmd/schemagen -root . -bfbs "$$bfbs"
	@$(MAKE) proto
	@$(MAKE) sqlc
	@for m in $(GO_MODULES); do (cd $$m && $(GO) generate ./...); done

# The API contract generates the Go messages and handlers and the OpenAPI
# description; buf runs with locally built plugins, never remote ones
# (SRV-002, CI-003).
proto: ## Regenerate the API contract's Go code and OpenAPI description (SRV-002)
	@command -v "$(BUF)" >/dev/null || { echo "✗ buf not found at $(BUF); run 'make install-buf'" >&2; exit 1; }
	rm -rf $(PROTO_GO_DIR) $(PROTO_API_DIR)
	@mkdir -p $(PROTO_API_DIR)
	cd $(PROTO_DIR) && PATH="$(TOOLS_BIN):$$PATH" "$(BUF)" format -w .
	cd $(PROTO_DIR) && PATH="$(TOOLS_BIN):$$PATH" "$(BUF)" generate

wasm-codecs: ## Rebuild the WebAssembly image codecs from pinned sources (CMP-030, ADR-0027)
	sh backend/internal/compiler/media/codecs/build.sh

# WASM_CODECS_OUT keeps the rebuilt modules, so a mismatch can be examined.
WASM_CODECS_OUT ?=
wasm-codecs-check: ## Rebuild the image codecs elsewhere and compare them with codecs.lock
	@out=$${WASM_CODECS_OUT:-$$(mktemp -d)} && mkdir -p "$$out" && \
		sh backend/internal/compiler/media/codecs/build.sh "$$out" >/dev/null && \
		(cd "$$out" && sha256sum -c $(CURDIR)/backend/internal/compiler/media/codecs/codecs.lock)

# The server's queries are type-checked against its own migrations, so a
# query that does not match the schema fails here rather than at run time
# (SRV-020).
sqlc: ## Regenerate the server's database access code from its SQL (SRV-020)
	@command -v "$(SQLC)" >/dev/null || { echo "✗ sqlc not found at $(SQLC); run 'make install-sqlc'" >&2; exit 1; }
	cd backend && "$(SQLC)" generate

sqlc-check: ## Fail if a query does not type-check against the migrations
	cd backend && "$(SQLC)" vet 2>/dev/null || cd backend && "$(SQLC)" compile

gen-check: go-gen-check dart-lock-check studio-install ## Fail if generated code or lockfiles are not committed; run on a clean tree (CI-003)

clean: ## Remove build and coverage output
	rm -rf $(BIN_DIR) backend/coverage.out tools/coverage.out studio/coverage $(addsuffix /coverage,$(DART_PACKAGES)) reqtrace.md reqtrace.json

##@ Go (backend, tools)

go-check: go-fmt-check go-lint go-tidy-check go-gen-check registry-lock-check proto-check go-cover go-vuln go-build go-reproducible ## All Go gates

proto-check: proto-lint proto-format-check proto-breaking ## All API contract gates (SRV-000, SRV-002)

proto-lint: ## Lint the API contract with buf (SRV-002)
	"$(BUF)" lint $(PROTO_DIR)

proto-format-check: ## Fail if the API contract is not formatted
	"$(BUF)" format --diff --exit-code $(PROTO_DIR)

# With no tagged release of the contract yet there is no baseline to
# compare against, and the check reports that instead of failing (SRV-000).
proto-breaking: ## Fail on a breaking API change against the last release (SRV-000)
	@if [ -z "$(PROTO_BASE)" ]; then \
		echo "· no backend release tag yet; buf breaking has no baseline"; \
	else \
		"$(BUF)" breaking $(PROTO_DIR) --against ".git#tag=$(PROTO_BASE),subdir=$(PROTO_DIR)"; \
	fi

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

currencies-check: ## Compare schema/pxl/currencies.json with ISO 4217 list one from SIX: ISO4217_XML=<list-one.xml>
	@test -n "$(ISO4217_XML)" || { echo "✗ set ISO4217_XML to list-one.xml from https://www.six-group.com/en/products-services/financial-information/data-standards.html" >&2; exit 2; }
	cd tools && $(GO) run ./cmd/currencycheck "$(abspath $(ISO4217_XML))" ../schema/pxl/currencies.json

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

# plux_svgc is outside the workspace: the server image builds it with the
# Dart SDK alone, which cannot resolve the workspace's Flutter packages.
dart-get: ## Resolve the pub workspace and plux_svgc (updates the pubspec.lock files)
	flutter pub get
	cd packages/plux_svgc && dart pub get

dart-lock-check: dart-get ## Fail if a pubspec.lock is not in sync with its pubspecs (CI-003)
	@changed=$$(git status --porcelain -- pubspec.lock packages/plux_svgc/pubspec.lock); if [ -n "$$changed" ]; then \
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

##@ Benchmarks and size (QA-007)

# Measured runs of the runtime benchmark, and runs per side of the A/B
# comparison.
RUNS ?=
# The commit the A/B comparison measures against.
BASE ?= origin/main

bench-runtime: ## Build the runtime benchmark in profile mode and run it under xvfb (Linux; RUNS=5)
	test/bench/runtime/bench.sh measure $(RUNS)

# Verifies: QA-007.
bench-runtime-ab: ## Compare the runtime benchmark with BASE's runtime; fail on regressions beyond 10% (QA-007; RUNS=10)
	test/bench/runtime/bench.sh compare $(BASE) $(RUNS)

# Verifies: QA-007, NFR-007.
bench-sync: ## Sync the benchmark app on the simulated slow network against a server built from source (needs PLUX_TEST_DATABASE_URL)
	@test -n "$$PLUX_TEST_DATABASE_URL" || { echo "✗ set PLUX_TEST_DATABASE_URL to a PostgreSQL database (see docs/engineering/testing.md)"; exit 1; }
	cd backend && PLUX_E2E_FLUTTER="$$(command -v flutter)" $(GO) test -count=1 -timeout 30m -run TestSyncOnSlowNetwork -v ./internal/server

# Verifies: RT-061, NFR-009.
size-android: ## Check what plux_flutter adds to a release APK for arm64 (RT-061; needs the Android SDK)
	test/size/size.sh android

# Verifies: RT-061, NFR-009.
size-ios: ## Check what plux_flutter adds to a release iOS app for arm64 (RT-061; needs Xcode)
	test/size/size.sh ios

##@ Documentation site (ADR-0033)

# Verifies: DX-002.
docs-site: ## Build the documentation site from docs/ into site/dist; fails on a broken internal link
	cd site && bun install --frozen-lockfile && bun run build

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

##@ Stack (Docker Compose)

COMPOSE_DIR := deploy/compose
COMPOSE     := docker compose -f $(COMPOSE_DIR)/compose.yaml

compose-secrets: ## Generate the stack's credentials into deploy/compose/.secrets on first run
	@$(COMPOSE_DIR)/init-secrets.sh

compose-up: compose-secrets ## Start the single-node stack: server, PostgreSQL, SeaweedFS, Valkey, OTel, Prometheus, Grafana (DEP-002)
	$(COMPOSE) up -d --build --wait
	@echo "Plux Server: http://localhost:8080  Grafana: http://localhost:3000  Prometheus: http://localhost:9090"

compose-down: ## Stop the stack (volumes are kept; add -v by hand to delete them)
	$(COMPOSE) down

# The starter app under `make dev`: where the device reaches the stack
# (after `adb reverse`, localhost works on Android too) and the defines
# file dev-starter writes from the seeded installation.
DEV_ENDPOINT    ?= http://localhost:8080
STARTER_DEFINES := apps/starter/.dart_defines.json
# A connected Android or iOS device, emulator or simulator for the starter.
STARTER_DEVICE  := flutter devices --machine 2>/dev/null | grep -Eq '"targetPlatform": *"(android|ios)'

# Verifies: DEP-020.
dev: ## Start the stack with hot reload of the server and, with a device or emulator attached, of the starter app; seed sample apps on first run (DEP-020)
	$(MAKE) compose-seed COMPOSE_OVERLAY=$(COMPOSE_DIR)/compose.dev.yaml
	$(MAKE) dev-starter
	@if $(STARTER_DEVICE); then \
		echo "Server: rebuilt on every change under backend/ (log: $(COMPOSE_DIR)/.dev-watch.log)."; \
		$(COMPOSE) -f $(COMPOSE_DIR)/compose.dev.yaml watch --no-up --quiet > $(COMPOSE_DIR)/.dev-watch.log 2>&1 & watch=$$!; \
		trap 'kill $$watch 2>/dev/null' EXIT INT TERM; \
		$(MAKE) --no-print-directory dev-app; \
	else \
		echo "No Android or iOS device, emulator or simulator attached: watching the server only."; \
		echo "Start one and run 'make dev-app' in a second terminal for the starter app with hot reload."; \
		$(COMPOSE) -f $(COMPOSE_DIR)/compose.dev.yaml watch --no-up; \
	fi

dev-starter: ## Point the starter app at the seeded dev stack: write its defines and pull its baseline (DEP-020)
	@test -s $(COMPOSE_DIR)/.secrets/dev.env || { echo "✗ no seeded stack: run 'make dev' first"; exit 1; }
	@. ./$(COMPOSE_DIR)/.secrets/dev.env && \
		{ test -n "$$PLUX_DEV_STARTER" || { echo "✗ this stack was seeded before the starter app: delete its volumes (docker compose down -v) and run 'make dev'"; exit 1; }; } && \
		printf '{"PLUX_ENDPOINT":"%s","PLUX_APP_ID":"%s","PLUX_ENVIRONMENT":"staging"}\n' "$(DEV_ENDPOINT)" "$$PLUX_DEV_STARTER" > $(STARTER_DEFINES) && \
		cd backend && PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run ./cmd/plux pull --server http://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
			--app "$$PLUX_DEV_STARTER" --env staging -o ../apps/starter/assets/plux
	@echo "Starter: $(STARTER_DEFINES) and its baseline in apps/starter/assets/plux"

dev-app: ## Run the starter app against the dev stack with Flutter hot reload: r reloads, R restarts, q quits (DEP-020)
	@test -s $(STARTER_DEFINES) || $(MAKE) --no-print-directory dev-starter
	@if command -v adb >/dev/null 2>&1; then adb reverse tcp:8080 tcp:8080 >/dev/null 2>&1 || true; fi
	cd apps/starter && flutter run --dart-define-from-file=.dart_defines.json

# Verifies: QA-006.
e2e-starter: ## Run the starter app's end-to-end flows on this machine against a server built from source (needs PLUX_TEST_DATABASE_URL)
	@test -n "$$PLUX_TEST_DATABASE_URL" || { echo "✗ set PLUX_TEST_DATABASE_URL to a PostgreSQL database (see docs/engineering/testing.md)"; exit 1; }
	cd backend && PLUX_E2E_FLUTTER="$$(command -v flutter)" $(GO) test -count=1 -run TestStarterAppAgainstTheServer -v ./internal/server

# Verifies: QA-010.
compat: ## Run the compatibility matrix: released runtimes against today's server, today's runtime against released servers (QA-010)
	@test -n "$$PLUX_TEST_DATABASE_URL" || { echo "✗ set PLUX_TEST_DATABASE_URL to a PostgreSQL database (see docs/engineering/testing.md)"; exit 1; }
	test/compat/run.sh

# COMPOSE_OVERLAY adds a Compose file for compose-seed (dev or load).
COMPOSE_OVERLAY ?=

compose-seed: compose-secrets ## Start the stack and, on first run, seed an administrator, the loan calculator and the starter app promoted to staging
	$(COMPOSE) $(if $(COMPOSE_OVERLAY),-f $(COMPOSE_OVERLAY)) up -d --build --wait
	@if [ ! -s $(COMPOSE_DIR)/.secrets/dev.env ]; then \
		umask 077; \
		$(COMPOSE) $(if $(COMPOSE_OVERLAY),-f $(COMPOSE_OVERLAY)) run --rm --no-deps plux-server seed -config /etc/plux/plux.yaml -out - > $(COMPOSE_DIR)/.secrets/dev.env && \
		. ./$(COMPOSE_DIR)/.secrets/dev.env && \
		(cd backend && PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run ./cmd/plux publish --server http://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
			--app "$$PLUX_DEV_APP" -C ../schema/testdata/documents/loan-calculator --promote staging && \
		 PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run ./cmd/plux publish --server http://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
			--app "$$PLUX_DEV_STARTER" -C ../schema/testdata/documents/starter --env staging --promote staging); \
		echo "Seeded dev@plux.localhost; password and token in $(COMPOSE_DIR)/.secrets/dev.env"; \
	fi

# Verifies: QA-005.
compose-test: compose-secrets ## Run the Go integration and end-to-end tests against the stack's PostgreSQL, SeaweedFS and Valkey (QA-005)
	$(COMPOSE) -f $(COMPOSE_DIR)/compose.test.yaml up -d --wait postgres seaweedfs s3-bucket valkey
	. ./$(COMPOSE_DIR)/.secrets/postgres.env && . ./$(COMPOSE_DIR)/.secrets/plux-server.env && cd backend && \
		PLUX_TEST_DATABASE_URL="postgres://plux:$$PLUX_APP_PASSWORD@127.0.0.1:55432/plux?sslmode=disable" \
		PLUX_TEST_S3_ENDPOINT=http://127.0.0.1:58333 PLUX_TEST_S3_BUCKET=plux \
		PLUX_TEST_S3_ACCESS_KEY_ID="$$PLUX_OBJECT_STORAGE_ACCESS_KEY_ID" \
		PLUX_TEST_S3_SECRET_ACCESS_KEY="$$PLUX_OBJECT_STORAGE_SECRET_ACCESS_KEY" \
		PLUX_TEST_VALKEY_URL=redis://127.0.0.1:56379 \
		$(GO) test -race -count=1 ./internal/storage/... ./internal/cache/... ./internal/server/... ./internal/api/... ./internal/release/...

# The image as shipped, for this machine's platform: its plux-svgc and
# Skia library must reproduce the golden output (CI runs it on amd64 and
# arm64, CMP-002), and every component must carry its notices.
IMAGE_CHECK_TAG ?= plux-server:check
# Verifies: CMP-031, CMP-002.
image-check: ## Build the server image and check its SVG compiler's output and its third-party notices
	docker build -f backend/Dockerfile --target server -t $(IMAGE_CHECK_TAG) .
	@out=$$(mktemp); trap 'rm -f "$$out"' EXIT; \
	docker run --rm -i --network none --entrypoint /usr/local/bin/plux-svgc $(IMAGE_CHECK_TAG) \
		--libpathops /usr/local/lib/plux/libpath_ops.so < packages/plux_svgc/test/icon.svg > "$$out"; \
	cmp "$$out" packages/plux_svgc/test/icon.pathops.vec && echo "✓ plux-svgc in the image reproduces the golden"
	@id=$$(docker create $(IMAGE_CHECK_TAG)); trap 'docker rm -f "$$id" >/dev/null' EXIT; \
	docs=$$(docker cp "$$id:/usr/share/doc" - | tar -t); \
	for f in plux-server/go.LICENSE plux-server/THIRD_PARTY_NOTICES.txt plux-server/LICENSE-material-design-icons \
		plux-server/LICENSE-cupertino-icons plux-svgc/dart-sdk.LICENSE plux-svgc/skia.LICENSE plux-svgc/flutter-path_ops.LICENSE; do \
		printf '%s\n' "$$docs" | grep -qx "doc/$$f" || { echo "✗ the image lacks /usr/share/doc/$$f" >&2; exit 1; }; \
	done; echo "✓ the image carries its third-party notices"

##@ Releases (CI-008)

# Releasable components and their directories. Tags are <component>/v<semver>.
COMPONENT ?=
component_path = $(if $(filter backend,$(1)),backend,$(if $(filter plux_flutter,$(1)),packages/plux_flutter,$(if $(filter plux_devtools,$(1)),packages/plux_devtools,$(if $(filter studio,$(1)),studio,))))

# Platforms the CLI and server are released for (CLI-001).
RELEASE_PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
DIST_DIR          := dist

# Verifies: CLI-001, CI-006.
release-binaries: ## Build reproducible release archives of plux and plux-server for every platform into dist/
	@rm -rf $(DIST_DIR) && mkdir -p $(DIST_DIR)
	@set -e; for p in $(RELEASE_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		dir=$(DIST_DIR)/plux_$(BACKEND_VERSION)_$${os}_$${arch}; mkdir -p $$dir; \
		(cd backend && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build $(GO_BUILD_FLAGS) -o ../$$dir/ ./cmd/plux ./cmd/plux-server); \
		cp -r LICENSES $$dir/; \
		GO="$(GO)" CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch sh scripts/release/go-notices.sh $$dir/NOTICES ./cmd/plux ./cmd/plux-server; \
		find $$dir -exec touch -h -d @0 {} +; \
		if [ "$$os" = windows ]; then (cd $(DIST_DIR) && zip -qrX $${dir#$(DIST_DIR)/}.zip $${dir#$(DIST_DIR)/}); \
		else tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -C $(DIST_DIR) -cf - $${dir#$(DIST_DIR)/} | gzip -n > $$dir.tar.gz; fi; \
		rm -rf $$dir; \
	done
	@cd $(DIST_DIR) && sha256sum plux_* > SHA256SUMS && cat SHA256SUMS
	@scripts/release/package-manifests.sh $(BACKEND_VERSION) $(DIST_DIR)

release-notes: ## Print release notes for COMPONENT (backend, plux_flutter, plux_devtools, studio) since its last tag
	@test -n "$(call component_path,$(COMPONENT))" || { echo "✗ COMPONENT must be backend, plux_flutter, plux_devtools or studio" >&2; exit 2; }
	@COMPONENT=$(COMPONENT) git cliff --config cliff.toml --include-path "$(call component_path,$(COMPONENT))/**" \
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
