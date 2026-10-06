// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The host's side of Plux in the shop: the navigator plugin pages open in,
/// and what the host knows about the network. The shop has no sign-in, so
/// there is no auth delegate. `main` creates one and gives it to both the
/// runtime's configuration and the app.
final class ExpressHost {
  /// Creates the host. [reportNetwork] tells Plux about the network;
  /// `Plux.setNetworkAvailable` unless a test replaces it.
  ExpressHost({void Function(bool available)? reportNetwork})
    : _reportNetwork = reportNetwork ?? Plux.setNetworkAvailable;

  final void Function(bool available) _reportNetwork;

  /// The app's navigator, where plugin pages open and close.
  final navigatorKey = GlobalKey<NavigatorState>();

  /// Whether the host believes the network is available. Plux queues the
  /// courier's delivery confirmations while it is not, and replays them
  /// when it is again (DAT-020).
  final online = ValueNotifier<bool>(true);

  /// Tells Plux, and the host's own switch, whether the network is
  /// available: what a connectivity plugin does when the device goes
  /// offline and back.
  void setOnline({required bool online}) {
    this.online.value = online;
    _reportNetwork(online);
  }
}
