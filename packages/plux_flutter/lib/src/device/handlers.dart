// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The handlers of the feedback and device actions (SEC-080): messages,
/// haptics, the clipboard, external URLs, sharing and permissions in the
/// core, and the media, scanner and location actions through the optional
/// packages the host registers (RT-060). The capability each needs is
/// checked by the engine before a handler runs (`DeviceGuard.admit`); a
/// handler fails with a typed [ActionError] and never sees a path or a
/// scanned value in a message (SEC-092).
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/control.dart' show durationOf;
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/device/guard.dart';
import 'package:plux_flutter/src/device/platform.dart';
import 'package:plux_flutter/src/device/types.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

/// The permissions `requestPermission` asks for: the device APIs a platform
/// asks the user to permit.
const List<String> permissionNames = [
  'camera',
  'photos',
  'location',
  'contacts',
  'biometrics',
  'notifications',
];

/// The handlers of the feedback and device actions, by action name.
final Map<String, ActionHandler> deviceHandlers = {
  'showSnackbar': actionHandler(_showSnackbar, waitsForUser: true),
  'showToast': actionHandler(_showToast),
  'haptic': actionHandler(_haptic),
  'copyToClipboard': actionHandler(_copy),
  'openUrl': actionHandler(_openUrl, waitsForUser: true),
  'share': actionHandler(_share, waitsForUser: true),
  'requestPermission': actionHandler(_requestPermission, waitsForUser: true),
  'pickImage': actionHandler(_pickImage, waitsForUser: true),
  'capturePhoto': actionHandler(_capturePhoto, waitsForUser: true),
  'pickFile': actionHandler(_pickFile, waitsForUser: true),
  'scanCode': actionHandler(_scanCode, waitsForUser: true),
  'getLocation': actionHandler(_getLocation),
};

DeviceScope _scope(StepContext c, String action) =>
    c.device ??
    (throw ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.actionsNotAvailable,
      '$action: no runtime provides device actions here',
    ));

/// The implementation a registered package provides, or a typed error
/// naming the package the host lacks (RT-060).
T _package<T extends Object>(StepContext c, String action, String package) =>
    c.service<T>() ??
    (throw ActionError(
      ActionErrorKind.permission,
      PluxErrorCode.devicePackageMissing,
      '$action needs $package: add it to PluxConfig.devicePackages',
    ));

T _service<T extends Object>(StepContext c, String action) =>
    c.service<T>() ??
    (throw ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.actionsNotAvailable,
      '$action: the runtime installs no ${T.toString()}',
    ));

/// Runs a platform or package call, turning its [PluxDeviceException] into
/// the step's typed error.
Future<T> _guarded<T>(Future<T> Function() call) async {
  try {
    return await call();
  } on PluxDeviceException catch (e) {
    throw switch (e.failure) {
      PluxDeviceFailure.denied => ActionError(
        ActionErrorKind.permission,
        PluxErrorCode.devicePermissionDenied,
        e.message,
      ),
      PluxDeviceFailure.unavailable => ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.deviceUnavailable,
        e.message,
      ),
    };
  }
}

String _string(Map<String, Object?> i, String name, String action) {
  final v = i[name];
  return v is String
      ? v
      : throw ActionError.validation('$action: $name is not a string');
}

int? _optionalInt(Map<String, Object?> i, String name, String action) {
  final v = i[name];
  if (v == null) return null;
  if (v is int && v > 0) return v;
  throw ActionError.validation('$action: $name is not a positive integer');
}

List<String> _strings(Map<String, Object?> i, String name, String action) {
  final v = i[name];
  if (v == null) return const [];
  if (v is List<Object?> && v.every((e) => e is String)) {
    return v.cast<String>();
  }
  throw ActionError.validation('$action: $name is not a list of strings');
}

// ── Feedback ──────────────────────────────────────────────────────────────

FutureOr<StepResult> _showSnackbar(StepContext c, Map<String, Object?> i) {
  final message = _string(i, 'message', 'showSnackbar');
  final label = i['actionLabel'];
  final duration = durationOf(i['duration'] ?? 4000, 'showSnackbar');
  final context = _scope(c, 'showSnackbar').context?.call();
  final messenger = context == null ? null : ScaffoldMessenger.maybeOf(context);
  if (messenger == null) {
    throw const ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.deviceUnavailable,
      'showSnackbar: there is no Scaffold to show the message on',
    );
  }
  final controller = messenger.showSnackBar(
    SnackBar(
      content: Text(message),
      duration: duration,
      action: label is String
          ? SnackBarAction(label: label, onPressed: () {})
          : null,
    ),
  );
  if (label is! String) return const StepDone();
  return controller.closed.then<StepResult>(
    (reason) => reason == SnackBarClosedReason.action
        ? const StepDone(null, 'action')
        : const StepDone(),
  );
}

