# ============================================================================
# mk/dart.mk — Dart and Flutter gates
# ============================================================================
# Included by the Makefile. A change to this file runs the Dart job on a
# pull request (ci/affected.json, ADR-0043).

.PHONY: dart-check dart-get dart-lock-check dart-fmt dart-fmt-check dart-analyze dart-test dart-cover widgets-api widgets-api-check

DART_PACKAGES := packages/plux_auto_route packages/plux_db_drift packages/plux_devtools packages/plux_flutter packages/plux_go_router packages/plux_native_scan packages/plux_svgc packages/plux_widget_api apps/add_to_app/plux_module apps/starter test/bench/runtime
# Generated Dart code is verified by regeneration (CI-003), not by the formatter.
DART_SOURCES  := find packages apps test/bench test/size -name '*.dart' ! -name '*.g.dart' ! -name '*_generated.dart' ! -path '*/build/*' ! -path '*/.dart_tool/*' -print0

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
