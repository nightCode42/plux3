// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The `scanCode` action for Plux plugins (RT-060, SEC-080).
///
/// Add [PluxScanner] to `PluxConfig.devicePackages`. A plugin that scans
/// declares the `camera` device API in its capabilities and the app
/// document approves it; without this package the action fails with
/// `PLX-5401`. The host declares the camera usage description
/// (`NSCameraUsageDescription`) and, on Android, the camera permission.
library;

export 'src/scanner.dart' show PluxScanner, ScanPageBuilder, ScanSink;
