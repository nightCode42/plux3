// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Navigator 2.0 (NAV-006, ADR-0040): a Plux route as a `Page`, for apps
/// that build their navigator from a declarative pages list.
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/navigation/router.dart';

/// A Plux route as a page of a declarative `Navigator.pages` list. It is
/// resolved like any other navigation: an unknown name shows the
/// not-found page.
///
/// ```dart
/// Navigator(pages: [
///   const MaterialPage(child: HomeScreen()),
///   if (showLoan) Plux.pageFor('loan-calculator', params: {'productId': id}),
/// ], onDidRemovePage: (page) { ... });
/// ```
final class PluxPage<T> extends Page<T> {
  /// Creates the page of [route] with [params]; built by `Plux.pageFor`.
  const PluxPage({
    required this.router,
    required this.route,
    this.params = const {},
    super.key,
  }) : super(name: route, arguments: params);

  /// The router that resolves the route.
  final PluxRouter router;

  /// The app-wide route name.
  final String route;

  /// The route's parameters.
  final Map<String, Object?> params;

  @override
  Route<T> createRoute(BuildContext context) =>
      router.spec(route, params).toRoute<T>(context, settings: this);
}
