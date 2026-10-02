// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Plux navigation through go_router (NAV-006, HST-031, ADR-0040).
///
/// Add [PluxGoRoutes.routes], and a [PluxGoRoutes.shell] per shell, to the
/// host's `GoRouter`, and pass `PluxGoRouter(router)` as
/// `PluxConfig.router`: Plux pages are pushed through the router, guards
/// run as the Plux route's redirect, and plugins open the router's named
/// routes as native routes.
library;

export 'src/adapter.dart' show PluxGoRouter, PluxGoRouterDelegate;
export 'src/routes.dart' show PluxGoBranch, PluxGoRoutes;
