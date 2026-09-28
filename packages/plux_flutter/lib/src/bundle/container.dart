// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The bundle container of spec Appendix B.1 (ADR-0002): a fixed header, a
/// section directory and 8-byte-aligned sections, each an independent
/// FlatBuffers buffer read with the accessors under `fbs/`.
///
/// This reader checks the container's structure and returns sections as
/// views of the input, without copying (BND-003). It reads nothing but the
/// header and the directory: the hashes and the FlatBuffers verifier of
/// `verify/` run before any section is read (SEC-052, BND-006).
/// [encodeBundle] lays sections out exactly as the Go encoder does, so a
/// bundle rebuilt from a delta has the bundle hash the server signed.
library;

import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// A malformed container (PLX-3040).
PluxException _malformed(String message) =>
    PluxException(PluxErrorCode.bundleMalformed, message);

/// Section kinds; the codes are permanent (ADR-0002).
abstract final class SectionKind {
  /// Identity, versions and required features.
  static const int meta = 1;

  /// One page.
  static const int page = 2;

  /// One component.
  static const int component = 3;

  /// Action graphs.
  static const int actions = 4;

  /// PXL programs.
  static const int pxl = 5;

  /// Deduplicated styles and design tokens.
  static const int styles = 6;

  /// The shared string table.
  static const int strings = 7;

  /// One locale's messages.
  static const int l10n = 8;

  /// Animation timelines.
  static const int timelines = 9;

  /// Types, state, data sources and collections.
  static const int schemas = 10;

  /// Asset references.
  static const int assetsIndex = 11;

  /// A device-placed function module.
  static const int wasm = 12;

  /// The source map of a development bundle.
  static const int sourceMap = 13;
}

/// One section: its kind, 16-byte ID, SHA-256 from the directory, and its
/// bytes as a view of the container.
final class Section {
  /// Creates a section.
  const Section(this.kind, this.id, this.hash, this.data);

  /// The kind.
  final int kind;

  /// The ID within its kind.
  final Uint8List id;

  /// The SHA-256 of [data] recorded in the directory.
  final Uint8List hash;

  /// The section, a view of the container.
  final Uint8List data;
}

/// A container whose structure has been checked.
final class BundleContainer {
  BundleContainer._(this.kind, this.flags, this.hash, this.sections);

  /// Checks [data] and returns its container; throws a [PluxException]
  /// with `PLX-3040`.
  factory BundleContainer.parse(Uint8List data) {
    const headerSize = 48, entrySize = 72;
    final view = ByteData.sublistView(data);
    if (data.length < headerSize ||
        String.fromCharCodes(data, 0, 4) != 'PLUX') {
      throw _malformed('not a Plux bundle');
    }
    if (view.getUint16(4, Endian.little) != 1) {
      throw _malformed('unknown container version');
    }
    final kind = view.getUint16(6, Endian.little),
        flags = view.getUint32(8, Endian.little);
    if (kind < 1 || kind > 3 || flags & ~3 != 0) {
      throw _malformed('unknown bundle kind or flags');
    }
    final count = view.getUint32(12, Endian.little);
    if (count > (data.length - headerSize) ~/ entrySize) {
      throw _malformed('the directory does not fit');
    }
    var end = headerSize + entrySize * count;
    final sections = <Section>[];
    for (var i = 0; i < count; i++) {
      final e = headerSize + entrySize * i;
      final sectionKind = view.getUint16(e + 16, Endian.little);
      final offset = view.getUint64(e + 20, Endian.little),
          length = view.getUint64(e + 28, Endian.little);
      // getUint64 reads a value above 2^63 as negative.
      if (sectionKind == 0 ||
          offset < 0 ||
          length < 0 ||
          offset % 8 != 0 ||
          offset < end ||
          offset > data.length ||
          length > data.length - offset) {
        throw _malformed('directory entry $i does not fit');
      }
      final id = Uint8List.sublistView(data, e, e + 16);
      if (sections.isNotEmpty &&
          _compare(sections.last, sectionKind, id) >= 0) {
        throw _malformed('directory entry $i is out of order');
      }
      for (var p = end; p < offset; p++) {
        if (data[p] != 0) {
          throw _malformed('non-zero bytes before section $i');
        }
      }
      sections.add(
        Section(
          sectionKind,
          id,
          Uint8List.sublistView(data, e + 36, e + 68),
          Uint8List.sublistView(data, offset, offset + length),
        ),
      );
      end = offset + length;
    }
    if (end != data.length) {
      throw _malformed('bytes after the last section');
    }
    return BundleContainer._(
      kind,
      flags,
      Uint8List.sublistView(data, 16, 48),
      sections,
    );
  }

