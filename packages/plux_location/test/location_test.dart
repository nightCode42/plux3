// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_test/flutter_test.dart';
import 'package:geolocator/geolocator.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_location/plux_location.dart';
import 'package:plux_location/src/location.dart' show accuracyOf;

final class FakeBackend implements LocationBackend {
  bool enabled = true;
  LocationPermission current = LocationPermission.whileInUse;
  LocationPermission afterRequest = LocationPermission.whileInUse;
  Exception? failure;
  bool hasAltitude = true;
  final List<String> calls = [];

  @override
  Future<bool> serviceEnabled() async => enabled;

  @override
  Future<LocationPermission> permission() async => current;

  @override
  Future<LocationPermission> request() async {
    calls.add('request');
    return afterRequest;
  }

  @override
  Future<Position> position(LocationAccuracy accuracy, Duration timeout) async {
    calls.add('position ${accuracy.name} ${timeout.inSeconds}');
    if (failure case final f?) throw f;
    return Position(
      longitude: 13.4,
      latitude: 52.5,
      timestamp: DateTime.utc(2026, 1, 2),
      accuracy: 8,
      altitude: 34,
      altitudeAccuracy: 0,
      heading: 0,
      headingAccuracy: 0,
      speed: 0,
      speedAccuracy: 0,
      hasAccuracy: true,
      hasAltitude: hasAltitude,
    );
  }
}

void main() {
  late FakeBackend backend;
  late PluxLocationProvider provider;

  setUp(() {
    backend = FakeBackend();
    provider =
        PluxLocation(
              backend: backend,
              timeout: const Duration(seconds: 5),
            ).services[PluxLocationProvider]!
            as PluxLocationProvider;
  });

  Matcher failsWith(PluxDeviceFailure f) => throwsA(
    isA<PluxDeviceException>().having((e) => e.failure, 'failure', f),
  );

  test(
    'the package registers itself as plux_location and provides the reader',
    () {
      final package = PluxLocation(backend: backend);
      expect(package, isA<PluxDevicePackage>());
      expect(package.name, 'plux_location');
      expect(package.services.keys, [PluxLocationProvider]);
    },
  );

  test('a position is read once at the accuracy asked for', () async {
    final p = await provider.current('high');
    expect(backend.calls, ['position high 5']);
    expect((p.latitude, p.longitude, p.accuracy), (52.5, 13.4, 8.0));
    expect(p.altitude, 34);
    expect(p.timestamp, DateTime.utc(2026, 1, 2));
  });

  test('an altitude the device does not know is null', () async {
    backend.hasAltitude = false;
    expect((await provider.current('balanced')).altitude, isNull);
  });

  test('the permission is asked for when the app does not hold it', () async {
    backend.current = LocationPermission.denied;
    await provider.current('low');
    expect(backend.calls, ['request', 'position low 5']);
  });

  test('a refused permission is a denial, and no position is read', () async {
    backend.current = LocationPermission.denied;
    backend.afterRequest = LocationPermission.deniedForever;
    await expectLater(
      provider.current('balanced'),
      failsWith(PluxDeviceFailure.denied),
    );
    expect(backend.calls, ['request']);
    backend.current = LocationPermission.deniedForever;
    await expectLater(
      provider.current('balanced'),
      failsWith(PluxDeviceFailure.denied),
    );
  });

  test(
    'a service that is off, a timeout and platform errors are unavailable',
    () async {
      backend.enabled = false;
      await expectLater(
        provider.current('balanced'),
        failsWith(PluxDeviceFailure.unavailable),
      );
      backend.enabled = true;
      backend.failure = TimeoutException('slow');
      await expectLater(
        provider.current('balanced'),
        failsWith(PluxDeviceFailure.unavailable),
      );
      backend.failure = PlatformException(code: 'LOCATION_ERROR');
      await expectLater(
        provider.current('balanced'),
        failsWith(PluxDeviceFailure.unavailable),
      );
      backend.failure = const LocationServiceDisabledException();
      await expectLater(
        provider.current('balanced'),
        failsWith(PluxDeviceFailure.unavailable),
      );
      backend.failure = const PermissionDeniedException('no');
      await expectLater(
        provider.current('balanced'),
        failsWith(PluxDeviceFailure.denied),
      );
    },
  );

  test('schema accuracies map to the platform accuracies', () {
    expect(accuracyOf('low'), LocationAccuracy.low);
    expect(accuracyOf('balanced'), LocationAccuracy.medium);
    expect(accuracyOf('high'), LocationAccuracy.high);
  });
}
