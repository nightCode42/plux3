// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The bundle container of spec Appendix B.1 (ADR-0002): a fixed header, a
/// section directory and 8-byte-aligned sections, each an independent
/// FlatBuffers buffer read with the accessors under `fbs/`.
///
/// This reader checks the container's structure and returns sections as
/// views of the input, without copying (BND-003). The hash and FlatBuffers
/// checks the runtime needs before it reads a section arrive with the
/// runtime in P3 (SEC-052, BND-006); until then it serves tests and tools.
library;

import 'dart:typed_data';

/// A malformed container (PLX-3040).
final class MalformedBundle implements Exception {
  /// Creates the error.
  const MalformedBundle(this.message);

  /// What is wrong.
  final String message;

  @override
  String toString() => 'PLX-3040 BUNDLE_MALFORMED: $message';
}

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

  /// Checks [data] and returns its container; throws [MalformedBundle].
  factory BundleContainer.parse(Uint8List data) {
    const headerSize = 48, entrySize = 72;
    final view = ByteData.sublistView(data);
    if (data.length < headerSize ||
        String.fromCharCodes(data, 0, 4) != 'PLUX') {
      throw const MalformedBundle('not a Plux bundle');
    }
    if (view.getUint16(4, Endian.little) != 1) {
      throw const MalformedBundle('unknown container version');
    }
    final kind = view.getUint16(6, Endian.little),
        flags = view.getUint32(8, Endian.little);
    if (kind < 1 || kind > 3 || flags & ~3 != 0) {
      throw const MalformedBundle('unknown bundle kind or flags');
    }
    final count = view.getUint32(12, Endian.little);
    if (count > (data.length - headerSize) ~/ entrySize) {
      throw const MalformedBundle('the directory does not fit');
    }
    var end = headerSize + entrySize * count;
    final sections = <Section>[];
    for (var i = 0; i < count; i++) {
      final e = headerSize + entrySize * i;
      final sectionKind = view.getUint16(e + 16, Endian.little);
      final offset = view.getUint64(e + 20, Endian.little),
          length = view.getUint64(e + 28, Endian.little);
      if (sectionKind == 0 ||
          offset % 8 != 0 ||
          offset < end ||
          offset > data.length ||
          length > data.length - offset) {
        throw MalformedBundle('directory entry $i does not fit');
      }
      final id = Uint8List.sublistView(data, e, e + 16);
      if (sections.isNotEmpty &&
          _compare(sections.last, sectionKind, id) >= 0) {
        throw MalformedBundle('directory entry $i is out of order');
      }
      for (var p = end; p < offset; p++) {
        if (data[p] != 0) {
          throw MalformedBundle('non-zero bytes before section $i');
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
      throw const MalformedBundle('bytes after the last section');
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
