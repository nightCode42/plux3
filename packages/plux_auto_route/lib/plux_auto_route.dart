// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux navigation through auto_route (NAV-006, HST-031, ADR-0040).
///
/// Add [PluxAutoRoutes.routes], and a [PluxAutoRoutes.shell] per shell, to
/// the host's `RootStackRouter`, and pass `PluxAutoRoute(router)` as
/// `PluxConfig.router`: Plux pages are pushed through the router, guards
/// run as the Plux route's `AutoRouteGuard`, and plugins open the router's
/// routes as native routes.
library;

export 'src/adapter.dart' show PluxAutoRoute, PluxAutoRouteDelegate;
export 'src/routes.dart' show PluxAutoBranch, PluxAutoRoutes;
