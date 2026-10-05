// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The `getLocation` action for Plux plugins (RT-060, SEC-080).
///
/// Add [PluxLocation] to `PluxConfig.devicePackages`. A plugin that reads
/// the position declares the `location` device API in its capabilities and
/// the app document approves it; without this package the action fails
/// with `PLX-5401`. The host declares the location usage description
/// (`NSLocationWhenInUseUsageDescription`) and, on Android, the
/// `ACCESS_COARSE_LOCATION` and `ACCESS_FINE_LOCATION` permissions.
library;

export 'src/location.dart' show LocationBackend, PluxLocation;
