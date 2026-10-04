// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_flutter/plux_flutter.dart';

import '../support/delegate_suite.dart';

/// The shared delegate suite on the default delegate: a plain `Navigator`
/// whose home is a `PluxView`, and a `Navigator` 2.0 pages list holding a
/// `PluxPage` (NAV-006).
void main() {
  delegateSuite('the plain Navigator', () {
    final key = GlobalKey<NavigatorState>();
    return DelegateHost(
      navigatorKey: key,
      app: () => MaterialApp(navigatorKey: key, home: const PluxView('home')),
    );
  });

  delegateSuite('a Navigator 2.0 pages list', () {
    final key = GlobalKey<NavigatorState>();
    return DelegateHost(
      navigatorKey: key,
      app: () => MaterialApp(
        home: Navigator(
          key: key,
          pages: [Plux.pageFor<void>('home')],
          onDidRemovePage: (_) {},
        ),
      ),
    );
  });
}
