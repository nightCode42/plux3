# ============================================================================
# mk/device.mk — end-to-end flows on the host, emulators and simulators (QA-006)
# ============================================================================
# Included by the Makefile. A change to this file runs the end-to-end jobs
# on a pull request (ci/affected.json, ADR-0043).

.PHONY: e2e-starter e2e-android e2e-ios compat

##@ End to end (QA-006, QA-010)

# Verifies: QA-006.
e2e-starter: ## Run the starter app's end-to-end flows on this machine against a server built from source (needs PLUX_TEST_DATABASE_URL)
	@test -n "$${PLUX_TEST_DATABASE_URL:-}" || { echo "✗ set PLUX_TEST_DATABASE_URL: run 'make test-db' and export what it prints (docs/engineering/testing.md)"; exit 1; }
	cd backend && PLUX_E2E_FLUTTER="$$(command -v flutter)" $(GO) test -count=1 -timeout 45m -run TestStarterAppAgainstTheServer -v ./internal/server

# ANDROID_API picks the emulator's system image for e2e-android.
ANDROID_API ?= 35

# Verifies: QA-006, RT-002.
e2e-android: ## Run the starter's flows on a headless Android emulator (CI only; needs the Android SDK, KVM, PLUX_TEST_DATABASE_URL; ANDROID_API=35)
	test/e2e/android.sh $(ANDROID_API)

# Verifies: QA-006, RT-002.
e2e-ios: ## Run the starter's flows on an iOS simulator (CI only; needs Xcode, jq, PLUX_TEST_DATABASE_URL)
	test/e2e/ios.sh

# Verifies: QA-010.
compat: ## Run the compatibility matrix: released runtimes against today's server, today's runtime against released servers (QA-010)
	@test -n "$${PLUX_TEST_DATABASE_URL:-}" || { echo "✗ set PLUX_TEST_DATABASE_URL: run 'make test-db' and export what it prints (docs/engineering/testing.md)"; exit 1; }
	test/compat/run.sh
