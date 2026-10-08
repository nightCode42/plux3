// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Certificate pinning of the Plux server (SEC-041): SPKI pins in the form
/// of RFC 7469 (the base64 of the SHA-256 of a certificate's
/// SubjectPublicKeyInfo), the set the runtime holds, and the rule for when
/// a configuration may go without pins. Pinning adds to the platform's
/// certificate validation and never replaces it.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart' show kReleaseMode;
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// The fewest distinct pins a set holds: the key in use and a backup key
/// not in use, so that a rotation never strands the app (SEC-041).
const minPins = 2;

/// Whether [pin] is the base64 of a SHA-256 digest, in its canonical form.
bool isWellFormedPin(String pin) {
  if (pin.length != 44) return false;
  try {
    final raw = base64.decode(pin);
    return raw.length == 32 && base64.encode(raw) == pin;
  } on FormatException {
    return false;
  }
}

/// [pins] as a set, after checking that each is well formed and that at
/// least [minPins] are distinct; a [PluxException] otherwise.
Set<String> validatedPins(Iterable<String> pins) {
  final set = <String>{};
  for (final p in pins) {
    if (!isWellFormedPin(p)) {
      // The value is not repeated: a malformed pin is no secret, but the
      // message stays free of anything that looks like key material.
      throw const PluxException(
        PluxErrorCode.invalidFormat,
        'PluxConfig.pins holds a value that is not the base64 of a SHA-256 '
        'SPKI hash (44 characters)',
      );
    }
    set.add(p);
  }
  if (set.length < minPins) {
    throw const PluxException(
      PluxErrorCode.missingProperty,
      'PluxConfig.pins needs at least two distinct pins: the key in use and '
      'a backup key that is not in use (SEC-041)',
    );
  }
  return set;
}

/// The pins of the Plux server a client enforces, with a version.
///
/// It starts as `PluxConfig.pins` and is replaced only from verified
/// update metadata (SEC-050), by [replace]: a replacement keeps at least
/// [minPins] distinct pins and its version must exceed the current one, so
/// older metadata cannot bring back a retired pin.
///
/// On Android and iOS a connection is accepted when any certificate of its
/// chain has a pinned key; on `dart:io` platforms only the leaf is visible,
/// so there the pins must name leaf keys and the backup pin the leaf key
/// that replaces the one in use.
final class PinSet {
  /// Creates a set of [pins] (see [validatedPins]) at [version].
  PinSet(Iterable<String> pins, {this._version = 0})
    : _pins = Set.unmodifiable(validatedPins(pins));

  Set<String> _pins;
  int _version;

  /// The pins in force.
  Set<String> get pins => _pins;

  /// The version of the metadata the pins came from; 0 for the build's.
  int get version => _version;

  /// Whether one of [hashes] is a pin of the set.
  bool matches(Iterable<String> hashes) => hashes.any(_pins.contains);

  /// Replaces the pins with those of verified metadata at [version].
  /// Throws a [PluxException] and changes nothing when the new pins are
  /// not valid or [version] is not newer.
  void replace(Iterable<String> pins, {required int version}) {
    if (version <= _version) {
      throw const PluxException(
        PluxErrorCode.outOfRange,
        'pin metadata is not newer than the pins held',
      );
    }
    _pins = Set.unmodifiable(validatedPins(pins));
    _version = version;
  }
}

/// The pin set to enforce for a server at [endpoint] configured with
/// [pins]; null when the app runs without pinning.
///
/// With pins, the set is validated whatever the build. Without, only a
/// build that is not a release build, or an endpoint that is not `https`
/// (a local development server), may run unpinned; a release build with an
/// `https` endpoint and no pins is refused, since an unpinned production
/// app is not a secure default (SEC-041). [releaseMode] is for tests.
PinSet? pinSetFor(
  Uri endpoint,
  List<String> pins, {
  bool releaseMode = kReleaseMode,
}) {
  if (pins.isNotEmpty) return PinSet(pins);
  if (!releaseMode || endpoint.scheme != 'https') return null;
  throw const PluxException(
    PluxErrorCode.missingProperty,
    'a release build with an https endpoint needs PluxConfig.pins: at least '
    'two SPKI SHA-256 pins of the Plux server (SEC-041)',
  );
}

/// The DER encoding of the SubjectPublicKeyInfo of the X.509 certificate
/// [der] (RFC 5280 §4.1); a [FormatException] when it is not one.
Uint8List subjectPublicKeyInfo(Uint8List der) {
  // Certificate ::= SEQUENCE { tbsCertificate, ... }
  final cert = _Tlv.at(der, 0);
  if (cert.tag != 0x30) throw const FormatException('not a certificate');
  // TBSCertificate ::= SEQUENCE { [0] version OPTIONAL, serialNumber,
  //   signature, issuer, validity, subject, subjectPublicKeyInfo, ... }
  final tbs = _Tlv.at(der, cert.contentStart);
  if (tbs.tag != 0x30) throw const FormatException('no TBSCertificate');
  var at = tbs.contentStart;
  if (_Tlv.at(der, at) case final v when v.tag == 0xA0) at = v.end;
  // serialNumber, signature, issuer, validity, subject.
  for (var i = 0; i < 5; i++) {
    at = _Tlv.at(der, at).end;
  }
  final spki = _Tlv.at(der, at);
  if (spki.tag != 0x30 || spki.end > tbs.end) {
    throw const FormatException('no SubjectPublicKeyInfo');
  }
  return Uint8List.sublistView(der, at, spki.end);
}

/// The pin of the certificate [der]: the base64 of the SHA-256 of its
/// SubjectPublicKeyInfo (RFC 7469 §2.4).
String spkiPin(Uint8List der) =>
    base64.encode(sha256.convert(subjectPublicKeyInfo(der)).bytes);

/// One DER element's position: tag, where its content starts and ends.
final class _Tlv {
  const _Tlv(this.tag, this.contentStart, this.end);

  factory _Tlv.at(Uint8List der, int offset) {
    if (offset < 0 || offset + 2 > der.length) {
      throw const FormatException('truncated DER');
    }
    final tag = der[offset];
    if (tag & 0x1F == 0x1F) {
      throw const FormatException('high tag numbers are not used');
    }
    final first = der[offset + 1];
    var start = offset + 2;
    var length = first;
    if (first & 0x80 != 0) {
      final n = first & 0x7F;
      if (n == 0 || n > 4 || start + n > der.length) {
        throw const FormatException('bad DER length');
      }
      length = 0;
      for (var i = 0; i < n; i++) {
        length = (length << 8) | der[start + i];
      }
      start += n;
    }
    final end = start + length;
    if (end > der.length) throw const FormatException('truncated DER');
    return _Tlv(tag, start, end);
  }

  final int tag;
  final int contentStart;
  final int end;
}
