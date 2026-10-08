// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Verifies: SEC-050, SEC-051, SEC-056, SEC-122.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/verify/jcs.dart';
import 'package:plux_flutter/src/verify/manifest.dart';
import 'package:plux_flutter/src/verify/update_metadata.dart';

/// Documents signed by the server's own code (`backend/internal/updatemeta`):
/// a root of three offline keys (Ed25519, ES256, Ed25519; two of three), an
/// Ed25519 targets and snapshot key and an ES256 timestamp key; a rotation
/// to root 2 and 3, and the same chains under root 2's online keys.
final Map<String, Object?> _v = jsonDecode(
  File('test/verify/update_metadata_vectors.json').readAsStringSync(),
) as Map<String, Object?>;

Uint8List _bytes(String s) => Uint8List.fromList(utf8.encode(s));
Uint8List _doc(String name) => _bytes(_v[name]! as String);

final DateTime _now = DateTime.parse(_v['now']! as String);

({
  Uint8List timestamp,
  Uint8List snapshot,
  Uint8List signed,
  List<DocumentSignature> sigs,
})
_chain(String name) {
  final c = _v[name]! as Map<String, Object?>;
  return (
    timestamp: _bytes(c['timestamp']! as String),
    snapshot: _bytes(c['snapshot']! as String),
    signed: base64.decode(c['manifestSigned']! as String),
    sigs: [
      for (final s
          in (c['manifestSignatures']! as List<Object?>)
              .cast<Map<String, Object?>>())
        DocumentSignature.fromJson(s),
    ],
  );
}

List<String> _ids(String name) =>
    ((_v['ids']! as Map<String, Object?>)[name]! as List<Object?>)
        .cast<String>();

/// [doc] with the first character of its first signature changed.
Uint8List _tampered(Uint8List doc) {
  final j = jsonDecode(utf8.decode(doc)) as Map<String, Object?>;
  final first =
      (j['signatures']! as List<Object?>).first! as Map<String, Object?>;
  final sig = first['sig']! as String;
  first['sig'] = (sig.startsWith('A') ? 'B' : 'A') + sig.substring(1);
  return _bytes(canonicalJson(j));
}

/// [doc] with its signed part changed by [edit] and its signatures kept.
Uint8List _edited(
  Uint8List doc,
  void Function(Map<String, Object?> signed) edit,
) {
  final j = jsonDecode(utf8.decode(doc)) as Map<String, Object?>;
  edit(j['signed']! as Map<String, Object?>);
  return _bytes(canonicalJson(j));
}

Matcher _fails(String fault, PluxErrorCode code) => throwsA(
  isA<PluxException>()
      .having((e) => e.details['fault'], 'fault', fault)
      .having((e) => e.code, 'code', code),
);

