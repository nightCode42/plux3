// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart' as ms;
import 'package:plux_flutter/plux_flutter.dart';

/// What a scan page reports. It reports once; later calls are ignored.
abstract interface class ScanSink {
  /// A code was read.
  void found(PluxScanResult result);

  /// The scan cannot go on: the camera is denied or missing.
  void fail(PluxDeviceException error);

  /// The user gave up.
  void cancel();
}

/// Builds the page that scans codes of [formats], `BarcodeFormat` member
/// names (every format when empty), and reports to [sink].
typedef ScanPageBuilder = Widget Function(
  BuildContext context,
  List<String> formats,
  ScanSink sink,
);

/// The `scanCode` action for Plux plugins (RT-060, SEC-080): a full-screen
/// camera page, on `mobile_scanner` (ML Kit on Android, Vision on iOS),
/// that returns the first code it reads.
///
/// Register it in `PluxConfig.devicePackages` with the key of the host's
/// root navigator: `PluxScanner(navigatorKey: navigatorKey)`. Plugins must
/// declare the `camera` device API, and the app must approve it.
final class PluxScanner implements PluxDevicePackage {
  /// Creates the package. The scan page opens on the navigator of
  /// [navigatorKey]; [pageBuilder] replaces the camera page.
  PluxScanner({
    required GlobalKey<NavigatorState> navigatorKey,
    @visibleForTesting ScanPageBuilder? pageBuilder,
  }) : _scanner = _Scanner(navigatorKey, pageBuilder ?? _cameraPage);

  final _Scanner _scanner;

  @override
  String get name => 'plux_scanner';

  @override
  Map<Type, Object> get services => {PluxCodeScanner: _scanner};
}

final class _Scanner implements PluxCodeScanner {
  _Scanner(this._navigator, this._page);

  final GlobalKey<NavigatorState> _navigator;
  final ScanPageBuilder _page;

  @override
  Future<PluxScanResult?> scan(List<String> formats) {
    final nav = _navigator.currentState;
    if (nav == null) {
      return Future.error(
        const PluxDeviceException.unavailable(
          'there is no navigator to show the scanner on',
        ),
      );
    }
    final done = Completer<PluxScanResult?>();
    late final MaterialPageRoute<void> route;
    void finish(PluxScanResult? result, [PluxDeviceException? error]) {
      if (done.isCompleted) return;
      if (error != null) {
        done.completeError(error);
      } else {
        done.complete(result);
      }
      if (route.isCurrent) {
        nav.pop();
      } else if (route.isActive) {
        nav.removeRoute(route);
      }
    }

    final sink = _Sink(finish);
    route = MaterialPageRoute<void>(
      fullscreenDialog: true,
      builder: (context) => _page(context, formats, sink),
    );
    // Leaving the page by the back button or a swipe is a cancel.
    unawaited(route.popped.then((_) => finish(null)));
    unawaited(nav.push(route));
    return done.future;
  }
}

final class _Sink implements ScanSink {
  const _Sink(this._finish);

  final void Function(PluxScanResult?, [PluxDeviceException?]) _finish;

  @override
  void found(PluxScanResult result) => _finish(result);

  @override
  void fail(PluxDeviceException error) => _finish(null, error);

  @override
  void cancel() => _finish(null);
}

/// The `BarcodeFormat` members of the schema and the scanner's formats.
const Map<String, List<ms.BarcodeFormat>> _formats = {
  'qrCode': [ms.BarcodeFormat.qrCode],
  'aztec': [ms.BarcodeFormat.aztec],
  'dataMatrix': [ms.BarcodeFormat.dataMatrix],
  'pdf417': [ms.BarcodeFormat.pdf417],
  'code39': [ms.BarcodeFormat.code39],
  'code93': [ms.BarcodeFormat.code93],
  'code128': [ms.BarcodeFormat.code128],
  'codabar': [ms.BarcodeFormat.codabar],
  'ean8': [ms.BarcodeFormat.ean8],
  'ean13': [ms.BarcodeFormat.ean13],
  'itf': [ms.BarcodeFormat.itf14, ms.BarcodeFormat.itf2of5],
  'upcA': [ms.BarcodeFormat.upcA],
  'upcE': [ms.BarcodeFormat.upcE],
};

/// The schema's `BarcodeFormat` member for a scanner format, or null for
/// a format the schema does not know.
@visibleForTesting
String? formatName(ms.BarcodeFormat format) {
  for (final e in _formats.entries) {
    if (e.value.contains(format)) return e.key;
  }
  return null;
}

/// The scanner formats for schema members [names]; empty for every format.
@visibleForTesting
List<ms.BarcodeFormat> scannerFormats(List<String> names) => [
  for (final n in names) ...?_formats[n],
];

Widget _cameraPage(BuildContext context, List<String> formats, ScanSink sink) =>
    _CameraPage(formats: formats, sink: sink);

final class _CameraPage extends StatefulWidget {
  const _CameraPage({required this.formats, required this.sink});

  final List<String> formats;
  final ScanSink sink;

  @override
  State<_CameraPage> createState() => _CameraPageState();
}

final class _CameraPageState extends State<_CameraPage> {
  late final ms.MobileScannerController _controller =
      ms.MobileScannerController(
        formats: scannerFormats(widget.formats),
        detectionSpeed: ms.DetectionSpeed.noDuplicates,
      );

  @override
  void dispose() {
    unawaited(_controller.dispose());
    super.dispose();
  }

  void _detected(ms.BarcodeCapture capture) {
    for (final code in capture.barcodes) {
      final value = code.rawValue, format = formatName(code.format);
      if (value == null || format == null) continue;
      if (widget.formats.isNotEmpty && !widget.formats.contains(format)) {
        continue;
      }
      widget.sink.found(PluxScanResult(value: value, format: format));
      return;
    }
  }

  Widget _failed(BuildContext context, ms.MobileScannerException error) {
    final denied =
        error.errorCode == ms.MobileScannerErrorCode.permissionDenied;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      // The message holds the error code only (SEC-092).
      widget.sink.fail(
        denied
            ? const PluxDeviceException.denied(
                'the camera permission was denied',
              )
            : PluxDeviceException.unavailable(
                'the scanner failed: ${error.errorCode.name}',
              ),
      );
    });
    return const ColoredBox(color: Colors.black);
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    appBar: AppBar(
      backgroundColor: Colors.transparent,
      foregroundColor: Colors.white,
      leading: IconButton(
        icon: const Icon(Icons.close),
        tooltip: MaterialLocalizations.of(context).closeButtonTooltip,
        onPressed: widget.sink.cancel,
      ),
    ),
    extendBodyBehindAppBar: true,
    body: ms.MobileScanner(
      controller: _controller,
      onDetect: _detected,
      errorBuilder: _failed,
    ),
  );
}
