// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Enforcement of a plugin's declared capabilities on device actions
/// (SEC-080): an operation the plugin does not declare, or that the host
/// narrowed away, is blocked before it reaches the device, and reported.
library;

import 'package:flutter/widgets.dart' show BuildContext, OverlayState;
import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/assets/image_providers.dart'
    show domainAllowed;
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/navigation/deep_links.dart';
import 'package:plux_flutter/src/schema/limits.g.dart';

/// Where `openUrl` sends an address.
sealed class OpenTarget {
  const OpenTarget(this.uri);

  /// The address.
  final Uri uri;
}

/// A link the app answers: opened as a route of the app.
final class OpenInApp extends OpenTarget {
  /// Creates the target.
  const OpenInApp(super.uri);
}

/// An HTTPS address on a domain the plugin declares: opened outside the
/// app.
final class OpenExternal extends OpenTarget {
  /// Creates the target.
  const OpenExternal(super.uri);
}

/// What a plugin declares, as the guard asks for it.
typedef PluginCapabilities = ({
  List<String> deviceApis,
  List<String> networkDomains,
});

/// Checks device actions against the capabilities of the plugin that runs
/// them (SEC-080). Without a declaration nothing is allowed.
final class DeviceGuard {
  /// Creates the guard. [capabilities] reads a plugin's declaration from
  /// the active release (`''` is the app); [deepLinks] the app's links;
  /// [allowed], when given, narrows the device APIs further
  /// (`PluxConfig.allowedCapabilities`); [report] receives each blocked
  /// operation.
  DeviceGuard({
    required this.capabilities,
    required this.deepLinks,
    required this.report,
    this.allowed,
    this.limits = _noLimits,
  });

  static Map<String, int> _noLimits() => const {};

  /// The limits of the active app bundle (LIM-004).
  final Map<String, int> Function() limits;

  /// The value of limit [l] in the active app bundle, or its default.
  int limit(PluxLimit l) => limits()[l.key] ?? l.defaultValue;

  /// A plugin's declared capabilities.
  final PluginCapabilities Function(String plugin) capabilities;

  /// The app's deep links.
  final fbs.DeepLinks? Function() deepLinks;

  /// Reports a blocked operation.
  final void Function(PluxException error) report;

  /// The device APIs the host allows, or null for all it is asked for.
  final Set<String>? allowed;

  /// The device API each action needs; `requestPermission` needs the
  /// permission it names.
  static const Map<String, String> apiOf = {
    'haptic': 'haptics',
    'copyToClipboard': 'clipboard',
    'share': 'share',
    'pickImage': 'photos',
    'capturePhoto': 'camera',
    'pickFile': 'files',
    'scanCode': 'camera',
    'getLocation': 'location',
  };

  /// Admits a step running [action] with [inputs] for [plugin], or fails
  /// with a `permission` [ActionError] (`PLX-5400`) after reporting it.
  void admit(String plugin, String action, Map<String, Object?> inputs) {
    final api = action == 'requestPermission'
        ? inputs['permission']
        : apiOf[action];
    if (api is! String) return;
    final declared = capabilities(plugin).deviceApis;
    final String? why;
    if (!declared.contains(api)) {
      why = 'the plugin does not declare the device API $api';
    } else if (allowed != null && !allowed!.contains(api)) {
      why = 'the host does not allow the device API $api';
    } else {
      return;
    }
    final message = '$action blocked: $why';
    report(
      PluxException(
        PluxErrorCode.deviceCapabilityBlocked,
        message,
        details: {'plugin': plugin, 'action': action, 'api': api},
      ),
    );
    throw ActionError(
      ActionErrorKind.permission,
      PluxErrorCode.deviceCapabilityBlocked,
      message,
    );
  }

  /// Where `openUrl` sends [raw] for [plugin]: into the app when the app's
  /// links answer it, outside when it is HTTPS on a domain the plugin
  /// declares; else the step fails with `PLX-5404` after the report.
  OpenTarget openTarget(String plugin, String raw) {
    final uri = Uri.tryParse(raw);
    if (uri != null && uri.hasScheme) {
      if (resolveDeepLink(deepLinks(), uri) != null) return OpenInApp(uri);
      if (uri.scheme.toLowerCase() == 'https' &&
          uri.host.isNotEmpty &&
          domainAllowed(uri.host, capabilities(plugin).networkDomains)) {
        return OpenExternal(uri);
      }
    }
    // Only the address's scheme and host: the rest may carry user data
    // (SEC-092).
    final where = uri == null ? 'an address' : '${uri.scheme}://${uri.host}';
    final message =
        'openUrl blocked: $where is not a link of the app or on a domain the '
        'plugin declares';
    report(
      PluxException(
        PluxErrorCode.openUrlBlocked,
        message,
        details: {'plugin': plugin},
      ),
    );
    throw ActionError(
      ActionErrorKind.permission,
      PluxErrorCode.openUrlBlocked,
      message,
    );
  }
}

/// What a device action knows about where it runs.
final class DeviceScope {
  /// Creates the scope of runs of [plugin] (`''` for the app).
  const DeviceScope({
    required this.plugin,
    required this.guard,
    this.secure = false,
    this.context,
    this.overlay,
  });

  /// The plugin whose steps run.
  final String plugin;

  /// Checks the plugin's device operations.
  final DeviceGuard guard;

  /// Whether the page the run belongs to is marked secure (SEC-090).
  final bool secure;

  /// The context a snackbar finds its `ScaffoldMessenger` from, or null
  /// where there is none.
  final BuildContext? Function()? context;

  /// The overlay a toast shows on, or null where there is none.
  final OverlayState? Function()? overlay;
}
