# ============================================================================
# mk/repo.mk — repository checks, the documentation site and releases
# ============================================================================
# Included by the Makefile. A change to this file runs only the repository
# checks every change runs on a pull request (ci/affected.json, ADR-0043).

.PHONY: docs-site release-binaries release-notes repo-check spec-lint trace secrets workflows-lint reuse-lint hygiene

REQTRACE      := $(GO) run ./tools/cmd/reqtrace
HYGIENE_HOOKS := trailing-whitespace end-of-file-fixer mixed-line-ending check-yaml check-toml \
	check-json check-merge-conflict check-case-conflict check-added-large-files detect-private-key

##@ Documentation site (ADR-0033)

# Verifies: DX-002.
docs-site: ## Build the documentation site from docs/ into site/dist; fails on a broken internal link
	cd site && bun install --frozen-lockfile && bun run build

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
