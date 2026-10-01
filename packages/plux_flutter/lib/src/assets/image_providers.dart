// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Images of Plux pages (AST-001, AST-002, RT-014): asset files of the
/// release, read from the store and checked against the hash their signed
/// bundle lists the first time this process shows them; and remote
/// images, fetched with the runtime's HTTP client, kept in a disk cache
/// bounded by `runtime.imageDiskCacheBytes` and in Flutter's memory
/// `ImageCache`, and refused above `runtime.imageSize`.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;
import 'package:plux_flutter/src/assets/thumbhash.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:vector_graphics/vector_graphics.dart';

/// Files larger than this are hashed on a background isolate (L-6), as
/// sections are (ADR-0029).
const int uiIsolateHashLimit = 64 * 1024;

/// The asset files this process has checked, by path.
final class VerifiedAssets {
  final Set<String> _paths = {};

  /// Checks [bytes] of [path] against [hash] once per process; throws
  /// [PluxException] (PLX-3045) on a mismatch.
  Future<void> check(String path, String hash, Uint8List bytes) async {
    if (_paths.contains(path)) return;
    final got = bytes.length > uiIsolateHashLimit
        ? await Isolate.run(() => sha256.convert(bytes).toString())
        : sha256.convert(bytes).toString();
    if (got != hash) {
      throw PluxException(
        PluxErrorCode.assetHashMismatch,
        'the stored asset file $hash reads as $got',
      );
    }
    _paths.add(path);
  }
}

/// An asset file of the release.
@immutable
final class PluxAssetImage extends ImageProvider<PluxAssetImage> {
  /// Creates the provider for the stored file [path] named by [hash].
  const PluxAssetImage(this.path, this.hash, this.verified);

  /// Where the store keeps the file.
  final String path;

  /// Its SHA-256, lower-case hex.
  final String hash;

  /// The files already checked.
  final VerifiedAssets verified;

  @override
  Future<PluxAssetImage> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(this);

  @override
  ImageStreamCompleter loadImage(
    PluxAssetImage key,
    ImageDecoderCallback decode,
  ) => MultiFrameImageStreamCompleter(
    codec: _load(decode),
    scale: 1,
    debugLabel: 'plux asset $hash',
  );

  Future<ui.Codec> _load(ImageDecoderCallback decode) async {
    final bytes = await File(path).readAsBytes();
    await verified.check(path, hash, bytes);
    return decode(await ui.ImmutableBuffer.fromUint8List(bytes));
  }

  @override
  bool operator ==(Object other) =>
      other is PluxAssetImage && other.path == path;

  @override
  int get hashCode => path.hashCode;
}

/// An SVG asset of the release as a node shows it (CMP-031): its
/// `vector_graphics` file, or none when the store holds none this device
/// can show (already reported).
@immutable
final class PluxVectorSource {
  /// Creates the source.
  const PluxVectorSource(this.loader);

  /// Loads the file; null when there is none.
  final PluxVectorLoader? loader;
}

/// Loads a stored `vector_graphics` file of the release, checked against
/// the hash its signed bundle lists the first time this process shows it.
@immutable
final class PluxVectorLoader extends BytesLoader {
  /// Creates the loader of the stored file [path] named by [hash].
  const PluxVectorLoader(this.path, this.hash, this.verified);

  /// Where the store keeps the file.
  final String path;

  /// Its SHA-256, lower-case hex.
  final String hash;

  /// The files already checked.
  final VerifiedAssets verified;

  @override
  Future<ByteData> loadBytes(BuildContext? context) async {
    final bytes = await File(path).readAsBytes();
    await verified.check(path, hash, bytes);
    return ByteData.sublistView(bytes);
  }

  @override
  bool operator ==(Object other) =>
      other is PluxVectorLoader && other.path == path;

  @override
  int get hashCode => path.hashCode;
}

/// Remote image bytes on disk, by the SHA-256 of their URL, least
/// recently used first out once over [maxBytes] (RT-014).
final class ImageDiskCache {
  /// Creates the cache in [directory].
  ImageDiskCache(this.directory, {required this.maxBytes});

  /// Where the files are.
  final String directory;

  /// The bytes the files may hold together.
  final int maxBytes;

  Future<void>? _trimming;

  String _file(Uri url) => '$directory/${sha256.convert(utf8.encode('$url'))}';

  /// The cached bytes of [url], or null; a hit counts as a use.
  Future<Uint8List?> get(Uri url) async {
    final f = File(_file(url));
    try {
      final bytes = await f.readAsBytes();
      await f.setLastModified(DateTime.now());
      return bytes;
    } on FileSystemException {
      return null;
    }
  }

  /// Keeps [bytes] of [url], then trims the cache.
  Future<void> put(Uri url, Uint8List bytes) async {
    if (bytes.length > maxBytes) return;
    await Directory(directory).create(recursive: true);
    final path = _file(url);
    final part = File('$path.part');
    await part.writeAsBytes(bytes, flush: true);
    await part.rename(path);
    await (_trimming ??= _trim().whenComplete(() => _trimming = null));
  }

