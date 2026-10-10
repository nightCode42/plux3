// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The runtime's platform channel, `dev.plux/runtime`, as the sync isolate
/// and the root isolate call it.
library;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

/// The `dev.plux/runtime` channel, implemented by `PluxFlutterPlugin` on
/// Android and iOS only.
///
/// Elsewhere a call fails at once with [MissingPluginException], as it does
/// on the root isolate: on a background isolate an unimplemented channel
/// call never completes, which would stall the sync isolate on a desktop.
final class RuntimeChannel {
  /// Creates the channel.
  const RuntimeChannel();

  static const _channel = MethodChannel('dev.plux/runtime');

  /// Invokes [method] with [arguments], as [MethodChannel.invokeMethod].
  Future<T?> invokeMethod<T>(String method, [Object? arguments]) {
    if (defaultTargetPlatform != TargetPlatform.android &&
        defaultTargetPlatform != TargetPlatform.iOS) {
      return Future.error(
        MissingPluginException(
          'No implementation found for method $method on channel '
          '${_channel.name}',
        ),
      );
    }
    return _channel.invokeMethod<T>(method, arguments);
  }
}
