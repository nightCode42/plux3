# ============================================================================
# mk/studio.mk — Studio gates (Bun, TypeScript)
# ============================================================================
# Included by the Makefile. A change to this file runs the Studio job on a
# pull request (ci/affected.json, ADR-0043).

.PHONY: studio-check studio-install studio-fmt studio-lint studio-typecheck studio-test studio-cover

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
