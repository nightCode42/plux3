// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The platform calls of the core device actions (SEC-080): the share
/// sheet and permission requests through the runtime's own platform
/// channel, and external URLs through `url_launcher`. Each is an interface
/// so tests run the handlers on fakes.
library;

import 'dart:math';

import 'package:flutter/services.dart';
import 'package:plux_flutter/src/device/types.dart';
import 'package:url_launcher/url_launcher.dart';

const _channel = MethodChannel('dev.plux/runtime');

/// Opens an address outside the app.
abstract interface class UrlOpener {
  /// Opens [uri] in the browser or the app that handles it; whether the
  /// platform did.
  Future<bool> open(Uri uri);
}

/// Opens addresses with `url_launcher`.
final class PlatformUrlOpener implements UrlOpener {
  /// Creates the opener.
  const PlatformUrlOpener();

  @override
  Future<bool> open(Uri uri) =>
      launchUrl(uri, mode: LaunchMode.externalApplication);
}

/// Opens a link of the app as one of its routes (NAV-008).
abstract interface class DeepLinkOpener {
  /// Opens [link]; whether a route was opened.
  Future<bool> open(Uri link);
}

/// The platform's share sheet and permission prompts.
abstract interface class DevicePlatform {
  /// Opens the share sheet with [text], [url] and the file at [filePath]
  /// (of [mimeType]), whichever are given.
  Future<void> share({
    String? text,
    String? url,
    String? filePath,
    String? mimeType,
  });

  /// Asks the user for [permission], a device API name (`camera`,
  /// `photos`, `location`, `contacts`, `biometrics` or `notifications`);
  /// whether it is granted. A permission already decided is not asked
  /// again.
  Future<bool> requestPermission(String permission);
}

/// The runtime's platform channel, `dev.plux/runtime`, implemented by
/// `PluxFlutterPlugin` in Kotlin and Swift.
final class ChannelDevicePlatform implements DevicePlatform {
  /// Creates the platform.
  const ChannelDevicePlatform();

  @override
  Future<void> share({
    String? text,
    String? url,
    String? filePath,
    String? mimeType,
  }) async {
    try {
      await _channel.invokeMethod<void>('share', {
        'text': ?text,
        'url': ?url,
        'filePath': ?filePath,
        'mimeType': ?mimeType,
      });
    } on PlatformException catch (e) {
      throw PluxDeviceException.unavailable('share failed: ${e.code}');
    } on MissingPluginException {
      throw const PluxDeviceException.unavailable(
        'this platform has no share sheet',
      );
    }
  }

  @override
  Future<bool> requestPermission(String permission) async {
    try {
      return await _channel.invokeMethod<bool>('permissionRequest', {
            'permission': permission,
          }) ??
          false;
    } on PlatformException catch (e) {
      throw PluxDeviceException.unavailable(
        'the permission request failed: ${e.code}',
      );
    } on MissingPluginException {
      throw const PluxDeviceException.unavailable(
        'this platform asks for no permissions',
      );
    }
  }
}

/// Keeps the files the user picked behind opaque handles, so plugins never
/// see a path (`PickedFile`). Handles last as long as the runtime; the
/// oldest are forgotten beyond [capacity].
final class PickedFiles {
  /// Creates the store.
  PickedFiles({Random? random, this.capacity = 256})
    : _random = random ?? Random.secure();

  /// How many files are remembered.
  final int capacity;

  final Random _random;
  final Map<String, PluxPickedFile> _files = {};

  /// Remembers [file]; its `PickedFile` value in PXL form.
  Map<String, Object?> add(PluxPickedFile file) {
    final handle = _handle();
    _files[handle] = file;
    while (_files.length > capacity) {
      _files.remove(_files.keys.first);
    }
    return {
      'handle': handle,
      'name': file.name,
      'mimeType': file.mimeType,
      'sizeBytes': file.sizeBytes,
    };
  }

  /// The file a `PickedFile` value names, or null for an unknown handle.
  PluxPickedFile? find(Object? picked) {
    final handle = picked is Map<String, Object?> ? picked['handle'] : null;
    return handle is String ? _files[handle] : null;
  }

  String _handle() {
    final bytes = [for (var i = 0; i < 12; i++) _random.nextInt(256)];
    return 'pf-${bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join()}';
  }
}
