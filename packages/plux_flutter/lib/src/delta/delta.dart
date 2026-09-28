// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Applies section-level bundle deltas (ADR-0003) exactly as the server's
/// `backend/internal/delta` does, checked against its conformance vectors
/// (`schema/testdata/delta`). A delta is never trusted: the rebuilt bundle
/// must have the bundle hash the delta names, and the caller then checks
/// it against the signed manifest (SYN-011).
library;

import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/zstd/zstd.dart';

const _magic = 'PXDL';
const _version = 1;
const _headerSize = 80;
const _instrSize = 32;
const _hashSize = 32;
const _maxSections = 1 << 16;

/// What a delta connects.
final class DeltaHeader {
  const DeltaHeader._(this.kind, this.sections, this.oldHash, this.newHash);

  /// Reads the 80-byte header of [delta]; throws [PluxException] with
  /// `PLX-3012` when it is not a delta this runtime understands.
  factory DeltaHeader.read(Uint8List delta) {
    if (delta.length < _headerSize ||
        String.fromCharCodes(delta, 0, 4) != _magic) {
      throw _malformed('not a delta');
    }
    final view = ByteData.sublistView(delta);
    final version = view.getUint16(4, Endian.little);
    if (version != _version) {
      throw _malformed('version $version is not supported');
    }
    if (view.getUint32(12, Endian.little) != 0) {
      throw _malformed('unknown flags');
    }
    final n = view.getUint32(8, Endian.little);
    if (n > _maxSections) throw _malformed('$n sections');
    return DeltaHeader._(
      view.getUint16(6, Endian.little),
      n,
      Uint8List.fromList(delta.sublist(16, 48)),
      Uint8List.fromList(delta.sublist(48, 80)),
    );
  }

  /// The new bundle's kind.
  final int kind;

  /// The number of sections of the new bundle.
  final int sections;

  /// The bundle hash of the base.
  final Uint8List oldHash;

  /// The bundle hash of the result.
  final Uint8List newHash;
}

/// Rebuilds the new bundle from [old] and [delta]. The rebuilt bundle may
/// have at most [maxSize] bytes of sections; each section is decoded into a
/// buffer of its declared size, so a hostile delta cannot make this
/// allocate beyond it. Throws [PluxException] with `PLX-3012` (the delta is
/// malformed) or `PLX-3011` (the result is not the bundle it names).
Uint8List applyDelta(Uint8List old, Uint8List delta, int maxSize) {
  final h = DeltaHeader.read(delta);
  final base = BundleContainer.parse(old);
  if (!_equal(bundleHash(old, base.sections.length), h.oldHash)) {
    throw _mismatch('the delta was made against another bundle');
  }
  final byHash = {for (final s in base.sections) _key(s.hash): s.data};
  final sections = <Section>[];
  var at = _headerSize, budget = maxSize;
  final view = ByteData.sublistView(delta);
  for (var i = 0; i < h.sections; i++) {
    if (delta.length - at < _instrSize) {
      throw _malformed('truncated instruction');
    }
    final id = Uint8List.sublistView(delta, at, at + 16);
    final kind = view.getUint16(at + 16, Endian.little);
    final op = delta[at + 18];
    final size = view.getUint64(at + 20, Endian.little);
    final n = view.getUint32(at + 28, Endian.little);
    if (delta[at + 19] != 0) throw _malformed('reserved byte set');
    // getUint64 reads a size above 2^63 as negative.
    if (size < 0 || size > (budget < 0 ? 0 : budget)) {
      throw _malformed('the sections exceed $budget bytes');
    }
    budget -= size;
    at += _instrSize;
    if (delta.length - at < n) throw _malformed('truncated payload');
    final payload = Uint8List.sublistView(delta, at, at + n);
    at += n;
    final data = switch (op) {
      0 => _reuse(payload, byHash),
      1 => _decode(payload, null, size),
      2 => _patch(payload, byHash, size),
      _ => throw _malformed('unknown operation $op'),
    };
    if (data.length != size) {
      throw _mismatch('a section decoded to ${data.length} bytes, not $size');
    }
    sections.add(Section(kind, id, Uint8List(0), data));
  }
  if (at != delta.length) {
    throw _malformed('${delta.length - at} bytes after the last instruction');
  }
  final Uint8List out;
  try {
    out = _encode(h.kind, sections);
  } on ArgumentError catch (e) {
    throw _malformed('${e.message}');
  }
  if (!_equal(Uint8List.sublistView(out, 16, 48), h.newHash)) {
    throw _mismatch("the rebuilt bundle's hash is not the one the delta names");
  }
  return out;
}

/// Encodes like the Go encoder, which also refuses unknown section kinds
/// and a source map outside development bundles.
Uint8List _encode(int kind, List<Section> sections) {
  for (final s in sections) {
    if (s.kind < SectionKind.meta || s.kind > SectionKind.sourceMap) {
      throw ArgumentError('unknown section kind ${s.kind}');
    }
    if (s.kind == SectionKind.sourceMap && kind != BundleKinds.development) {
      throw ArgumentError('a source map outside a development bundle');
    }
  }
  return encodeBundle(kind, sections);
}

Uint8List _reuse(Uint8List payload, Map<String, Uint8List> byHash) {
  if (payload.length != _hashSize) {
    throw _malformed('a reuse instruction carries ${payload.length} bytes');
  }
  return byHash[_key(payload)] ??
      (throw _mismatch('a reused section is not in the old bundle'));
}

Uint8List _patch(Uint8List payload, Map<String, Uint8List> byHash, int size) {
  if (payload.length < _hashSize) throw _malformed('truncated patch');
  final base =
      byHash[_key(Uint8List.sublistView(payload, 0, _hashSize))] ??
      (throw _mismatch("the patch's base section is not in the old bundle"));
  return _decode(Uint8List.sublistView(payload, _hashSize), base, size);
}

Uint8List _decode(Uint8List frame, Uint8List? base, int size) {
  try {
    final declared = zstdContentSize(frame);
    if (declared != null && declared != size) {
      throw _malformed('the frame declares $declared bytes, the section $size');
    }
    return zstdDecompress(frame, capacity: size, dictionary: base);
  } on ZstdException catch (e) {
    throw _malformed('zstd: ${e.message}');
  }
}

/// The SHA-256 of [data], for callers that check sections and results.
Uint8List sha256Of(Uint8List data) =>
    Uint8List.fromList(sha256.convert(data).bytes);

String _key(Uint8List hash) => String.fromCharCodes(hash);

bool _equal(Uint8List a, Uint8List b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

PluxException _malformed(String message) =>
    PluxException(PluxErrorCode.deltaMalformed, message);

PluxException _mismatch(String message) =>
    PluxException(PluxErrorCode.patchHashMismatch, message);