StepResult _showToast(StepContext c, Map<String, Object?> i) {
  final message = _string(i, 'message', 'showToast');
  final duration = durationOf(i['duration'] ?? 2000, 'showToast');
  final overlay = _scope(c, 'showToast').overlay?.call();
  if (overlay == null) {
    throw const ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.deviceUnavailable,
      'showToast: there is no overlay to show the message on',
    );
  }
  late final OverlayEntry entry;
  entry = OverlayEntry(
    builder: (context) {
      final theme = Theme.of(context);
      return IgnorePointer(
        child: SafeArea(
          child: Align(
            alignment: Alignment.bottomCenter,
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Semantics(
                liveRegion: true,
                child: Material(
                  color: theme.colorScheme.inverseSurface,
                  borderRadius: BorderRadius.circular(8),
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 10,
                    ),
                    child: Text(
                      message,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: theme.colorScheme.onInverseSurface,
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    },
  );
  overlay.insert(entry);
  Timer(duration, () {
    if (entry.mounted) entry.remove();
    entry.dispose();
  });
  return const StepDone();
}

Future<StepResult> _haptic(StepContext c, Map<String, Object?> i) async {
  final pattern = enumMember('HapticPattern', i['pattern']) ?? 'light';
  switch (pattern) {
    case 'light':
      await HapticFeedback.lightImpact();
    case 'medium':
      await HapticFeedback.mediumImpact();
    case 'heavy':
      await HapticFeedback.heavyImpact();
    case 'selection':
      await HapticFeedback.selectionClick();
    case 'vibrate':
      await HapticFeedback.vibrate();
    default:
      throw ActionError.validation('haptic: pattern is not a haptic pattern');
  }
  return const StepDone();
}

// ── Core device actions ───────────────────────────────────────────────────

Future<StepResult> _copy(StepContext c, Map<String, Object?> i) async {
  final scope = _scope(c, 'copyToClipboard');
  final text = _string(i, 'text', 'copyToClipboard');
  if (scope.secure) {
    throw const ActionError(
      ActionErrorKind.permission,
      PluxErrorCode.clipboardBlocked,
      'copyToClipboard: the clipboard is not written on a secure page',
    );
  }
  final max = scope.guard.limit(PluxLimit.deviceClipboardChars);
  if (text.length > max) {
    throw ActionError(
      ActionErrorKind.validation,
      PluxErrorCode.clipboardBlocked,
      'copyToClipboard: the text is over device.clipboardChars = $max',
    );
  }
  await Clipboard.setData(ClipboardData(text: text));
  return const StepDone();
}

Future<StepResult> _openUrl(StepContext c, Map<String, Object?> i) async {
  final scope = _scope(c, 'openUrl');
  final target = scope.guard.openTarget(
    scope.plugin,
    _string(i, 'url', 'openUrl'),
  );
  final opened = switch (target) {
    OpenInApp(:final uri) =>
      await (c.service<DeepLinkOpener>()?.open(uri) ?? Future.value(false)),
    OpenExternal(:final uri) => await _service<UrlOpener>(
      c,
      'openUrl',
    ).open(uri),
  };
  if (!opened) {
    throw const ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.deviceUnavailable,
      'openUrl: the platform could not open the address',
    );
  }
  return const StepDone();
}

Future<StepResult> _share(StepContext c, Map<String, Object?> i) async {
  final text = i['text'], url = i['url'], file = i['file'];
  if (text is! String? || url is! String?) {
    throw ActionError.validation('share: text and url are strings');
  }
  PluxPickedFile? picked;
  if (file != null) {
    picked = _service<PickedFiles>(c, 'share').find(file);
    if (picked == null) {
      throw ActionError.validation('share: file is not a picked file');
    }
  }
  if (text == null && url == null && picked == null) {
    throw ActionError.validation('share: nothing to share');
  }
  await _guarded(
    () => _service<DevicePlatform>(c, 'share').share(
      text: text,
      url: url,
      filePath: picked?.path,
      mimeType: picked?.mimeType,
    ),
  );
  return const StepDone();
}

Future<StepResult> _requestPermission(
  StepContext c,
  Map<String, Object?> i,
) async {
  final permission = _string(i, 'permission', 'requestPermission');
  if (!permissionNames.contains(permission)) {
    throw ActionError.validation(
      'requestPermission: $permission is not a permission',
    );
  }
  final granted = await _guarded(
    () => _service<DevicePlatform>(
      c,
      'requestPermission',
    ).requestPermission(permission),
  );
  return StepDone(granted, granted ? 'granted' : 'denied');
}

// ── Optional packages ─────────────────────────────────────────────────────

Future<StepResult> _pickImage(StepContext c, Map<String, Object?> i) async {
  final picker = _package<PluxMediaPicker>(c, 'pickImage', 'plux_media');
  final limit = _scope(c, 'pickImage').guard.limit(PluxLimit.devicePickCount);
  final multiple = i['multiple'] == true;
  final files = await _guarded(
    () => picker.pickImages(
      multiple: multiple,
      limit: multiple ? limit : 1,
      maxDimension: _optionalInt(i, 'maxDimension', 'pickImage'),
    ),
  );
  return _picked(c, files, limit);
}

Future<StepResult> _capturePhoto(StepContext c, Map<String, Object?> i) async {
  final picker = _package<PluxMediaPicker>(c, 'capturePhoto', 'plux_media');
  final file = await _guarded(
    () => picker.capturePhoto(
      maxDimension: _optionalInt(i, 'maxDimension', 'capturePhoto'),
    ),
  );
  if (file == null) return const StepDone(null, 'cancelled');
  return StepDone(_service<PickedFiles>(c, 'capturePhoto').add(file));
}

Future<StepResult> _pickFile(StepContext c, Map<String, Object?> i) async {
  final picker = _package<PluxMediaPicker>(c, 'pickFile', 'plux_media');
  final limit = _scope(c, 'pickFile').guard.limit(PluxLimit.devicePickCount);
  final multiple = i['multiple'] == true;
  final files = await _guarded(
    () => picker.pickFiles(
      mimeTypes: _strings(i, 'mimeTypes', 'pickFile'),
      multiple: multiple,
      limit: multiple ? limit : 1,
    ),
  );
  return _picked(c, files, limit);
}

StepResult _picked(StepContext c, List<PluxPickedFile> files, int limit) {
  if (files.isEmpty) return const StepDone(<Object?>[], 'cancelled');
  final store = _service<PickedFiles>(c, 'pick');
  return StepDone(<Object?>[for (final f in files.take(limit)) store.add(f)]);
}

Future<StepResult> _scanCode(StepContext c, Map<String, Object?> i) async {
  final scanner = _package<PluxCodeScanner>(c, 'scanCode', 'plux_scanner');
  final raw = i['formats'];
  if (raw != null && raw is! List<Object?>) {
    throw ActionError.validation('scanCode: formats is not a list');
  }
  final formats = <String>[];
  for (final f in (raw as List<Object?>?) ?? const <Object?>[]) {
    final name = enumMember('BarcodeFormat', f);
    if (name == null) {
      throw ActionError.validation('scanCode: formats holds a non-format');
    }
    formats.add(name);
  }
  final result = await _guarded(() => scanner.scan(formats));
  if (result == null) return const StepDone(null, 'cancelled');
  return StepDone({'value': result.value, 'format': result.format});
}

Future<StepResult> _getLocation(StepContext c, Map<String, Object?> i) async {
  final provider = _package<PluxLocationProvider>(
    c,
    'getLocation',
    'plux_location',
  );
  final accuracy = enumMember('LocationAccuracy', i['accuracy']) ?? 'balanced';
  if (!const ['low', 'balanced', 'high'].contains(accuracy)) {
    throw ActionError.validation('getLocation: accuracy is not an accuracy');
  }
  final p = await _guarded(() => provider.current(accuracy));
  return StepDone({
    'latitude': p.latitude,
    'longitude': p.longitude,
    'accuracy': p.accuracy,
    'altitude': p.altitude,
    'timestamp': PxlDateTime(p.timestamp.toUtc().millisecondsSinceEpoch, 0),
  });
}