  static int _compare(Section a, int kind, Uint8List id) {
    if (a.kind != kind) return a.kind.compareTo(kind);
    for (var i = 0; i < 16; i++) {
      if (a.id[i] != id[i]) return a.id[i].compareTo(id[i]);
    }
    return 0;
  }

  /// 1 plugin, 2 app, 3 development.
  final int kind;

  /// Bit 0 encrypted, bit 1 has a source map.
  final int flags;

  /// The bundle hash from the header (BND-005).
  final Uint8List hash;

  /// The sections in directory order.
  final List<Section> sections;

  /// The sections of [kind].
  Iterable<Section> ofKind(int kind) => sections.where((s) => s.kind == kind);
}

/// Bundle kinds (Appendix B.1).
abstract final class BundleKinds {
  /// A plugin bundle.
  static const int plugin = 1;

  /// The app bundle.
  static const int app = 2;

  /// A development bundle.
  static const int development = 3;
}

/// The size of the container header.
const int bundleHeaderSize = 48;

/// The size of one directory entry.
const int bundleEntrySize = 72;

/// The bundle hash of a container with [count] sections: SHA-256 of bytes
/// 0–15 followed by the section directory (BND-005). It reads only the
/// header and directory, which [BundleContainer.parse] has bounded.
Uint8List bundleHash(Uint8List data, int count) {
  final sink = _Collect();
  final input = sha256.startChunkedConversion(sink)
    ..add(Uint8List.sublistView(data, 0, 16))
    ..add(
      Uint8List.sublistView(
        data,
        bundleHeaderSize,
        bundleHeaderSize + bundleEntrySize * count,
      ),
    );
  input.close();
  return Uint8List.fromList(sink.digest!.bytes);
}

/// Lays [sections] out in a container of [kind], ordered by kind and ID,
/// exactly as the Go encoder does (ADR-0002), and returns its bytes. Each
/// section's `hash` is ignored and recomputed.
Uint8List encodeBundle(int kind, List<Section> sections) {
  if (kind < BundleKinds.plugin || kind > BundleKinds.development) {
    throw ArgumentError.value(kind, 'kind', 'unknown bundle kind');
  }
  final sorted = [...sections]..sort(_order);
  var flags = 0;
  for (var i = 0; i < sorted.length; i++) {
    if (i > 0 && _order(sorted[i - 1], sorted[i]) == 0) {
      throw ArgumentError('duplicate section of kind ${sorted[i].kind}');
    }
    if (sorted[i].kind == SectionKind.sourceMap) flags |= 2;
  }
  final dirEnd = bundleHeaderSize + bundleEntrySize * sorted.length;
  final offsets = <int>[];
  var end = dirEnd;
  for (final s in sorted) {
    offsets.add(_align(end));
    end = offsets.last + s.data.length;
  }
  final out = Uint8List(end);
  final view = ByteData.sublistView(out);
  out.setAll(0, 'PLUX'.codeUnits);
  view
    ..setUint16(4, 1, Endian.little)
    ..setUint16(6, kind, Endian.little)
    ..setUint32(8, flags, Endian.little)
    ..setUint32(12, sorted.length, Endian.little);
  for (var i = 0; i < sorted.length; i++) {
    final s = sorted[i], e = bundleHeaderSize + bundleEntrySize * i;
    out.setAll(e, s.id);
    view
      ..setUint16(e + 16, s.kind, Endian.little)
      ..setUint64(e + 20, offsets[i], Endian.little)
      ..setUint64(e + 28, s.data.length, Endian.little);
    out.setAll(e + 36, sha256.convert(s.data).bytes);
    out.setAll(offsets[i], s.data);
  }
  out.setAll(16, bundleHash(out, sorted.length));
  return out;
}

int _align(int n) => (n + 7) & ~7;

int _order(Section a, Section b) {
  if (a.kind != b.kind) return a.kind.compareTo(b.kind);
  for (var i = 0; i < 16; i++) {
    if (a.id[i] != b.id[i]) return a.id[i].compareTo(b.id[i]);
  }
  return 0;
}

/// Collects the digest of a chunked conversion.
final class _Collect implements Sink<Digest> {
  Digest? digest;

  @override
  void add(Digest data) => digest = data;

  @override
  void close() {}
}
