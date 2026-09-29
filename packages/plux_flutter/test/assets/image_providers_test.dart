// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/painting.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:plux_flutter/src/assets/avif_probe.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// Loads [provider]'s first frame, or fails with its error.
Future<ImageInfo> load<T extends Object>(ImageProvider<T> provider) {
  final stream = provider.resolve(ImageConfiguration.empty);
  final done = Completer<ImageInfo>();
  late final ImageStreamListener listener;
  listener = ImageStreamListener(
    (info, _) {
      if (!done.isCompleted) done.complete(info);
      stream.removeListener(listener);
    },
    onError: (e, _) {
      if (!done.isCompleted) done.completeError(e);
      stream.removeListener(listener);
    },
  );
  stream.addListener(listener);
  return done.future;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final logo = File(
    '../../schema/testdata/documents/loan-calculator/assets/images/logo.png',
  ).readAsBytesSync();
  final logoHash = sha256.convert(logo).toString();
  late Directory dir;
  setUp(() => dir = Directory.systemTemp.createTempSync('plux_images'));
  tearDown(() => dir.deleteSync(recursive: true));

  test('an asset file shows once it matches its hash, checked once per '
      'process, large files off the UI isolate [AST-001] [SEC-052]', () async {
    final path = '${dir.path}/$logoHash';
    File(path).writeAsBytesSync(logo);
    final verified = VerifiedAssets();
    final info = await load(PluxAssetImage(path, logoHash, verified));
    expect(info.image.width, greaterThan(0));
    expect(
      PluxAssetImage(path, logoHash, verified),
      PluxAssetImage(path, 'x', verified),
    );
    final wrong = '${dir.path}/wrong';
    File(wrong).writeAsBytesSync(logo);
    await expectLater(
      load(PluxAssetImage(wrong, sha256.convert([0]).toString(), verified)),
      throwsA(
        isA<PluxException>().having(
          (e) => e.code,
          'code',
          PluxErrorCode.assetHashMismatch,
        ),
      ),
    );
    final big = Uint8List(uiIsolateHashLimit + 1);
    await verified.check('big', sha256.convert(big).toString(), big);
    await expectLater(
      verified.check('big2', logoHash, big),
      throwsA(isA<PluxException>()),
    );
  });

  test('a remote image is fetched once, then served from the disk cache '
      '[AST-002] [RT-014]', () async {
    var requests = 0;
    final client = MockClient((req) async {
      requests++;
      return http.Response.bytes(logo, 200);
    });
    final cache = ImageDiskCache('${dir.path}/cache', maxBytes: 1 << 20);
    final url = Uri.parse('https://cdn.example.com/logo.png');
    PluxNetworkImage image(http.Client c) =>
        PluxNetworkImage(url, cache: cache, client: c, maxBytes: 1 << 20);
    expect((await load(image(client))).image.width, greaterThan(0));
    final down = MockClient((_) async => throw const SocketException('down'));
    expect((await load(image(down))).image.width, greaterThan(0));
    expect(requests, 1);
    expect(image(client), image(down));
  });

  test('a remote image that fails, or is larger than runtime.imageSize, '
      'is refused [AST-002]', () async {
    final cache = ImageDiskCache('${dir.path}/cache', maxBytes: 1 << 20);
    Future<void> refused(http.Client c, Matcher m) => expectLater(
      load(
        PluxNetworkImage(
          Uri.parse('https://cdn.example.com/${c.hashCode}.png'),
          cache: cache,
          client: c,
          maxBytes: 16,
        ),
      ),
      throwsA(m),
    );
    await refused(
      MockClient((_) async => http.Response('gone', 404)),
      isA<NetworkImageLoadException>(),
    );
    final tooBig = isA<PluxException>().having(
      (e) => e.code,
      'code',
      PluxErrorCode.limitExceeded,
    );
    await refused(
      MockClient((_) async => http.Response.bytes(logo, 200)),
      tooBig,
    );
    await refused(
      MockClient.streaming(
        (req, _) async => http.StreamedResponse(Stream.value(logo), 200),
      ),
      tooBig,
    );
  });

  test('the disk cache drops the least recently used files over its bound '
      '[RT-014]', () async {
    final cache = ImageDiskCache('${dir.path}/cache', maxBytes: 10);
    final a = Uri.parse('https://x/a'), b = Uri.parse('https://x/b');
    await cache.put(a, Uint8List(4));
    await cache.put(b, Uint8List(4));
    File old(Uri u) =>
        File('${dir.path}/cache/${sha256.convert(utf8.encode('$u'))}');
    old(a).setLastModifiedSync(DateTime(2020));
    old(b).setLastModifiedSync(DateTime(2019));
    expect(await cache.get(a), hasLength(4), reason: 'a use refreshes a');
    await cache.put(Uri.parse('https://x/c'), Uint8List(4));
    expect(await cache.get(b), isNull);
    expect(await cache.get(a), isNotNull);
    await cache.put(Uri.parse('https://x/huge'), Uint8List(11));
    expect(await cache.get(Uri.parse('https://x/huge')), isNull);
  });

  test('declared domains match exactly or under a wildcard [SEC-080]', () {
    const domains = ['example.com', '*.cdn.example.org'];
    expect(domainAllowed('example.com', domains), isTrue);
    expect(domainAllowed('EXAMPLE.com', domains), isTrue);
    expect(domainAllowed('img.cdn.example.org', domains), isTrue);
    expect(domainAllowed('cdn.example.org', domains), isFalse);
    expect(domainAllowed('evil-example.com', domains), isFalse);
    expect(domainAllowed('sub.example.com', domains), isFalse);
    expect(domainAllowed('x.evilcdn.example.org', domains), isFalse);
  });

  test('a ThumbHash placeholder decodes through the image cache, a bad one '
      'is not shown [RT-014]', () async {
    expect(ThumbHashImage.tryParse(null), isNull);
    expect(ThumbHashImage.tryParse('not base64!'), isNull);
    expect(ThumbHashImage.tryParse(base64.encode([1, 2])), isNull);
    const hash = '1QcSHQRnh493V4dIh4eXh1h4kJUI';
    final p = ThumbHashImage.tryParse(hash)!;
    expect(p, const ThumbHashImage(hash));
    final info = await load(p);
    expect(info.image.width, greaterThan(0));
  });

  test('the AVIF probe answers without throwing', () async {
    expect(await decodesAvif(), isA<bool>());
  });
}
