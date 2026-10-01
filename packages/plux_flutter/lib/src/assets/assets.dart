// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The asset files of a release (AST-001, ADR-0027 Revision): which file
/// of an asset this device uses. The sync engine downloads the first file
/// of [preferredFiles]; the renderer shows the first one the store holds,
/// so a baseline's other densities or a change of pixel ratio still find
/// a file.
library;

import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/verify/manifest.dart';

/// The media type of an SVG compiled to `vector_graphics` (CMP-031).
const vectorGraphicsType = 'image/vnd.plux.vector-graphics';

/// Media types of asset files.
const svgType = 'image/svg+xml',
    webpType = 'image/webp',
    avifType = 'image/avif';

/// What decides the files of an asset this device uses.
final class AssetDevice {
  /// Creates the description.
  const AssetDevice({required this.pixelRatio, required this.avif});

  /// A device with no preferences: 3× WebP.
  static const plain = AssetDevice(pixelRatio: 3, avif: false);

  /// The device pixel ratio.
  final double pixelRatio;

  /// Whether the engine decodes AVIF here.
  final bool avif;
}

/// The assets a bundle indexes; none when it has no assets-index section.
List<fbs.Asset> assetsOf(BundleContainer bundle) {
  final sections = bundle.ofKind(SectionKind.assetsIndex);
  if (sections.isEmpty) return const [];
  return fbs.AssetIndex(sections.first.data).assets ?? const [];
}

/// The files of [asset] this device can use, best first, as lower-case
/// hex SHA-256: for an SVG only its `vector_graphics` variant, since
/// raw SVG is never parsed on the device (CMP-031); for a transcoded
/// raster image AVIF (where it decodes) before WebP, each at the smallest
/// density at or above the pixel ratio, then the other densities from the
/// nearest; the asset's own file for anything else, and last for a
/// raster image.
List<String> preferredFiles(fbs.Asset asset, AssetDevice device) {
  final variants = asset.variants ?? const <fbs.AssetVariant>[];
  String hex(List<int>? h) => hexEncode(h ?? const []);
  if (asset.mediaType == svgType) {
    return [
      for (final v in variants)
        if (v.mediaType == vectorGraphicsType) hex(v.hash),
    ];
  }
  final out = <String>[];
  for (final type in [if (device.avif) avifType, webpType]) {
    final ofType = variants.where((v) => v.mediaType == type).toList()
      ..sort((a, b) {
        double rank(fbs.AssetVariant v) => v.density >= device.pixelRatio
            ? v.density - device.pixelRatio
            : 100 + device.pixelRatio - v.density;
        return rank(a).compareTo(rank(b));
      });
    out.addAll(ofType.map((v) => hex(v.hash)));
  }
  return [...out, hex(asset.hash)];
}

/// The size [asset] declares for its file or variant [hash], or 0.
int fileSize(fbs.Asset asset, String hash) {
  if (hexEncode(asset.hash ?? const []) == hash) return asset.size;
  for (final v in asset.variants ?? const <fbs.AssetVariant>[]) {
    if (hexEncode(v.hash ?? const []) == hash) return v.size;
  }
  return 0;
}
