// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Verification of whole bundles before they enter the release store, and
/// of single sections before their first use (SEC-052, BND-006, ADR-0029).
library;

import 'dart:isolate';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/verify/flatbuffers_verifier.dart';

/// The verifier's limits from the limits registry (LIM-001).
final class VerifierLimits {
  /// Creates limits.
  const VerifierLimits({required this.maxDepth, required this.maxVisits});

  /// `bundle.verifierDepth`.
  final int maxDepth;

  /// `bundle.verifierTables`.
  final int maxVisits;
}

/// Checks a whole bundle in the order of ADR-0029 — structure, bundle hash
/// against [expectedHash], every section hash, the FlatBuffers verifier on
/// every section this runtime knows, the encrypted flag and the required
/// features — and returns its container. Unknown section kinds are skipped
/// (BND-018); a bundle that needs one names it in its required features,
/// which [supportsFeature] refuses (BND-008). [fromDelta] selects the code
/// of a hash mismatch: `PLX-3011` after patching, `PLX-3040` otherwise.
BundleContainer verifyBundle(
  Uint8List data,
  Uint8List expectedHash, {
  required VerifierLimits limits,
  required bool Function(String feature) supportsFeature,
  bool fromDelta = false,
}) {
  final b = BundleContainer.parse(data);
  if (!_equal(bundleHash(data, b.sections.length), expectedHash)) {
    throw PluxException(
      fromDelta
          ? PluxErrorCode.patchHashMismatch
          : PluxErrorCode.bundleMalformed,
      'the bundle hash is not the signed one',
    );
  }
  if (b.flags & 1 != 0) {
    throw const PluxException(
      PluxErrorCode.bundleEncryptedUnsupported,
      'confidential bundles arrive in P6',
    );
  }
  for (final s in b.sections) {
    checkSectionHash(s);
    if (sectionIdentifiers.containsKey(s.kind)) {
      verifySection(
        s.kind,
        s.data,
        maxDepth: limits.maxDepth,
        maxVisits: limits.maxVisits,
      );
    }
  }
  final metas = b.ofKind(SectionKind.meta).toList();
  if (metas.length != 1) {
    throw const PluxException(
      PluxErrorCode.bundleMalformed,
      'a bundle has exactly one meta section',
    );
  }
  for (final f
      in fbs.Meta(metas.single.data).requiredFeatures ?? const <String>[]) {
    if (!supportsFeature(f)) {
      throw PluxException(
        PluxErrorCode.unsupportedRequiredFeature,
        'the bundle requires $f',
        details: {'feature': f},
      );
    }
  }
  return b;
}

/// Checks that a section's SHA-256 is the one in the directory (BND-005).
void checkSectionHash(Section s) {
  if (!_equal(Uint8List.fromList(sha256.convert(s.data).bytes), s.hash)) {
    throw PluxException(
      PluxErrorCode.sectionHashMismatch,
      'section of kind ${s.kind} does not match its directory hash',
    );
  }
}

/// Remembers which sections passed their first-use check, by section hash,
/// for the life of one mapping (ADR-0029): a section is hashed and
/// verified once, the first time a page or component reads it.
final class SectionGate {
  /// Creates a gate with the verifier's [limits].
  SectionGate(this.limits);

  /// The verifier's limits.
  final VerifierLimits limits;

  final Set<String> _passed = {};

  /// Checks [s] unless it passed before; throws [PluxException].
  void check(Section s) {
    final key = String.fromCharCodes(s.hash);
    if (_passed.contains(key)) return;
    checkSectionHash(s);
    if (sectionIdentifiers.containsKey(s.kind)) {
      verifySection(
        s.kind,
        s.data,
        maxDepth: limits.maxDepth,
        maxVisits: limits.maxVisits,
      );
    }
    _passed.add(key);
  }

  /// Whether [s] has passed.
  bool passed(Section s) => _passed.contains(String.fromCharCodes(s.hash));

  /// The largest section checked on the UI isolate (layering rule L-6);
  /// larger ones are checked by [prepare].
  static const int uiIsolateLimit = 64 * 1024;

  /// Checks, on a background isolate, every section of [sections] larger
  /// than [uiIsolateLimit] that has not passed (ADR-0029); null when there
  /// is none. Completes with an error, a [PluxException], when one fails.
  Future<void>? prepare(Iterable<Section> sections) {
    final pending = [
      for (final s in sections)
        if (s.data.length > uiIsolateLimit && !passed(s)) s,
    ];
    if (pending.isEmpty) return null;
    final limits = this.limits;
    final work = [
      for (final s in pending)
        (s.kind, s.id, s.hash, Uint8List.fromList(s.data)),
    ];
    return Isolate.run(() {
      for (final (kind, id, hash, data) in work) {
        checkSectionHash(Section(kind, id, hash, data));
        if (sectionIdentifiers.containsKey(kind)) {
          verifySection(
            kind,
            data,
            maxDepth: limits.maxDepth,
            maxVisits: limits.maxVisits,
          );
        }
      }
    }).then((_) {
      for (final s in pending) {
        _passed.add(String.fromCharCodes(s.hash));
      }
    });
  }
}

bool _equal(Uint8List a, Uint8List b) {
  if (a.length != b.length) return false;
  var d = 0;
  for (var i = 0; i < a.length; i++) {
    d |= a[i] ^ b[i];
  }
  return d == 0;
}
