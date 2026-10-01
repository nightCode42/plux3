// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/assets/assets.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;

/// The SHA-256 of [name], standing for a file's hash.
List<int> h(String name) => sha256.convert(utf8.encode(name)).bytes;
String hx(String name) => sha256.convert(utf8.encode(name)).toString();

fbs.AssetVariantObjectBuilder v(String type, int density, String name) =>
    fbs.AssetVariantObjectBuilder(
      mediaType: type,
      density: density,
      hash: h(name),
      size: name.length * 10,
    );

/// One asset read back from an index holding it.
fbs.Asset asset(
  String type,
  String file, [
  List<fbs.AssetVariantObjectBuilder> variants = const [],
]) {
  final bytes = fbs.AssetIndexObjectBuilder(
    assets: [
      fbs.AssetObjectBuilder(
        id: fbs.UuidObjectBuilder(hi: 1, lo: 2),
        key: file,
        mediaType: type,
        hash: h(file),
        size: 7,
        variants: variants,
      ),
    ],
  ).toBytes();
  return fbs.AssetIndex(bytes).assets!.single;
}

void main() {
  final photo = asset('image/png', 'photo.png', [
    v(avifType, 1, 'p1.avif'),
    v(avifType, 2, 'p2.avif'),
    v(avifType, 3, 'p3.avif'),
    v(webpType, 1, 'p1.webp'),
    v(webpType, 2, 'p2.webp'),
    v(webpType, 3, 'p3.webp'),
  ]);

  test('a raster image takes AVIF where it decodes, at the smallest density '
      'at or above the pixel ratio, then the others [AST-001] [CMP-030]', () {
    expect(
      preferredFiles(photo, const AssetDevice(pixelRatio: 2.6, avif: true)),
      [
        hx('p3.avif'),
        hx('p2.avif'),
        hx('p1.avif'),
        hx('p3.webp'),
        hx('p2.webp'),
        hx('p1.webp'),
        hx('photo.png'),
      ],
    );
    expect(
      preferredFiles(
        photo,
        const AssetDevice(pixelRatio: 1, avif: false),
      ).take(3),
      [hx('p1.webp'), hx('p2.webp'), hx('p3.webp')],
    );
    expect(
      preferredFiles(
        photo,
        const AssetDevice(pixelRatio: 4, avif: false),
      ).first,
      hx('p3.webp'),
      reason: 'above every density, the largest',
    );
  });

  test('an SVG is only ever its vector_graphics form; anything else is its '
      'own file [CMP-031]', () {
    expect(
      preferredFiles(
        asset(svgType, 'icon.svg', [v(vectorGraphicsType, 0, 'icon.vec')]),
        AssetDevice.plain,
      ),
      [hx('icon.vec')],
    );
    expect(
      preferredFiles(asset(svgType, 'raw.svg'), AssetDevice.plain),
      isEmpty,
    );
    final font = asset('font/ttf', 'font.ttf');
    expect(preferredFiles(font, AssetDevice.plain), [hx('font.ttf')]);
    expect(fileSize(font, hx('font.ttf')), 7);
    expect(fileSize(photo, hx('p2.webp')), 'p2.webp'.length * 10);
    expect(fileSize(photo, hx('other')), 0);
  });

  test('a bundle without an assets-index has no assets', () {
    final empty = BundleContainer.parse(encodeBundle(BundleKinds.plugin, []));
    expect(assetsOf(empty), isEmpty);
  });
}
