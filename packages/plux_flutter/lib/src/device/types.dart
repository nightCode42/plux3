// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the optional device packages (`plux_media`, `plux_scanner`,
/// `plux_location`) provide to the runtime (RT-060, SEC-080): small
/// interfaces the core defines, so that a host pays for a package's
/// dependencies only when it registers the package in
/// `PluxConfig.devicePackages`.
library;

/// A package of device features the host registers in
/// `PluxConfig.devicePackages` (RT-060): it gives the runtime the
/// implementations the package's actions use.
abstract interface class PluxDevicePackage {
  /// The package's name, such as `plux_media`; the native catalogue records
  /// it, so that publishing can warn when a build lacks it.
  String get name;

  /// The implementations the package provides, by the interface they
  /// implement: [PluxMediaPicker], [PluxCodeScanner] or
  /// [PluxLocationProvider].
  Map<Type, Object> get services;
}

/// Why a device operation failed.
enum PluxDeviceFailure {
  /// The user, or the platform's policy, denied the permission.
  denied,

  /// The hardware or service is not available, or the platform call failed.
  unavailable,
}

/// A device operation failed; the runtime turns it into a typed step error
/// (`PLX-5402` for [PluxDeviceFailure.denied], `PLX-5403` otherwise). The
/// message never holds a value the user picked or scanned (SEC-092).
final class PluxDeviceException implements Exception {
  /// Creates the failure.
  const PluxDeviceException(this.failure, this.message);

  /// A permission denied.
  const PluxDeviceException.denied(String message)
    : this(PluxDeviceFailure.denied, message);

  /// A feature that is not available.
  const PluxDeviceException.unavailable(String message)
    : this(PluxDeviceFailure.unavailable, message);

  /// What failed.
  final PluxDeviceFailure failure;

  /// What happened, for developers.
  final String message;

  @override
  String toString() => 'PluxDeviceException(${failure.name}): $message';
}

/// A file the user picked or captured, as a package hands it to the
/// runtime, which keeps it behind an opaque handle.
final class PluxPickedFile {
  /// Creates a file.
  const PluxPickedFile({
    required this.path,
    required this.name,
    required this.mimeType,
    required this.sizeBytes,
  });

  /// Where the file is, in app-private storage.
  final String path;

  /// The file's name.
  final String name;

  /// Its media type, such as `image/jpeg`.
  final String mimeType;

  /// Its size in bytes.
  final int sizeBytes;
}

/// Picks and captures images and files (`plux_media`).
abstract interface class PluxMediaPicker {
  /// Lets the user pick up to [limit] images from the photo library; an
  /// empty list when the user cancels. Images are scaled down so that
  /// neither side exceeds [maxDimension] pixels, when given.
  Future<List<PluxPickedFile>> pickImages({
    required bool multiple,
    required int limit,
    int? maxDimension,
  });

  /// Takes a photo with the camera; null when the user cancels.
  Future<PluxPickedFile?> capturePhoto({int? maxDimension});

  /// Lets the user pick up to [limit] files of [mimeTypes] (any type when
  /// empty); an empty list when the user cancels.
  Future<List<PluxPickedFile>> pickFiles({
    required List<String> mimeTypes,
    required bool multiple,
    required int limit,
  });
}

/// A decoded barcode or two-dimensional code.
final class PluxScanResult {
  /// Creates a result.
  const PluxScanResult({required this.value, required this.format});

  /// The decoded text.
  final String value;

  /// The `BarcodeFormat` member that was read, such as `qrCode`.
  final String format;
}

/// Scans codes with the camera (`plux_scanner`).
abstract interface class PluxCodeScanner {
  /// Scans one code of [formats], `BarcodeFormat` member names (every
  /// format when empty); null when the user cancels.
  Future<PluxScanResult?> scan(List<String> formats);
}

/// A position the device reports.
final class PluxPosition {
  /// Creates a position.
  const PluxPosition({
    required this.latitude,
    required this.longitude,
    required this.accuracy,
    required this.timestamp,
    this.altitude,
  });

  /// Latitude in degrees (WGS 84).
  final double latitude;

  /// Longitude in degrees (WGS 84).
  final double longitude;

  /// Horizontal accuracy radius in metres.
  final double accuracy;

  /// Altitude in metres, if known.
  final double? altitude;

  /// When the position was measured.
  final DateTime timestamp;
}

/// Reads the device's position (`plux_location`).
abstract interface class PluxLocationProvider {
  /// Reads the position once, asking for [accuracy], a `LocationAccuracy`
  /// member name (`low`, `balanced` or `high`). Fails with a
  /// [PluxDeviceException].
  Future<PluxPosition> current(String accuracy);
}