  Future<void> _trim() async {
    final files = <(File, DateTime, int)>[];
    await for (final e in Directory(directory).list()) {
      if (e is! File || e.path.endsWith('.part')) continue;
      final st = e.statSync();
      files.add((e, st.modified, st.size));
    }
    var total = files.fold<int>(0, (n, f) => n + f.$3);
    files.sort((a, b) => a.$2.compareTo(b.$2));
    for (final (f, _, size) in files) {
      if (total <= maxBytes) break;
      try {
        await f.delete();
        total -= size;
      } on FileSystemException {
        // Removed by another trim; nothing to do.
      }
    }
  }
}

/// A remote image (AST-002), fetched once and then served from the disk
/// cache.
@immutable
final class PluxNetworkImage extends ImageProvider<PluxNetworkImage> {
  /// Creates the provider.
  const PluxNetworkImage(
    this.url, {
    required this.cache,
    required this.client,
    required this.maxBytes,
  });

  /// The image's HTTPS URL, on a domain the plugin declares.
  final Uri url;

  /// The disk cache.
  final ImageDiskCache cache;

  /// The runtime's HTTP client.
  final http.Client client;

  /// The largest image accepted (`runtime.imageSize`).
  final int maxBytes;

  @override
  Future<PluxNetworkImage> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(this);

  @override
  ImageStreamCompleter loadImage(
    PluxNetworkImage key,
    ImageDecoderCallback decode,
  ) => MultiFrameImageStreamCompleter(
    codec: _load(decode),
    scale: 1,
    debugLabel: 'plux image $url',
  );

  Future<ui.Codec> _load(ImageDecoderCallback decode) async {
    final bytes = await cache.get(url) ?? await _fetch();
    return decode(await ui.ImmutableBuffer.fromUint8List(bytes));
  }

  Future<Uint8List> _fetch() async {
    final res = await client.send(http.Request('GET', url));
    if (res.statusCode != 200) {
      await res.stream.drain<void>();
      throw NetworkImageLoadException(statusCode: res.statusCode, uri: url);
    }
    final declared = res.contentLength;
    if (declared != null && declared > maxBytes) {
      await res.stream.drain<void>();
      throw PluxException(
        PluxErrorCode.limitExceeded,
        'the image at $url is $declared bytes, over runtime.imageSize = $maxBytes',
      );
    }
    final out = BytesBuilder(copy: false);
    await for (final chunk in res.stream) {
      out.add(chunk);
      if (out.length > maxBytes) {
        throw PluxException(
          PluxErrorCode.limitExceeded,
          'the image at $url exceeds runtime.imageSize = $maxBytes',
        );
      }
    }
    final bytes = out.takeBytes();
    await cache.put(url, bytes);
    return bytes;
  }

  @override
  bool operator ==(Object other) =>
      other is PluxNetworkImage && other.url == url;

  @override
  int get hashCode => url.hashCode;
}

/// Whether [host] is one of the plugin's declared [domains] (SEC-080):
/// the same name, or under a `*.` wildcard (which does not match the bare
/// domain).
bool domainAllowed(String host, List<String> domains) {
  final h = host.toLowerCase();
  for (final d in domains) {
    if (d.startsWith('*.')) {
      if (h.endsWith(d.substring(1)) && h.length > d.length - 1) return true;
    } else if (h == d) {
      return true;
    }
  }
  return false;
}

/// The placeholder a ThumbHash describes (RT-014), decoded once per hash
/// through Flutter's image cache.
@immutable
final class ThumbHashImage extends ImageProvider<ThumbHashImage> {
  /// Creates the provider for [hash], base64-encoded.
  const ThumbHashImage(this.hash);

  /// The ThumbHash, base64-encoded.
  final String hash;

  /// The provider for [hash], or null when it is not a valid ThumbHash: a
  /// placeholder is optional, so a bad one is simply not shown.
  static ThumbHashImage? tryParse(String? hash) {
    if (hash == null) return null;
    try {
      decodeThumbHash(base64.decode(hash));
      return ThumbHashImage(hash);
    } on FormatException {
      return null;
    }
  }

  @override
  Future<ThumbHashImage> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(this);

  @override
  ImageStreamCompleter loadImage(
    ThumbHashImage key,
    ImageDecoderCallback decode,
  ) => OneFrameImageStreamCompleter(_load(decode));

  Future<ImageInfo> _load(ImageDecoderCallback decode) async {
    final bmp = bmpOf(decodeThumbHash(base64.decode(hash)));
    final codec = await decode(await ui.ImmutableBuffer.fromUint8List(bmp));
    final frame = await codec.getNextFrame();
    codec.dispose();
    return ImageInfo(image: frame.image);
  }

  @override
  bool operator ==(Object other) =>
      other is ThumbHashImage && other.hash == hash;

  @override
  int get hashCode => hash.hashCode;
}
