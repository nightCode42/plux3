# 0003. Section-level deltas with zstd raw-dictionary patches

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `REL-020`–`REL-025`, `BND-007`, `BND-014`, `NFR-005`, `SYN-010`, `QA-002`

## Context and problem

Devices update over mobile networks, often slow ones (§30.1), and the runtime brings every
plugin up to date at app start (ADR-0021). The server must therefore be able to serve a
delta from **every** older version of a bundle to the newest one (`REL-020`), and a single
text change in one page must cost at most 2 KiB on the wire (`NFR-005`). What produces
those deltas, and at what granularity?

## Decision drivers

- Smallest realistic payload for the common case: one page, one string, one style changed.
- A patcher that the Flutter runtime can run on a phone, off the UI isolate (L-6), with a
  library it already links: the bundle transport is zstd (`BND-007`).
- Content-addressed, immutable, CDN-cacheable artifacts (`REL-024`).
- Deltas from arbitrary old versions, computed on demand and cached (`REL-022`).
- A round-trip that is provable byte for byte (`REL-025`, `QA-002`).

## Considered options

1. **Per-section deltas: unchanged sections referenced by hash, new sections shipped whole, changed sections shipped as zstd patches against the old section used as a raw dictionary.**
2. One binary patch over the whole container, with bsdiff.
3. One binary patch over the whole container, with xdelta3 (VCDIFF).
4. HDiffPatch over the whole container.

## Decision

Chosen option: **1**, as the specification requires (`REL-021`).

### Format

A delta is a small header plus a list of instructions, one per section of the **new**
bundle, in directory order:

| Instruction | Meaning |
|---|---|
| `reuse` | the old bundle has a section with this kind, ID and hash — take its bytes |
| `whole` | the section is new or unrelated; its zstd-compressed bytes follow |
| `patch` | a zstd frame that decodes to the new section with the old section as a raw dictionary |

The header carries the format version, the old and new bundle hashes, and the section
count. The device reconstructs the container by laying the resulting sections out with the
ordinary encoder and then checks the bundle hash against the manifest (`REL-030`): a delta
is never trusted, only verified (`SEC-052`). `PLX-3011` reports a mismatch after patching.

This works because sections reference each other only by stable ID, never by offset into
another section (`BND-014`), so an edit to one page changes one section and leaves the rest
byte-identical.

### Why zstd raw dictionaries

zstd's `--patch-from` is exactly "compress this file with that file as a raw content
dictionary". `klauspost/compress`, already a dependency for bundle transport (`BND-007`),
exposes it as `WithEncoderDictRaw` and `WithDecoderDictRaw` for content up to 2 GiB, far
above the 20 MiB plugin-bundle limit (`BND-010`). The device applies patches with the same
zstd it already uses, so no second patching library, no second FFI surface and no new
dependency on either side. An old version that is too different is detected by comparing
the patch with the whole-section compression and shipping whichever is smaller; if the
whole delta exceeds 60% of the full compressed bundle the server tells the device to fetch
the bundle instead (`REL-023`).

### Measurements

Measured on 2026-09-27 on the development machine, on bundles produced by the P1 compiler
from the `loan-calculator` conformance project, comparing this scheme with whole-container
binary patches:

| Change | Sections changed | This scheme | bsdiff | xdelta3 | Full bundle (zstd) |
|---|---|---|---|---|---|
| One string in one page | 2 of 9 | **135 B** | 451 B | 387 B | 3,400 B |
| One translated message | 1 of 8 | **39 B** | 290 B | 204 B | 2,209 B |

Section granularity wins because the patch is computed against 1–3 KiB of related bytes
rather than against the whole container, and because unchanged sections cost a 72-byte
directory entry rather than any payload at all. Both cases are an order of magnitude below
the 2 KiB budget of `NFR-005`. The comparison is re-run as a benchmark in
`docs/benchmarks/p2-backend.md` on the reference deployment.

### Generation and caching

Deltas from the last *K* versions (default 10) and from the *N* versions with the most
active installs (default 5) are generated as jobs at publish time; any other pair is
generated on first request, de-duplicated across concurrent requests by a single-flight
marker in the cache, and stored (`REL-022`). Every delta is content-addressed by the hash
of its own bytes and served with `Cache-Control: public, max-age=31536000, immutable` and
range support (`REL-024`).

### Verification

A property test generates bundle pairs — sections added, removed, reordered, grown, shrunk
and left alone — and asserts `apply(delta(a, b), a) == b` byte for byte, including the
bundle hash (`REL-025`, `QA-002`). The patch applier is fuzzed against arbitrary bytes and
must never panic or allocate beyond the declared size (`QA-004`).

## Consequences

- **Positive:** the smallest payload of the four options; no new dependency on server or device; unchanged sections cost nothing; a delta is verifiable against a hash the manifest already signs.
- **Negative:** the delta format is Plux's own, so it must be versioned and evolved additively like the bundle format (`BND-000`); section-level granularity means a compiler change that renumbers or merges sections makes old deltas useless, which the determinism rules of `CMP-002` already prevent.
- **Follow-up:** the runtime's patcher lands in P3 with sync; the cross-implementation check that `klauspost` output decodes under the reference zstd implementation is part of the delta package's tests.

## Options in detail

### Option 2 — bsdiff

Produces good deltas on executables and needs no dictionary support, but it costs memory
proportional to the input during **application** (its suffix-array reconstruction), which is
the wrong side to be expensive on a phone, and it adds a patching library to the runtime.
Measured larger than section patches on both test cases.

### Option 3 — xdelta3 (VCDIFF)

A standard format with a small, fast applier. Competitive on the whole container, but still
transfers a patch covering sections that did not change, and it adds a second compression
dependency to the device. Kept in the benchmark as the comparison point.

### Option 4 — HDiffPatch

The strongest of the whole-file patchers, with a low-memory applier. Its advantages appear
on large, heavily rewritten files; Plux bundles are small and mostly untouched between
versions, which is precisely where per-section reuse dominates. Rejected for the dependency
it would add on both sides for no measured gain.
