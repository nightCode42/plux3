# 0030. Native code in `plux_flutter`: memory maps and zstd over FFI

- **Status:** Accepted
- **Date:** 2026-09-28
- **Requirements:** `RT-010`, `RT-061`, `NFR-009`, `SYN-010`, `SYN-011`, `BND-007`, `REL-021`, `QA-002`, `QA-004`, `SEC-054`

## Context and problem

Two things the runtime must do have no pure-Dart answer. Bundles must be read **zero-copy
from memory-mapped files** (`RT-010`), and Dart has no `mmap`. Deltas must be **applied on
the device** (ADR-0003): a changed section is a zstd frame compressed with the old section
as a raw-content dictionary, and no maintained pure-Dart zstd decoder supports raw
dictionaries reliably. Both need native code inside every host app. How is that code
sourced, built for Android and iOS, bound to Dart, tested and kept small?

## Decision drivers

- Zero-copy access from Dart to mapped bytes (`RT-010`).
- Byte-for-byte agreement with the server's delta encoder (`QA-002`) and bounded,
  bomb-proof decoding (`QA-004`).
- Nothing to install for host developers beyond `flutter pub add`; no prebuilt binaries in
  the package (reproducible from source).
- A small size cost, counted against the 3 MiB budget (`RT-061`).
- Code that is tested on every change, on the Linux host as well as on devices.

## Considered options

1. **One C library, `plux_native`, built from source by a Dart build hook (native assets) with `native_toolchain_c`, bound with `@Native` FFI: a vendored decompression-only zstd plus a few lines of `mmap` glue.**
2. A Flutter plugin with Kotlin and Swift code for each platform (platform channels).
3. Prebuilt static libraries per ABI checked into the package.
4. A pure-Dart zstd decoder (existing package or written in house) and `RandomAccessFile` reads instead of maps.

## Decision

Chosen option: **1**.

### What is native

| Function | Purpose |
|---|---|
| `plux_map(path, &length)` / `plux_unmap(address, length)` | `open`, `fstat`, `mmap(PROT_READ, MAP_PRIVATE)`, `close`; the file stays mapped after the descriptor closes |
| `plux_zstd_frame_size(src, n)` | the frame's declared content size, or an error; a frame that declares none is refused (`BND-007`) |
| `plux_zstd_decompress(dst, cap, src, n, dict, dictLength)` | one-shot decompression into a buffer of the declared size, with an optional raw-content dictionary (`ZSTD_DCtx_refPrefix`) |

Everything else — the delta format, bounds, hashes and the container — stays in Dart, so
the native surface is three calls with no state and no allocation beyond zstd's own
decoding context. The runtime never passes downloaded code to native code (`SEC-054`);
these functions take bytes and return bytes.

### zstd

The C sources of zstd **v1.5.7** (tag commit `f8745da6ff1ad1e7bab384bd1f9d742439278e99`,
BSD-3-Clause) are vendored under `packages/plux_flutter/native/zstd/` — only `lib/common`
and `lib/decompress`, with the legacy formats, dictionary builder, compressor, assembly
(`ZSTD_DISABLE_ASM`) and multithreading left out. `native/zstd/VERSION` records the tag and
commit and a script re-imports and checks them. Decoding is one-shot, straight into a
destination buffer of the size the frame declares, which Dart checks against the limits
before it allocates; zstd itself allocates only its fixed-size decoding context, so a
hostile frame cannot make the decoder allocate more.

Section deltas are produced by `klauspost/compress` on the server with the old section as
a raw dictionary (ADR-0003). Its frames are standard zstd frames; the Go delta tests
generate conformance vectors into `schema/testdata/delta` — old bundle, new bundle, delta —
and the Dart applier must rebuild every new bundle byte for byte, and reject every
corrupted and truncated delta in the vector set with `PLX-3012` or `PLX-3011`.

### Memory maps

A mapped file becomes a `Uint8List` with `Pointer<Uint8>.asTypedList`, which wraps the
mapping without copying. The mapping is released explicitly when the last page lease on
its release ends (ADR-0021); a `NativeFinalizer` on the owning object unmaps it if the
object is collected first, so a leak is bounded by the garbage collector. A view is never
used after release: views are handed out only through the owning object, which throws once
released. `MAP_PRIVATE` with `PROT_READ` means a change to the file after mapping cannot
change bytes that were already verified in a way that silently corrupts the store; the
first-use checks of ADR-0029 cover what is read.

