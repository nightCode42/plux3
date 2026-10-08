# ============================================================================
# mk/stack.mk — the Docker Compose stack, the dev loop and the server image
# ============================================================================
# Included by the Makefile. A change to this file runs the Compose and image
# jobs on a pull request (ci/affected.json, ADR-0043).

.PHONY: compose-secrets compose-up compose-down test-db test-db-down compose-seed dev dev-starter dev-app compose-test image-check

##@ Stack (Docker Compose)

COMPOSE_DIR := deploy/compose
COMPOSE     := docker compose -f $(COMPOSE_DIR)/compose.yaml
# The development CA the server's certificate chains to. Go clients on Linux
# read it from SSL_CERT_FILE; elsewhere trust it first (deploy/compose/README.md).
DEV_CA      := $(abspath $(COMPOSE_DIR))/.secrets/tls/ca.crt

compose-secrets: ## Generate the stack's credentials into deploy/compose/.secrets on first run
	@$(COMPOSE_DIR)/init-secrets.sh

compose-up: compose-secrets ## Start the single-node stack: server, PostgreSQL, SeaweedFS, Valkey, OTel, Prometheus, Grafana (DEP-002)
	$(COMPOSE) up -d --build --wait
	@echo "Plux Server: https://localhost:8080  Grafana: http://localhost:3000  Prometheus: http://localhost:9090"

compose-down: ## Stop the stack (volumes are kept; add -v by hand to delete them)
	$(COMPOSE) down

test-db: ## Start a throwaway PostgreSQL for the tests and print the PLUX_TEST_DATABASE_URL to export (TEST_DB_PORT=55432)
	@scripts/test-db.sh up

test-db-down: ## Remove the test database and its data
	@scripts/test-db.sh down

# The starter app under `make dev`: where the device reaches the stack
# (after `adb reverse`, localhost works on Android too) and the defines
# file dev-starter writes from the seeded installation.
DEV_ENDPOINT    ?= https://localhost:8080
STARTER_DEFINES := apps/starter/.dart_defines.json
# A connected Android or iOS device, emulator or simulator for the starter.
STARTER_DEVICE  := flutter devices --machine 2>/dev/null | grep -Eq '"targetPlatform": *"(android|ios)'

# Verifies: DEP-020.
dev: ## Start the stack with hot reload of the server and, with a device or emulator attached, of the starter app; seed sample apps on first run (DEP-020)
	"$(MAKE)" compose-seed COMPOSE_OVERLAY=$(COMPOSE_DIR)/compose.dev.yaml
	"$(MAKE)" dev-starter
	@if $(STARTER_DEVICE); then \
		echo "Server: rebuilt on every change under backend/ (log: $(COMPOSE_DIR)/.dev-watch.log)."; \
		$(COMPOSE) -f $(COMPOSE_DIR)/compose.dev.yaml watch --no-up --quiet > $(COMPOSE_DIR)/.dev-watch.log 2>&1 & watch=$$!; \
		trap 'kill $$watch 2>/dev/null' EXIT INT TERM; \
		"$(MAKE)" --no-print-directory dev-app; \
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
		cd backend && PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run -exec "env SSL_CERT_FILE=$(DEV_CA)" ./cmd/plux pull --server https://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
			--app "$$PLUX_DEV_STARTER" --env staging -o ../apps/starter/assets/plux
	@echo "Starter: $(STARTER_DEFINES) and its baseline in apps/starter/assets/plux"

dev-app: ## Run the starter app against the dev stack with Flutter hot reload: r reloads, R restarts, q quits (DEP-020)
	@test -s $(STARTER_DEFINES) || "$(MAKE)" --no-print-directory dev-starter
	@if command -v adb >/dev/null 2>&1; then adb reverse tcp:8080 tcp:8080 >/dev/null 2>&1 || true; fi
	cd apps/starter && flutter run --dart-define-from-file=.dart_defines.json
# COMPOSE_OVERLAY adds a Compose file for compose-seed (dev or load).
COMPOSE_OVERLAY ?=

compose-seed: compose-secrets ## Start the stack and, on first run, seed an administrator, the loan calculator and the starter app promoted to staging
	$(COMPOSE) $(if $(COMPOSE_OVERLAY),-f $(COMPOSE_OVERLAY)) up -d --build --wait
	@if [ ! -s $(COMPOSE_DIR)/.secrets/dev.env ]; then \
		umask 077; \
		$(COMPOSE) $(if $(COMPOSE_OVERLAY),-f $(COMPOSE_OVERLAY)) run --rm --no-deps plux-server seed -config /etc/plux/plux.yaml -out - > $(COMPOSE_DIR)/.secrets/dev.env && \
		. ./$(COMPOSE_DIR)/.secrets/dev.env && \
		(cd backend && PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run -exec "env SSL_CERT_FILE=$(DEV_CA)" ./cmd/plux publish --server https://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
			--app "$$PLUX_DEV_APP" -C ../schema/testdata/documents/loan-calculator --promote staging && \
		 PLUX_TOKEN="$$PLUX_DEV_TOKEN" $(GO) run -exec "env SSL_CERT_FILE=$(DEV_CA)" ./cmd/plux publish --server https://localhost:8080 --org "$$PLUX_DEV_ORGANIZATION" \
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
