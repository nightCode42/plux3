// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The host's side of Plux in the starter: the native route and action
/// plugins use, the signed-in user guards read, and the keys Plux opens
/// links and notifications with. `main` creates one and gives it to both
/// the runtime's configuration and the app.
final class StarterHost implements PluxAuthDelegate {
  /// The app's navigator, where deep links and notifications open their
  /// pages (NAV-008).
  final navigatorKey = GlobalKey<NavigatorState>();

  /// The app's messenger, where the native `sharePlace` action reports.
  final messengerKey = GlobalKey<ScaffoldMessengerState>();

  /// Whether the user is signed in. The starter has no login: its home
  /// screen's switch stands for one.
  final signedIn = ValueNotifier<bool>(false);

  @override
  bool get isAuthenticated => signedIn.value;

  /// The starter calls no API of its own, so it holds no token.
  @override
  Future<String?> accessToken() async => null;

  @override
  Future<String?> refresh() async => null;

  @override
  void onLogout() => signIn(signedIn: false);

  /// Signs the user in or out: guards read it as `user.authenticated`
  /// (HST-010), and expressions read the user's plan as `user.tier`
  /// (HST-011).
  void signIn({required bool signedIn}) {
    this.signedIn.value = signedIn;
    Plux.setUserContext(
      signedIn
          ? const PluxUser(id: 'starter-user', attributes: {'tier': 'pro'})
          : null,
    );
  }

  /// The native routes plugin pages open with `navigate` (NAV-002): the
  /// host's profile screen of a place.
  Map<String, PluxNativeRoute<Object?, Object?>> get nativeRoutes => {
    'profile': PluxNativeRoute<String, void>(
      params: (json) => json['name']! as String,
      builder: (context, name) => ProfileScreen(name: name),
    ),
  };

  /// The native actions plugin pages call by name (ACT-060). A name
  /// never repeats a catalogue action's: a step names the catalogue's.
  Map<String, PluxNativeAction<Object?, Object?>> get nativeActions => {
    'sharePlace': PluxNativeAction<String, bool>(
      input: (json) => json['text']! as String,
      handler: share,
    ),
  };

  /// The starter's share: it shows [text] in a snack bar, and answers
  /// whether it could.
  bool share(String text) {
    final messenger = messengerKey.currentState;
    messenger?.showSnackBar(SnackBar(content: Text('Shared: $text')));
    return messenger != null;
  }
}

/// The host's profile screen of a place, which plugin pages open as the
/// native route `profile`.
final class ProfileScreen extends StatelessWidget {
  /// Creates the screen for the place [name].
  const ProfileScreen({super.key, required this.name});

  /// The place.
  final String name;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Profile')),
    body: Center(child: Text('Profile of $name')),
  );
}
