# 0027. Asset pipeline: uploads, WebAssembly image codecs and dotLottie

- **Status:** Accepted
- **Date:** 2026-09-28; revised 2026-09-28 for P3 (see [Revision](#revision-2026-09-28-p3-svg-and-assets-on-the-device))
- **Requirements:** `SRV-060`, `CMP-030`, `CMP-033`, `AST-003`, `CMP-031` (P3 revision), `AST-001`, `AST-002`, `RT-014`; deferred: `CMP-032` (P8)

## Context and problem

Assets are uploaded through Studio or `plux push`, stored, and published with the app. `SRV-060`
requires uploads to be size-limited, typed from their bytes, stripped of metadata, deduplicated by
hash and optionally scanned for malware. `CMP-030` requires raster images as WebP (and AVIF where
the runtime supports it) at 1×, 2× and 3×, metadata-free and content-addressed; `CMP-033` requires
Lottie as dotLottie. The server binary is built without cgo so that release builds are reproducible
(`CI-006`), and Go's standard library encodes neither WebP nor AVIF. How are images transcoded, and
where does each step run?

## Decision drivers

- No cgo in the server or CLI; the same input gives the same bytes on every machine (`CMP-002`).
- A codec bug on a hostile file must not reach the server's memory.
- An upload answers quickly; transcoding, which takes seconds, does not block it.
- Nothing is transcoded twice.

## Considered options

1. **libwebp, and libavif with libaom, compiled to WebAssembly and run on wazero.**
2. cgo bindings to the same libraries.
3. External encoder processes (`cwebp`, `avifenc`) in the image.
4. Pure-Go encoders.

## Decision

Chosen option: **1**, as the maintainer decided for P2.

- **Codecs.** `backend/internal/compiler/media/codecs/build.sh` fetches libwebp v1.5.0, libaom
  v3.12.1 (encoder only, generic C, single-threaded) and libavif v1.3.0 at pinned commits, checks
  them, and builds `webp.wasm` (encode and decode) and `avif.wasm` (encode) with clang 18 and
  wasi-libc as WASI reactors. Two small shims stand in for what WASI lacks — `setjmp`, whose
  `longjmp` becomes a trap, and `pthread_once`/`pthread_create`, which run inline or report no
  thread — so an error path ends the call instead of the process. The modules are committed with
  their hashes in `codecs.lock`, a test pins the embedded bytes to it, and `make wasm-codecs-check`
  rebuilds them elsewhere and compares; rebuilding in two directories gave identical bytes.
- **Runtime.** wazero compiles the modules once per process; every call instantiates a fresh
  module with a 1 GiB memory ceiling, no files, no environment and a clock that does not move, so
  output depends on input alone and no image sees another's memory. Only the worker role compiles
  the codecs.
- **Upload** (api role, synchronous): the size against `asset.fileSize`; the type from the bytes —
  PNG, JPEG, GIF, WebP, SVG, TrueType, OpenType, Lottie JSON, dotLottie, Rive — never the name;
  packaging, where Lottie JSON becomes a canonical dotLottie archive and an uploaded one is checked
  (entry count, expanded size, safe paths, a manifest naming Lottie animations) and rewritten
  canonically; metadata stripping — PNG ancillary text, time and profile chunks, JPEG APP1–APP15
  and comments (a JPEG turned by its EXIF orientation is turned and re-encoded first, so it still
  displays upright), GIF comments and application data other than looping, WebP EXIF, XMP and
  ICC chunks, SVG `metadata` elements; a raster image's pixel count against `asset.imagePixels`,
  read from its header before anything is decoded; then the malware scanner when configured
  (`assets.malwareScanner`, ClamAV's `INSTREAM`). The cleaned file is stored once per SHA-256 in
  object storage, and the upload is a write to the app-level draft: `assets/index.json` lists it,
  under the app's editing lock, in one snapshot. A raster image is left `pending` and a transcoding
  job is enqueued in the same transaction.
- **Transcoding** (worker role, asynchronous): the image is decoded — PNG, JPEG and GIF by the
  standard library, WebP by libwebp — taken to be drawn at 3×, and resized to 2× and 1× by area
  averaging with alpha weighting in integer arithmetic; each density is encoded as WebP (lossless
  for PNG, GIF and lossless WebP, quality 85 otherwise) and as AVIF (quality 60, speed 6). Variants
  are stored by hash and listed on the asset; the same content uploaded again reuses them. Animated
  images are kept as uploaded. A file the codecs refuse is marked `failed` with a diagnostic.
  Compiling the two modules costs seconds of CPU, so the worker compiles them in the background
  while it starts and serves, and an asset job waits for them.
- **Publish.** The compiler lists each asset's variants in the `assets-index` section — a new,
  additive `variants` field of `Asset` in the bundle IDL — from what the pipeline recorded, and
  checks `asset.fileSize` per file and `plugin.assetBytes` per plugin (`AST-003`). A publish job
  first waits until none of the app's assets is `pending`: compiled earlier, its bundle would list
  fewer variants than the release's recompilation finds, and the release would be refused
  (`PLX-8050`, `REL-003`). The job is snoozed in the queue, which counts no attempt, and checked
  again every two seconds; after `publish.assetWait` (default ten minutes) from when it was queued
  it fails with `PLX-8053`. Transcoding stays with the asset job alone (maintainer, 2026-09-29).
- **Where the code lives.** Sniffing, stripping, packaging and transcoding are
  `internal/compiler/media`, a library the CLI can use offline; uploads, the job and the index are
  `internal/document`, since assets are part of the app's draft. Spec §6.3 gains no module.

The 3.6 MB `avif.wasm` is exempted, by name, from the 500 KB large-file check (maintainer, 2026-09-28).
Its future depends on the Flutter runtime: if the P3 runtime decodes AVIF properly, AVIF variants
stay; if it cannot, the AVIF encoder module is revisited then. AVIF is produced for every raster
image, and the runtime chooses: `CMP-030` asks for AVIF "where
the runtime supports it", which is a runtime decision (P3). SVG compilation to `vector_graphics`
(`CMP-031`) waits for P3, where its Dart consumer exists; font subsetting (`CMP-032`, a `SHOULD`)
for P8, where the locales that decide the script ranges exist.

## Consequences

- **Positive:** reproducible, cgo-free binaries; a codec crash is a failed job, not a crashed
  server; uploads are fast; the CLI can transcode offline with the same bytes.
- **Negative:** WebAssembly encoding is slower than native — AVIF most of all — which the job queue
  absorbs; the committed modules (3.7 MB and 0.35 MB) grow the repository and the binaries;
  rebuilding them needs clang, wasi-libc, CMake and Ninja.
- **Follow-up:** a CI job for `make wasm-codecs-check` (a new gate, for the maintainer to approve);
  asset IDs are unique per app, so `GetAsset` by ID picks the oldest match when an export was
  imported into a second app of the same organisation, as `GetComponent` and `GetTemplate` do —
  an optional app ID on those requests would remove the ambiguity.

## Options in detail

### Option 2 — cgo

The fastest encoders, but it breaks the reproducible, static, distroless build and puts C parsers
in the server's own memory.

### Option 3 — external processes

Keeps the Go build clean, but adds binaries to the image, process management and a larger attack
surface, and makes the output depend on whichever versions the image carries.

### Option 4 — pure Go

No maintained lossy WebP or AVIF encoder exists in Go; writing one is out of proportion to P2.

## Revision (2026-09-28, P3: SVG and assets on the device)

### SVG to `vector_graphics` (`CMP-031`)

`vector_graphics`' binary format has one encoder, `vector_graphics_compiler`, written in
Dart; the Go worker cannot call it. The maintainer chose to run it **in the worker as a Dart
helper process** (option (a) of the P3 plan), over porting the encoder to Go (large, with a
fidelity risk) and over deferring `CMP-031` with `flutter_svg` on the device (which parses
raw SVG on the device, contrary to `CMP-031`).

- **`plux-svgc`**, a small Dart program in the workspace, reads one SVG on standard input
  and writes the `.vec` encoding on standard output, with the masking, clipping and
  overdraw optimisers on. It is compiled ahead of time (`dart compile exe`) into a native
  executable that needs no Dart SDK at run time. The worker runs it per SVG with a time and
  output limit, as it runs the WebAssembly codecs per image, and stores the result as a
  variant of the asset (`image/vnd.plux.vector-graphics`); a failure marks the asset
  `failed` with a diagnostic, as for raster images.
- **Its native dependency.** The optimisers call Skia's path operations through FFI
  (`libpath_ops`). Flutter's engine ships a prebuilt copy; Plux instead **builds it from
  source**, like `flatc` and the codecs: the engine's 90-line `path_ops.cc` wrapper and the
  61 Skia sources it needs (32 in `src/pathops`, 27 in `src/core`, 2 in `src/ports`), from the Skia revision the
  pinned Flutter uses (`8df24be66531469e576a806749a0202ae26b8d08`, BSD-3-Clause, fetched
  from its GitHub mirror), compiled with clang at `-O2` with hidden visibility, section
  garbage collection, identical-code folding and a static C++ runtime, so that it needs
  only glibc.
- **Determinism.** The encoder's output is a function of its input; two runs give
  identical bytes. The AOT executable is reproducible when built from the same path, which
  the image build fixes.
- **As built (R6d, 2026-09-29).** The library is built in the image build, for each target
  platform, on Debian 12 — the base of the server image — with that release's GCC 12
  (`packages/plux_svgc/native/build.sh`, same flags, no identical-code folding, which GNU ld
  lacks; 437 KiB): built on a newer distribution it needs glibc 2.38, which Debian 12
  (2.36) does not have. A GCC build's output matched the clang build's byte for byte on the
  test SVG.
  `plux_svgc` is outside the pub workspace, since the Dart SDK alone cannot resolve the
  workspace's Flutter packages; the image build runs its tests against the freshly built
  library before shipping both. The worker runs the helper with no environment, a 30 s
  time limit and a 16 MiB output limit; a malformed, slow or oversized SVG fails its asset
  with a diagnostic, and a server without the helper (`assets.svgCompiler`) fails SVG
  assets the same way instead of retrying.

**Size, measured on 2026-09-28 (linux/amd64):**

| Part | Size | Compressed (gzip -9) |
|---|---|---|
| `plux-svgc` (AOT executable; a Dart "hello world" is 6.2 MiB of it) | 7.04 MiB | 2.87 MiB |
| `libpath_ops.so` built from source (Flutter's prebuilt: 484 KiB) | 374 KiB | 170 KiB |
| Runtime base `distroless/static-debian12` → `distroless/base-nossl-debian12` (glibc, which the Dart runtime needs) | +12.5 MiB (3.0 → 15.5 MiB) | +4.7 MiB (0.7 → 5.4 MiB) |
| **Total added to the server image** | **≈ 19.9 MiB** | **≈ 7.7 MiB** |

The Go binary stays static and cgo-free (`CI-006`); only the image base changes, to one
with glibc and still no shell or package manager (`SEC-108`). Every role runs from the one
server image (`SRV-001`), so api replicas carry the helper without using it. A separate
worker image would save the 7.7 MiB there at the cost of a second image to build, sign and
document; the maintainer kept one image for all roles (2026-09-29).

### Assets on the device (`AST-001`, `AST-002`, `RT-014`)

- Asset bytes are **not** inside bundles: the `assets-index` section names each asset and
  variant by SHA-256, and the api role serves asset objects by that address at
  `/v1/objects/assets/…` with the same immutable caching and range support as bundles
  (`REL-024`). A device downloads each referenced asset once per release set, checks it
  against the hash in the signed bundle, and stores it content-addressed, shared by every
  plugin (`AST-001`, ADR-0021). `plux pull` writes them into the baseline too.
- **Which variant.** The runtime chooses per asset: the `vector_graphics` variant for SVG;
  for raster images the density nearest above the device pixel ratio, as AVIF when the
  platform decoder supports it (Android 12+, iOS 16+) and WebP otherwise. Support is
  reported in the device's registration features, which also feed `REL-080`.
- **Remote images** (`AST-002`, `RT-014`) are fetched by the runtime's image provider,
  decoded at their laid-out size (`cacheWidth`/`cacheHeight`), cached in memory and on disk
  within the limits registry's bounds, shown with a ThumbHash or BlurHash placeholder when
  the document gives one and an error image on failure, and restricted to the plugin's
  declared network domains when the app pins them.
