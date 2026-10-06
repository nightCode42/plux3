# ============================================================================
# mk/go.mk — Go gates, the API contract and code generation
# ============================================================================
# Included by the Makefile. A change to this file runs the Go jobs on a pull
# request (ci/affected.json, ADR-0043).

.PHONY: gen proto sqlc sqlc-check go-check proto-check proto-lint proto-format-check proto-breaking go-fmt go-fmt-check \
	go-lint go-tidy go-tidy-check go-gen-check registry-lock-check go-test go-test-race go-cover \
	go-determinism go-budgets go-fuzz currencies-check phone-metadata go-vuln go-build go-reproducible

# How long `make go-fuzz` runs each fuzz target.
FUZZTIME      ?= 30s
GO_MODULES    := backend tools test/refapi
# Everything `make gen` writes; `go-gen-check` fails if any of it changes.
GEN_PATHS     := backend tools docs/reference packages studio/packages schema
PROTO_DIR     := proto
PROTO_GO_DIR  := backend/internal/pluxv1
PROTO_API_DIR := docs/reference/api
# The base of `buf breaking`: the last tagged release of the contract
# (SRV-000). Override to compare with another ref.
PROTO_BASE    ?= $(shell git describe --tags --match 'backend/v*' --abbrev=0 2>/dev/null)
FBS_SCHEMA    := schema/fbs/bundle.fbs
FBS_SECTIONS  := schema/fbs/sections/*.fbs
FBS_GO_DIR    := backend/internal/bundle/fbs
FBS_DART_DIR  := packages/plux_flutter/lib/src/bundle/fbs

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

##@ Code generation (CI-003)

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
	@"$(MAKE)" proto
	@"$(MAKE)" sqlc
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

# The server's queries are type-checked against its own migrations, so a
# query that does not match the schema fails here rather than at run time
# (SRV-020).
sqlc: ## Regenerate the server's database access code from its SQL (SRV-020)
	@command -v "$(SQLC)" >/dev/null || { echo "✗ sqlc not found at $(SQLC); run 'make install-sqlc'" >&2; exit 1; }
	cd backend && "$(SQLC)" generate

sqlc-check: ## Fail if a query does not type-check against the migrations
	cd backend && "$(SQLC)" vet 2>/dev/null || cd backend && "$(SQLC)" compile

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

# libphonenumber's release and commit that schema/pxl/phone.json is derived
# from (pxl.phone.v1, schema/pxl/phone.md); move both to update the metadata.
LIBPHONENUMBER_VERSION := v9.0.40
LIBPHONENUMBER_COMMIT  := 9d77a67180fc7d342bf04da469968501317bbc91

phone-metadata: ## Re-derive schema/pxl/phone.json from libphonenumber at the pinned commit, then run 'make gen'
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	curl -sSfL -o "$$tmp/PhoneNumberMetadata.xml" "https://raw.githubusercontent.com/google/libphonenumber/$(LIBPHONENUMBER_COMMIT)/resources/PhoneNumberMetadata.xml"; \
	cd tools && $(GO) run ./cmd/phonemeta -xml "$$tmp/PhoneNumberMetadata.xml" -version $(LIBPHONENUMBER_VERSION) -commit $(LIBPHONENUMBER_COMMIT) -out ../schema/pxl/phone.json

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
