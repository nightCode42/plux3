// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What Plux shows in place of content it cannot render (RT-020, RT-022,
/// ADR-0031 § Fault isolation): the host's fallback for the plugin, else
/// its app-wide fallback, else [PluxDefaultFallback].
library;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:plux_flutter/src/core/config.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

/// Builds the fallback for [error] in content of [plugin] (null: not tied
/// to one plugin, such as a route no plugin has): the most specific of
/// [PluxConfig.pluginFallbackBuilders], [PluxConfig.fallbackBuilder] and
/// [PluxDefaultFallback].
Widget buildFallback(
  BuildContext context,
  PluxConfig config,
  PluxException error, {
  String? plugin,
}) {
  final builder =
      (plugin == null ? null : config.pluginFallbackBuilders[plugin]) ??
      config.fallbackBuilder;
  return builder?.call(context, error) ?? PluxDefaultFallback(error);
}

/// The fallback Plux shows when the host configures none: a neutral panel
/// in the colours of the surrounding theme, with an icon, the error code
/// in debug builds only, and a screen-reader label. It never shows the
/// error's message, which may name internals.
final class PluxDefaultFallback extends StatelessWidget {
  /// Creates the fallback for [error].
  const PluxDefaultFallback(this.error, {super.key});

  /// What failed.
  final PluxException error;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    return Semantics(
      container: true,
      label: 'Content unavailable',
      child: ColoredBox(
        color: scheme.surfaceContainerHighest,
        child: Center(
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: ExcludeSemantics(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(Icons.error_outline, color: scheme.onSurfaceVariant),
                  if (kDebugMode)
                    Text(
                      error.code.id,
                      style: theme.textTheme.labelSmall?.copyWith(
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
