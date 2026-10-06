# ============================================================================
# mk/bench.mk — benchmarks and size (QA-007)
# ============================================================================
# Included by the Makefile. A change to this file runs the benchmark and
# size jobs on a pull request (ci/affected.json, ADR-0043).

.PHONY: bench-runtime bench-runtime-ab bench-sync bench-steps bench-pxl size-android size-ios

##@ Benchmarks and size (QA-007)

# Measured runs of the runtime benchmark, and runs per side of the A/B
# comparison.
RUNS ?=
# The commit the A/B comparison measures against.
BASE ?= origin/main
# The parts of each run to measure (startup, open, native, scroll, list;
# comma-separated); all when empty. CI runs them in parallel jobs.
SCENARIOS ?=

bench-runtime: ## Build the runtime benchmark in profile mode and run it under xvfb (Linux; RUNS=5, SCENARIOS=all)
	BENCH_SCENARIOS="$(SCENARIOS)" test/bench/runtime/bench.sh measure $(RUNS)

# Verifies: QA-007.
bench-runtime-ab: ## Compare the runtime benchmark with BASE's runtime; fail on regressions beyond 10% (QA-007; RUNS=10, SCENARIOS=all)
	BENCH_SCENARIOS="$(SCENARIOS)" test/bench/runtime/bench.sh compare $(BASE) $(RUNS)

# Verifies: QA-007, NFR-007.
bench-sync: ## Sync the benchmark app on the simulated slow network against a server built from source (needs PLUX_TEST_DATABASE_URL)
	@test -n "$${PLUX_TEST_DATABASE_URL:-}" || { echo "✗ set PLUX_TEST_DATABASE_URL: run 'make test-db' and export what it prints (docs/engineering/testing.md)"; exit 1; }
	cd backend && PLUX_E2E_FLUTTER="$$(command -v flutter)" $(GO) test -count=1 -timeout 30m -run TestSyncOnSlowNetwork -v ./internal/server

# The engine's cost per step, an early measurement of NFR-011 (recorded in
# docs/benchmarks/p4-routing.md, not gated: the gate arrives in P5).
bench-steps: ## Measure the action engine's cost per step (NFR-011, early; prints JSON)
	cd packages/plux_flutter && PLUX_BENCH_STEPS=1 flutter test --reporter expanded test/actions/step_bench_test.dart

# Verifies: NFR-010, PXL-004.
bench-pxl: ## Measure the evaluation of typical PXL bindings (NFR-010; prints JSON)
	cd packages/plux_flutter && PLUX_BENCH_PXL=1 flutter test --reporter expanded test/pxl/pxl_bench_test.dart

# Verifies: RT-061, NFR-009.
size-android: ## Check what plux_flutter adds to the release APKs and App Bundle downloads (RT-061; needs the Android SDK)
	test/size/size.sh android

# Verifies: RT-061, NFR-009.
size-ios: ## Check what plux_flutter adds to a release iOS app for arm64 (RT-061; needs Xcode)
	test/size/size.sh ios
