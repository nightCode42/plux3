# 0027. Asset pipeline: uploads, WebAssembly image codecs and dotLottie

- **Status:** Accepted
- **Date:** 2026-09-28
- **Requirements:** `SRV-060`, `CMP-030`, `CMP-033`, `AST-003`; deferred: `CMP-031` (P3), `CMP-032` (P8)

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
- **Publish.** The compiler lists each asset's variants in the `assets-index` section — a new,
  additive `variants` field of `Asset` in the bundle IDL — from what the pipeline recorded, and
  checks `asset.fileSize` per file and `plugin.assetBytes` per plugin (`AST-003`).
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