void main() {
  late RootMetadata root1;
  late Map<String, String> allProduction;

  setUp(() async {
    root1 = await loadRoot(_doc('root1'));
    allProduction = {
      for (final id in [
        ..._ids('root1'),
        ..._ids('online1'),
        ..._ids('online2'),
        ..._ids('newRoot'),
      ])
        id: environmentProduction,
    };
  });

  MetadataVerifier verifier({
    RootMetadata? root,
    DateTime? now,
    bool production = false,
    Map<String, String>? environments,
    MetadataFloor floor = const MetadataFloor(),
  }) => MetadataVerifier(
    root: root ?? root1,
    now: now ?? _now,
    production: production,
    keyEnvironments: environments ?? allProduction,
    floor: floor,
  );

  Future<MetadataFloor> run(MetadataVerifier v, String chainName) async {
    final c = _chain(chainName);
    final ts = await v.timestamp(c.timestamp);
    final snap = await v.snapshot(c.snapshot, ts);
    final t = await v.targets(c.signed, c.sigs, snap);
    return MetadataFloor(
      timestamp: ts.version,
      snapshot: snap.version,
      targets: t.version,
    );
  }

  group('the chain', () {
    test('timestamp, snapshot and manifest verify under the root, whatever '
        'the algorithm of each role [SEC-050] [SEC-122]', () async {
      final floor = await run(verifier(), 'chain1');
      expect(floor.timestamp, 9, reason: 'signed with ES256');
      expect(floor.snapshot, 3, reason: 'signed with Ed25519');
      expect(floor.targets, 5);
    });

    test('the root made from the embedded keys verifies the same chain '
        '[SEC-050] [SEC-051]', () async {
      final anchor = rootFromKeys([
        for (final k
            in (_v['keys1']! as List<Object?>).cast<Map<String, Object?>>())
          TrustedKey.fromJson(k),
      ]);
      expect(anchor.version, 1);
      expect(anchor.role('root').threshold, 2);
      expect(anchor.role('timestamp').threshold, 1);
      await run(verifier(root: anchor), 'chain1');
    });

    test(
      'a wrong signature on any role fails the threshold [SEC-050]',
      () async {
        final c = _chain('chain1');
        final v = verifier();
        await expectLater(
          v.timestamp(_tampered(c.timestamp)),
          _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
        );
        final ts = await v.timestamp(c.timestamp);
        // A changed snapshot is not the one the timestamp hashes.
        await expectLater(
          v.snapshot(_tampered(c.snapshot), ts),
          _fails(MetadataFault.mismatch, PluxErrorCode.updateMetadataInvalid),
        );
        final snap = await v.snapshot(c.snapshot, ts);
        final bad = [
          DocumentSignature(
            keyId: c.sigs.single.keyId,
            algorithm: c.sigs.single.algorithm,
            signature: Uint8List.fromList(c.sigs.single.signature)..[3] ^= 1,
          ),
        ];
        await expectLater(
          v.targets(c.signed, bad, snap),
          _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
        );
      },
    );

    test(
      'a signature by a key the role does not list, with an algorithm '
      'nobody verifies, or none at all counts for nothing [SEC-050] [SEC-122]',
      () async {
        final v = verifier();
        for (final doc in ['timestampWrongKey', 'timestampMLDSA']) {
          await expectLater(
            v.timestamp(_doc(doc)),
            _fails(
              MetadataFault.threshold,
              PluxErrorCode.updateMetadataInvalid,
            ),
            reason: doc,
          );
        }
        final c = _chain('chain1');
        final ts = await v.timestamp(c.timestamp);
        final snap = await v.snapshot(c.snapshot, ts);
        await expectLater(
          v.targets(c.signed, const [], snap),
          _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
        );
      },
    );

    test('a threshold of distinct keys is needed: one key twice is one '
        '[SEC-050]', () async {
      final doc = _doc('root2OneOld');
      final j = jsonDecode(utf8.decode(doc)) as Map<String, Object?>;
      final sigs = (j['signatures']! as List<Object?>)
          .cast<Map<String, Object?>>();
      j['signatures'] = [sigs.first, sigs.first, sigs.first];
      await expectLater(
        nextRoot(root1, _bytes(canonicalJson(j))),
        _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
      );
    });

    test('an expired timestamp is refused at sync, by the clock passed in '
        '[SEC-050] [B11]', () async {
      final late = _now.add(const Duration(days: 2));
      await expectLater(
        run(verifier(now: late), 'chain1'),
        _fails(MetadataFault.expired, PluxErrorCode.updateMetadataInvalid),
      );
      // The snapshot lives a week and the manifest a month.
      final c = _chain('chain1');
      final inAWeek = verifier(now: _now.add(const Duration(days: 8)));
      await expectLater(
        inAWeek.snapshot(c.snapshot, await verifier().timestamp(c.timestamp)),
        _fails(MetadataFault.expired, PluxErrorCode.updateMetadataInvalid),
      );
    });

    test('a version below the trusted one is a rollback, in each role '
        '[SEC-050]', () async {
      await run(
        verifier(
          floor: const MetadataFloor(timestamp: 9, snapshot: 3, targets: 5),
        ),
        'chain1',
      );
      for (final floor in const [
        MetadataFloor(timestamp: 10),
        MetadataFloor(snapshot: 4),
        MetadataFloor(targets: 6),
      ]) {
        await expectLater(
          run(verifier(floor: floor), 'chain1'),
          _fails(MetadataFault.rollback, PluxErrorCode.rollbackRejected),
          reason: '${floor.timestamp}/${floor.snapshot}/${floor.targets}',
        );
      }
    });

    test('the snapshot pins the manifest\'s version, and the timestamp the '
        'snapshot\'s hash and version [SEC-050]', () async {
      final v = verifier();
      final c = _chain('chain1');
      final older = _chain('chain1Targets4');
      final ts = await v.timestamp(c.timestamp);
      final snap = await v.snapshot(c.snapshot, ts);
      await expectLater(
        v.targets(older.signed, older.sigs, snap),
        _fails(MetadataFault.mismatch, PluxErrorCode.updateMetadataInvalid),
      );
      // A genuine snapshot, but not the one this timestamp names.
      await expectLater(
        v.snapshot(_chain('chain2').snapshot, ts),
        _fails(MetadataFault.mismatch, PluxErrorCode.updateMetadataInvalid),
      );
    });

    test('a chain signed under the keys of a later root does not verify under '
        'the earlier one [SEC-051]', () async {
      await expectLater(
        run(verifier(), 'chain2'),
        _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
      );
      final root2 = await nextRoot(root1, _doc('root2'));
      await run(verifier(root: root2), 'chain2');
      await expectLater(
        run(verifier(root: root2), 'chain1'),
        _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
      );
    });
  });

  group('development keys [SEC-056]', () {
    test('a production runtime refuses a key that is not known to be a '
        'production key', () async {
      await run(verifier(production: true), 'chain1');
      for (final env in [
        <String, String>{},
        {...allProduction, _ids('online1')[2]: environmentDevelopment},
        {...allProduction, _ids('online1')[1]: environmentDevelopment},
        {...allProduction, _ids('online1')[0]: environmentDevelopment},
      ]) {
        await expectLater(
          run(verifier(production: true, environments: env), 'chain1'),
          _fails(
            MetadataFault.developmentKey,
            PluxErrorCode.developmentKeyInProduction,
          ),
        );
      }
    });

    test('a development runtime accepts them', () async {
      await run(
        verifier(
          environments: {
            for (final k in allProduction.keys) k: environmentDevelopment,
          },
        ),
        'chain1',
      );
    });
  });

  group('root rotation [SEC-051]', () {
    test(
      'a root signed by the previous threshold and its own is accepted',
      () async {
        final root2 = await nextRoot(root1, _doc('root2'));
        expect(root2.version, 2);
        expect(root2.role('timestamp').keyIds, _ids('online2').sublist(2));
        final anchor = rootFromKeys([
          for (final k
              in (_v['keys1']! as List<Object?>).cast<Map<String, Object?>>())
            TrustedKey.fromJson(k),
        ]);
        expect((await nextRoot(anchor, _doc('root2'))).version, 2);
      },
    );

    test('the chain of roots since the embedded one is followed in order, '
        'tolerating intermediate expiry [SEC-051] [B11]', () async {
      final last = await rootChain(root1, [_doc('root2'), _doc('root3')], _now);
      expect(last.version, 3);
      final viaExpired = await rootChain(root1, [
        _doc('root2Expired'),
        _doc('root3'),
      ], _now);
      expect(viaExpired.version, 3);
      expect((await rootChain(root1, const [], _now)).version, 1);
    });

    test('a root signed only by the old keys, only by the new keys, or by '
        'too few of either is refused', () async {
      for (final name in ['root2OnlyOld', 'root2OnlyNew', 'root2OneOld']) {
        await expectLater(
          nextRoot(root1, _doc(name)),
          _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
          reason: name,
        );
      }
    });

    test('a root that skips a version or repeats one is refused', () async {
      await expectLater(
        nextRoot(root1, _doc('root4')),
        _fails(MetadataFault.rollback, PluxErrorCode.rollbackRejected),
      );
      final root2 = await nextRoot(root1, _doc('root2'));
      await expectLater(
        nextRoot(root2, _doc('root2')),
        _fails(MetadataFault.rollback, PluxErrorCode.rollbackRejected),
      );
      await expectLater(
        nextRoot(root1, _doc('root3')),
        _fails(MetadataFault.rollback, PluxErrorCode.rollbackRejected),
      );
    });

    test('the newest root must not have expired [SEC-050]', () async {
      await expectLater(
        rootChain(root1, [_doc('root2Expired')], _now),
        _fails(MetadataFault.expired, PluxErrorCode.updateMetadataInvalid),
      );
      await expectLater(
        rootChain(root1, const [], _now.add(const Duration(days: 400))),
        _fails(MetadataFault.expired, PluxErrorCode.updateMetadataInvalid),
      );
    });

    test('a tampered root is refused', () async {
      await expectLater(
        nextRoot(root1, _tampered(_doc('root2'))),
        _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
      );
      await expectLater(
        loadRoot(_tampered(_doc('root1'))),
        _fails(MetadataFault.threshold, PluxErrorCode.updateMetadataInvalid),
      );
    });
  });

  group('the formats [SEC-122]', () {
    test('algorithm names read as the schema spells them', () {
      expect(canonicalAlgorithm('ed25519'), algEd25519);
      expect(canonicalAlgorithm('ecdsa-p256-sha256'), algES256);
      expect(canonicalAlgorithm('ML-DSA-65'), algMlDsa65);
      expect(isSupportedAlgorithm('ES256'), isTrue);
      expect(isSupportedAlgorithm('Ed25519'), isTrue);
      expect(isSupportedAlgorithm('ML-DSA-65'), isFalse);
      expect(isSupportedAlgorithm('rsa'), isFalse);
    });

    test(
      'a file must be canonical JSON with exactly the schema\'s members',
      () {
        final doc = _doc('root1');
        final pretty = _bytes(
          const JsonEncoder.withIndent(' ')
              .convert(jsonDecode(utf8.decode(doc))),
        );
        expect(
          () => parseMetadataDocument(pretty),
          throwsA(isA<PluxException>()),
        );
        expect(
          () => parseMetadataDocument(Uint8List(0)),
          throwsA(isA<PluxException>()),
        );
        expect(
          () => parseMetadataDocument(Uint8List(metadataMaxBytes + 1)),
          throwsA(isA<PluxException>()),
        );
        final extra = _bytes(
          canonicalJson({
            ...(jsonDecode(utf8.decode(doc)) as Map<String, Object?>),
            'extra': 1,
          }),
        );
        expect(
          () => parseMetadataDocument(extra),
          throwsA(isA<PluxException>()),
        );
        final dup = _bytes(
          utf8
              .decode(doc)
              .replaceFirst('{"signatures"', '{"signatures":[],"signatures"'),
        );
        expect(() => parseMetadataDocument(dup), throwsA(isA<PluxException>()));
      },
    );

    test('the documents of the schema vectors that break the schema are '
        'refused', () {
      final vectors = jsonDecode(
        File('../../schema/testdata/update/vectors.json').readAsStringSync(),
      ) as Map<String, Object?>;
      var refused = 0;
      for (final role in ['root', 'timestamp', 'snapshot']) {
        final invalid =
            ((vectors[role]! as Map<String, Object?>)['invalid']!
                    as List<Object?>)
                .cast<Map<String, Object?>>();
        for (final c in invalid) {
          expect(
            () {
              final d = parseMetadataDocument(
                _bytes(canonicalJson(c['document'])),
              );
              switch (role) {
                case 'root':
                  parseRoot(d);
                case 'timestamp':
                  parseTimestamp(d);
                default:
                  parseSnapshot(d);
              }
            },
            throwsA(isA<PluxException>()),
            reason: '$role ${c['name']}',
          );
          refused++;
        }
      }
      expect(refused, 27);
    });

    test('the valid timestamp and snapshot vectors parse', () {
      final vectors = jsonDecode(
        File('../../schema/testdata/update/vectors.json').readAsStringSync(),
      ) as Map<String, Object?>;
      Map<String, Object?> valid(String role) =>
          (((vectors[role]! as Map<String, Object?>)['valid']! as List<Object?>)
                      .first!
                  as Map<String, Object?>)['document']!
              as Map<String, Object?>;
      final ts = parseTimestamp(
        parseMetadataDocument(_bytes(canonicalJson(valid('timestamp')))),
      );
      expect(ts.version, 1);
      final snap = parseSnapshot(
        parseMetadataDocument(_bytes(canonicalJson(valid('snapshot')))),
      );
      expect(snap.targetsVersion, greaterThan(0));
    });

    test('an impossible date is not RFC 3339', () {
      final doc = _edited(
        _chain('chain1').timestamp,
        (s) => s['expires'] = '2026-02-30T00:00:00Z',
      );
      expect(
        () => parseTimestamp(parseMetadataDocument(doc)),
        throwsA(isA<PluxException>()),
      );
      final lax = _edited(
        _chain('chain1').timestamp,
        (s) => s['expires'] = '2026-10-09 00:00:00',
      );
      expect(
        () => parseTimestamp(parseMetadataDocument(lax)),
        throwsA(isA<PluxException>()),
      );
    });

    test('the manifest\'s targets fields: its role, version and expiry', () {
      final signed = _chain('chain1').signed;
      final t = parseTargets(signed);
      expect(t.version, 5);
      final wrong = _bytes(
        canonicalJson({
          ...(jsonDecode(utf8.decode(signed)) as Map<String, Object?>),
          'role': 'root',
        }),
      );
      expect(() => parseTargets(wrong), throwsA(isA<PluxException>()));
      expect(
        () => parseTargets(
          _bytes(
            '{"role":"targets","version":0,"expires":"2027-01-01T00:00:00Z"}',
          ),
        ),
        throwsA(isA<PluxException>()),
      );
    });

    test('a root whose key is not the hash of its public key, or whose role '
        'cannot meet its threshold, is malformed', () {
      final swapped = _edited(_doc('root1'), (s) {
        final keys = s['keys']! as Map<String, Object?>;
        final ids = keys.keys.toList();
        final a = keys[ids[0]];
        keys[ids[0]] = keys[ids[1]];
        keys[ids[1]] = a;
      });
      expect(
        () => parseRoot(parseMetadataDocument(swapped)),
        throwsA(isA<PluxException>()),
      );
      final tooMany = _edited(_doc('root1'), (s) {
        (((s['roles']! as Map<String, Object?>)['timestamp']!)
                as Map<String, Object?>)['threshold'] =
            2;
      });
      expect(
        () => parseRoot(parseMetadataDocument(tooMany)),
        throwsA(isA<PluxException>()),
      );
    });
  });
}
