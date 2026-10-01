// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The contract between the page host (a page's lease, boundary and
/// timing) and the renderer that turns its section into widgets
/// (ADR-0031).
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/core/active_release.dart';

/// Builds the widget tree of one page.
abstract interface class PageRenderer {
  /// Builds [page] of [release] with the route [params].
  Widget build(
    BuildContext context,
    ActiveRelease release,
    PageRef page,
    Map<String, Object?> params,
  );
}
