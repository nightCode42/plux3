// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The host's side of Plux in the bank: the session token plugin pages
/// authenticate with, and the native action that hands it over. `main`
/// creates one and gives it to both the runtime's configuration and the app.
///
/// The token lives in memory only: nothing writes it to disk, and the end
/// of the session (`logout`, `Plux.wipeData`) drops it.
final class BankHost implements PluxAuthDelegate {
  /// The app's navigator, where plugin pages open and close.
  final navigatorKey = GlobalKey<NavigatorState>();

  String? _token;

  @override
  bool get isAuthenticated => _token != null;

  @override
  Future<String?> accessToken() async => _token;

  /// The bank's tokens are not refreshable: a `401` means signing in again.
  @override
  Future<String?> refresh() async {
    _token = null;
    return null;
  }

  @override
  void onLogout() {
    _token = null;
  }

  /// Keeps [token] as the session's. Whether it was kept: a blank token is
  /// not one.
  bool signIn(String token) {
    if (token.isEmpty) return false;
    _token = token;
    return true;
  }

  /// The native actions plugin pages call by name (ACT-060): `bankSignIn`
  /// hands the token a successful login returned to the host.
  Map<String, PluxNativeAction<Object?, Object?>> get nativeActions => {
    'bankSignIn': PluxNativeAction<String, bool>(
      input: (json) => json['token']! as String,
      handler: signIn,
    ),
  };
}
