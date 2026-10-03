// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:plux_devtools/plux_devtools.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_starter/plux/plux.g.dart';
import 'package:plux_starter/src/config.dart';
import 'package:plux_starter/src/host.dart';
import 'package:plux_starter/src/mixed_screen.dart';

/// The app: a native home screen around a Plux page, with the Plux debug
/// overlay in debug builds.
final class StarterApp extends StatefulWidget {
  /// Creates the app for [config] and [host], after `Plux.initialize`
  /// returned [startup].
  const StarterApp({
    super.key,
    required this.config,
    required this.startup,
    required this.host,
  });

  /// Where the app's content comes from.
  final StarterConfig config;

  /// What start-up found.
  final PluxStartup startup;

  /// The host's side of Plux, which the runtime's configuration also has.
  final StarterHost host;

  @override
  State<StarterApp> createState() => _StarterAppState();
}

final class _StarterAppState extends State<StarterApp> {
  var _dark = false;
  var _analytics = false;

  @override
  Widget build(BuildContext context) => PluxScope(
    child: MaterialApp(
      title: 'Plux starter',
      navigatorKey: widget.host.navigatorKey,
      scaffoldMessengerKey: widget.host.messengerKey,
      debugShowCheckedModeBanner: false,
      theme: ThemeData(colorSchemeSeed: const Color(0xFF5B3DF5)),
      darkTheme: ThemeData(
        colorSchemeSeed: const Color(0xFF5B3DF5),
        brightness: Brightness.dark,
      ),
      themeMode: _dark ? ThemeMode.dark : ThemeMode.light,
      builder: (context, child) => PluxDevtools(child: child!),
      home: HomeScreen(
        config: widget.config,
        startup: widget.startup,
        host: widget.host,
        dark: _dark,
        analytics: _analytics,
        onDark: (v) {
          setState(() => _dark = v);
          Plux.setThemeMode(v ? ThemeMode.dark : ThemeMode.light);
        },
        onAnalytics: (v) {
          setState(() => _analytics = v);
          Plux.setConsent(PluxConsent(analytics: v));
        },
      ),
    ),
  );
}

/// The home screen: native controls above a published page, and the way
/// to the mixed screens.
final class HomeScreen extends StatelessWidget {
  /// Creates the screen.
  const HomeScreen({
    super.key,
    required this.config,
    required this.startup,
    required this.host,
    required this.dark,
    required this.analytics,
    required this.onDark,
    required this.onAnalytics,
  });

  /// Where the content comes from.
  final StarterConfig config;

  /// What start-up found.
  final PluxStartup startup;

  /// The host's side of Plux.
  final StarterHost host;

  /// Whether the dark theme is on.
  final bool dark;

  /// Whether the user shares analytics.
  final bool analytics;

  /// Turns the dark theme on or off.
  final ValueChanged<bool> onDark;

  /// Grants or withdraws analytics consent.
  final ValueChanged<bool> onAnalytics;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('Plux starter')),
    body: ListView(
      children: [
        const PluxSyncTile(),
        SwitchListTile(
          title: const Text('Share usage analytics'),
          value: analytics,
          onChanged: onAnalytics,
        ),
        SwitchListTile(
          title: const Text('Dark theme'),
          value: dark,
          onChanged: onDark,
        ),
        ListTile(
          key: const ValueKey('open-page'),
          leading: const Icon(Icons.open_in_full),
          title: Text('Open ${config.route}'),
          onTap: () => Plux.open<void>(context, config.route),
        ),
        // Both directions of a mixed screen (ADR-0023): plugin views in a
        // native screen, and a native slot in a plugin page.
        ListTile(
          key: const ValueKey('open-mixed'),
          leading: const Icon(Icons.splitscreen),
          title: const Text('Mixed screen'),
          onTap: () => Navigator.of(
            context,
          ).push(MaterialPageRoute<void>(builder: (_) => const MixedScreen())),
        ),
        // The typed routes plux codegen writes from the app (HST-030).
        ListTile(
          key: const ValueKey('open-places'),
          leading: const Icon(Icons.map_outlined),
          title: const Text('Places'),
          onTap: () => PluxScreens.places().push(context),
        ),
        const Divider(),
        SizedBox(
          height: 420,
          child: PluxView(
            config.route,
            loadingBuilder: (_) =>
                const Center(child: CircularProgressIndicator()),
            fallbackBuilder: (_, e) => Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  'The page is not available yet (${e.code.id}).',
                  textAlign: TextAlign.center,
                ),
              ),
            ),
          ),
        ),
        const Divider(),
        // A guarded page: signed out, its guard sends the user to the
        // sign-in page (NAV-009, HST-010).
        ValueListenableBuilder(
          valueListenable: host.signedIn,
          builder: (context, signedIn, _) => SwitchListTile(
            key: const ValueKey('sign-in'),
            title: const Text('Signed in'),
            value: signedIn,
            onChanged: (v) => host.signIn(signedIn: v),
          ),
        ),
        ListTile(
          key: const ValueKey('open-account'),
          leading: const Icon(Icons.account_circle_outlined),
          title: const Text('Account'),
          onTap: () => PluxScreens.account().push(context),
        ),
        // What a host's link handling and push SDK hand Plux (NAV-008).
        ListTile(
          key: const ValueKey('open-link'),
          leading: const Icon(Icons.link),
          title: const Text('Open a link'),
          subtitle: const LastShared(),
          onTap: () => Plux.handleDeepLink(
            Uri.parse('plux-starter://places/Lighthouse'),
          ),
        ),
        ListTile(
          key: const ValueKey('open-notification'),
          leading: const Icon(Icons.notifications_outlined),
          title: const Text('Open a notification'),
          onTap: () => Plux.handlePushPayload(const {
            'plux': '{"route": "place", "params": {"name": "Beach"}}',
          }),
        ),
        // A feature flag of the active release (ABT-006).
        if (PluxFlags.showTips)
          const ListTile(
            key: ValueKey('tip'),
            leading: Icon(Icons.lightbulb_outline),
            title: Text('Tip: a place page shares and opens its profile.'),
          ),
      ],
    ),
  );
}

/// The place a plugin page last shared, from its `placeShared` events
/// (HST-013).
final class LastShared extends StatefulWidget {
  /// Creates the line.
  const LastShared({super.key});

  @override
  State<LastShared> createState() => _LastSharedState();
}

final class _LastSharedState extends State<LastShared> {
  StreamSubscription<PlaceSharedEvent>? _events;
  String? _name;

  @override
  void initState() {
    super.initState();
    _events = PluxHostEvents.placeShared.listen(
      (e) => setState(() => _name = e.name),
    );
  }

  @override
  void dispose() {
    unawaited(_events?.cancel());
    super.dispose();
  }

  @override
  Widget build(BuildContext context) =>
      Text(_name == null ? 'Nothing shared yet' : 'Last shared: $_name');
}
