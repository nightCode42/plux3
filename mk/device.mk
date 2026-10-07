# ============================================================================
# mk/device.mk — end-to-end flows on the host, emulators and simulators (QA-006)
# ============================================================================
# Included by the Makefile. A change to this file runs the end-to-end jobs
# on a pull request (ci/affected.json, ADR-0043).

.PHONY: e2e-starter e2e-android e2e-ios compat

##@ End to end (QA-006, QA-010)

# E2E_SHARD picks the flows: starter (the starter and a generated project),
# hosts (the add-to-app module and hosts), reference (Plux Bank and Plux
# Express), or all. CI runs the shards as parallel jobs (ADR-0043, Revision).
E2E_SHARD ?= all
E2E_RUN_starter := TestStarterAppAgainstTheServer|TestGeneratedAppAgainstTheServer
E2E_RUN_hosts := TestAddToAppAgainstTheServer
E2E_RUN_reference := TestPluxBankAgainstTheServer|TestPluxExpressAgainstTheServer
# One go test run per word; with all, the reference apps run last, so a
# failure of theirs ends the log.
E2E_RUNS = $(if $(filter all,$(E2E_SHARD)),$(E2E_RUN_starter)|$(E2E_RUN_hosts) $(E2E_RUN_reference),$(E2E_RUN_$(E2E_SHARD)))

# Verifies: QA-006, HST-033, DX-004.
e2e-starter: ## Run the starter app's end-to-end flows, a project plux create generates, the add-to-app module's and the reference apps' against a server built from source (needs PLUX_TEST_DATABASE_URL; E2E_SHARD=all|starter|hosts|reference)
	@test -n "$${PLUX_TEST_DATABASE_URL:-}" || { echo "✗ set PLUX_TEST_DATABASE_URL: run 'make test-db' and export what it prints (docs/engineering/testing.md)"; exit 1; }
	@test -n "$(E2E_RUNS)" || { echo "✗ E2E_SHARD=$(E2E_SHARD): use all, starter, hosts or reference"; exit 1; }
	for run in $(foreach r,$(E2E_RUNS),'$(r)'); do \
		(cd backend && PLUX_E2E_FLUTTER="$$(command -v flutter)" $(GO) test -count=1 -timeout 60m -run "$$run" -v ./internal/server) || exit 1; \
	done

# ANDROID_API picks the emulator's system image for e2e-android.
ANDROID_API ?= 35

# Verifies: QA-006, RT-002.
e2e-android: ## Run the starter's and the Kotlin add-to-app host's flows on a headless Android emulator (CI only; needs the Android SDK, KVM, PLUX_TEST_DATABASE_URL; ANDROID_API=35)
	test/e2e/android.sh $(ANDROID_API)

# Verifies: QA-006, RT-002.
e2e-ios: ## Run the starter's and the Swift add-to-app host's flows on an iOS simulator (CI only; needs Xcode, CocoaPods, jq, PLUX_TEST_DATABASE_URL)
	test/e2e/ios.sh

# Verifies: QA-010.
compat: ## Run the compatibility matrix: released runtimes against today's server, today's runtime against released servers (QA-010)
	@test -n "$${PLUX_TEST_DATABASE_URL:-}" || { echo "✗ set PLUX_TEST_DATABASE_URL: run 'make test-db' and export what it prints (docs/engineering/testing.md)"; exit 1; }
	test/compat/run.sh