Android and iOS are POSIX; so are the Linux and macOS hosts that run `flutter test`. Windows
hosts are not supported for running the runtime's tests (development of `plux_flutter` on
Windows uses WSL, as the rest of the repository does).

### Build

`packages/plux_flutter/hook/build.dart` compiles `native/plux_native.c` and the zstd
sources with `CBuilder.library` for every target the Flutter tool asks for (Android
`arm64-v8a`, `armeabi-v7a`, `x86_64`; iOS device and simulator; the host for tests), with
`-O2`, hidden visibility, `-ffunction-sections -fdata-sections` and
`--gc-sections`, and on Android a 16 KiB page alignment (`-Wl,-z,max-page-size=16384`)
as Google Play requires. Dart binds the functions with `@Native` and the asset ID
`package:plux_flutter/src/native/plux_native.dart`; calls are `isLeaf` and pass
`Uint8List`s directly, so no marshalling package is needed.

The hook's own packages are Dart-team packages used only at build time and never imported
by `lib/`, so they add nothing to the app: `hooks` 2.2.0, `code_assets` 1.2.1 and
`native_toolchain_c` 0.19.3 (BSD-3-Clause), with their transitive build-time packages. They
are the newest versions compatible with `cupertino_http` 3.1.0, which pins `code_assets` 1.x.

### Size (`RT-061`)

The decompression-only zstd with the glue is expected at 90–120 KiB per ABI for
`arm64-v8a`; the measured figure is recorded in `docs/benchmarks/p3-runtime.md` and the CI
size job fails the whole package above 3 MiB.

### Tests

The same Dart tests run on the Linux host in `make dart-cover` (the hook builds the library
for the host) and on the Android emulator and iOS simulator in the CI device jobs. A fuzz
test feeds random and mutated frames and deltas to the decoder and requires a typed error,
never a crash or an allocation above the declared size.

## Consequences

- **Positive:** zero-copy reads and on-device patching with one small library, built from
  pinned source for every target, with no prebuilt binaries and no platform-channel
  round-trips; the server's and device's zstd are cross-checked by shared vectors.
- **Negative:** host developers need the C toolchain Flutter already requires for Android
  and iOS builds; vendored zstd must be updated by hand (security advisories are watched
  through the upstream repository); native code is memory-unsafe, so its surface is kept to
  three functions and fuzzed.
- **Follow-up:** the P7 device-function interpreter is a separate optional package
  (`plux_functions`, `RT-060`) and does not reuse this library.

## Options in detail

### Option 2 — platform channels

Kotlin and Swift implementations would be two code paths to keep equal, every call would
be an asynchronous message copying its bytes, and neither can hand Dart a zero-copy view of
a mapping. Platform channels remain the right tool for platform services (key storage,
ADR-0029; background scheduling, ADR-0021) and are used only for those.

### Option 3 — prebuilt libraries

Faster host builds, but binaries in a published package cannot be reproduced or reviewed
from source, and every toolchain or ABI change needs a manual rebuild.

### Option 4 — pure Dart

A Dart zstd decoder would be several thousand lines of performance-critical code of our
own, slower on the device than the C reference, and still would not give zero-copy maps;
`RandomAccessFile` reads copy every section into the Dart heap.

## Revision (2026-10-05)

*A view is never used after release* did not hold. Decoded bundle objects keep views into
the mapping, and from P5 the app's and the plugins' triggers and detached runs outlive the
page leases of their release: after an update replaced the active release, such an object
read the unmapped file, which crashed the add-to-app hosts' online flow (P5 batch 1, CI run
37284811902). Leases now cover release owners and every run (`ActiveRelease.hold`), and,
so that no remaining path can read freed memory, the view owns the mapping: the bytes are
created with `asTypedList(finalizer:, token:)` over `plux_unmap`, `MappedFile.release`
only gives the file up (`bytes` throws afterwards), and the file is unmapped when the last
view becomes unreachable. The leak bound is unchanged — the garbage collector — and a
retired release's address space is returned once nothing references it.
