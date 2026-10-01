# ============================================================================
# mk/codecs.mk — the WebAssembly image codecs (CMP-030, ADR-0027)
# ============================================================================
# Included by the Makefile. A change to this file runs the codec rebuild on
# a pull request (ci/affected.json, ADR-0043).

.PHONY: wasm-codecs wasm-codecs-check

wasm-codecs: ## Rebuild the WebAssembly image codecs from pinned sources (CMP-030, ADR-0027)
	sh backend/internal/compiler/media/codecs/build.sh

# WASM_CODECS_OUT keeps the rebuilt modules, so a mismatch can be examined.
WASM_CODECS_OUT ?=
wasm-codecs-check: ## Rebuild the image codecs elsewhere and compare them with codecs.lock
	@out=$${WASM_CODECS_OUT:-$$(mktemp -d)} && mkdir -p "$$out" && \
		sh backend/internal/compiler/media/codecs/build.sh "$$out" >/dev/null && \
		(cd "$$out" && sha256sum -c $(CURDIR)/backend/internal/compiler/media/codecs/codecs.lock)
