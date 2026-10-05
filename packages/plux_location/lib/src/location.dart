// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/services.dart' show PlatformException;
import 'package:geolocator/geolocator.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The platform calls behind `getLocation`. The default is `geolocator`;
/// tests and hosts with their own source supply another.
abstract interface class LocationBackend {
  /// Whether the device's location service is on.
  Future<bool> serviceEnabled();

  /// The permission the app holds now.
  Future<LocationPermission> permission();

  /// Asks the user for the permission.
  Future<LocationPermission> request();

  /// Reads the position once, at [accuracy], giving up after [timeout].
  Future<Position> position(LocationAccuracy accuracy, Duration timeout);
}

/// The `getLocation` action for Plux plugins (RT-060, SEC-080): the
/// device's position, read once, with `geolocator`.
///
/// Register it in `PluxConfig.devicePackages`:
/// `PluxConfig(…, devicePackages: [PluxLocation()])`. Plugins must declare
/// the `location` device API, and the app must approve it. The action asks
/// for the permission when the app does not hold it yet.
final class PluxLocation implements PluxDevicePackage {
  /// Creates the package. A read gives up after [timeout]; [backend]
  /// replaces `geolocator`.
  PluxLocation({
    Duration timeout = const Duration(seconds: 30),
    @visibleForTesting LocationBackend? backend,
  }) : _provider = _Provider(backend ?? const _GeolocatorBackend(), timeout);

  final _Provider _provider;

  @override
  String get name => 'plux_location';

  @override
  Map<Type, Object> get services => {PluxLocationProvider: _provider};
}

final class _Provider implements PluxLocationProvider {
  const _Provider(this._backend, this._timeout);

  final LocationBackend _backend;
  final Duration _timeout;

  @override
  Future<PluxPosition> current(String accuracy) async {
    try {
      if (!await _backend.serviceEnabled()) {
        throw const PluxDeviceException.unavailable(
          'the location service is switched off',
        );
      }
      var permission = await _backend.permission();
      if (permission == LocationPermission.denied) {
        permission = await _backend.request();
      }
      if (permission == LocationPermission.denied ||
          permission == LocationPermission.deniedForever) {
        throw const PluxDeviceException.denied(
          'the location permission was denied',
        );
      }
      final p = await _backend.position(accuracyOf(accuracy), _timeout);
      return PluxPosition(
        latitude: p.latitude,
        longitude: p.longitude,
        accuracy: p.accuracy,
        altitude: p.hasAltitude ? p.altitude : null,
        timestamp: p.timestamp,
      );
    } on PermissionDeniedException {
      throw const PluxDeviceException.denied(
        'the location permission was denied',
      );
    } on LocationServiceDisabledException {
      throw const PluxDeviceException.unavailable(
        'the location service is switched off',
      );
    } on TimeoutException {
      throw const PluxDeviceException.unavailable(
        'no position was found in time',
      );
    } on PlatformException catch (e) {
      throw PluxDeviceException.unavailable(
        'the location read failed: ${e.code}',
      );
    }
  }
}

/// The accuracy `geolocator` is asked for by a `LocationAccuracy` member.
@visibleForTesting
LocationAccuracy accuracyOf(String name) => switch (name) {
  'low' => LocationAccuracy.low,
  'high' => LocationAccuracy.high,
  _ => LocationAccuracy.medium,
};

final class _GeolocatorBackend implements LocationBackend {
  const _GeolocatorBackend();

  @override
  Future<bool> serviceEnabled() => Geolocator.isLocationServiceEnabled();

  @override
  Future<LocationPermission> permission() => Geolocator.checkPermission();

  @override
  Future<LocationPermission> request() => Geolocator.requestPermission();

  @override
  Future<Position> position(LocationAccuracy accuracy, Duration timeout) =>
      Geolocator.getCurrentPosition(
        locationSettings: LocationSettings(
          accuracy: accuracy,
          timeLimit: timeout,
        ),
      );
}
