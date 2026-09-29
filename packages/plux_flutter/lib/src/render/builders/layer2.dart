// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Layer 2 components of P3 (WGT-020, Appendix C.9): standard empty,
/// error and offline states and skeleton placeholders, built only from
/// Layer 1 widgets, the page's theme and runtime services. Their
/// screen-reader script is docs/accessibility/layer2-screen-reader.md.
library;

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/render/decoders.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/plux_icon.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// `EmptyState`: icon, title, message and an optional action.
Widget buildEmptyState(NodeContext c) => _StatePanel(
  icon: c.decode(EmptyStateProps.icon, decodeIconData),
  title:
      c.decode(EmptyStateProps.title, asString) ??
      c.missing('EmptyState.title'),
  message: c.decode(EmptyStateProps.message, asString),
  actions: [?c.slot(EmptyStateSlots.action)],
);

/// `ErrorState`: as `EmptyState`, in the error colour, announced when it
/// appears, with a built-in retry button when `retryLabel` is set. The
/// button fires `onRetry`, and is disabled when nothing handles it.
Widget buildErrorState(NodeContext c) {
  final retry = c.decode(ErrorStateProps.retryLabel, asString);
  final handled = c.handles(ErrorStateEvents.onRetry);
  return _StatePanel(
    icon: c.decode(ErrorStateProps.icon, decodeIconData),
    title:
        c.decode(ErrorStateProps.title, asString) ??
        c.missing('ErrorState.title'),
    message: c.decode(ErrorStateProps.message, asString),
    error: true,
    actions: [
      if (retry != null)
        FilledButton.tonal(
          onPressed: handled ? () => c.fire(ErrorStateEvents.onRetry) : null,
          child: Text(retry),
        ),
      ?c.slot(ErrorStateSlots.action),
    ],
  );
}

/// The layout both states share.
final class _StatePanel extends StatelessWidget {
  const _StatePanel({
    required this.icon,
    required this.title,
    required this.message,
    required this.actions,
    this.error = false,
  });

  final PluxIconSource? icon;
  final String title;
  final String? message;
  final List<Widget> actions;
  final bool error;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final icon = this.icon;
    final message = this.message;
    return Semantics(
      container: true,
      liveRegion: error,
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              PluxIcon(
                icon,
                size: 48,
                color: error ? scheme.error : scheme.onSurfaceVariant,
              ),
              const SizedBox(height: 16),
            ],
            Semantics(
              container: true,
              header: true,
              child: Text(
                title,
                style: theme.textTheme.titleMedium,
                textAlign: TextAlign.center,
              ),
            ),
            if (message != null) ...[
              const SizedBox(height: 8),
              Text(
                message,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: scheme.onSurfaceVariant,
                ),
                textAlign: TextAlign.center,
              ),
            ],
            if (actions.isNotEmpty) ...[
              const SizedBox(height: 24),
              Wrap(
                alignment: WrapAlignment.center,
                spacing: 12,
                runSpacing: 12,
                children: actions,
              ),
            ],
          ],
        ),
      ),
    );
  }
}

/// `OfflineBanner`: while the device is offline — the last sync reached
/// no server — a banner with the message, announced when it appears.
/// `visible` false hides it whatever the connection.
Widget buildOfflineBanner(NodeContext c) {
  final message =
      c.decode(OfflineBannerProps.message, asString) ??
      c.missing('OfflineBanner.message');
  if (!(c.decode(OfflineBannerProps.visible, asBool) ?? true)) {
    return const SizedBox.shrink();
  }
  return Consumer(
    builder: (context, ref, _) {
      final shown = ref.watch(pluxOfflineProvider);
      final scheme = Theme.of(context).colorScheme;
      return AnimatedSize(
        duration: MediaQuery.disableAnimationsOf(context)
            ? Duration.zero
            : const Duration(milliseconds: 200),
        child: shown
            ? Semantics(
                container: true,
                liveRegion: true,
                child: ColoredBox(
                  color: scheme.inverseSurface,
                  child: Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 12,
                    ),
                    child: Text(
                      message,
                      style: Theme.of(context).textTheme.bodyMedium
                          ?.copyWith(color: scheme.onInverseSurface),
                    ),
                  ),
                ),
              )
            : const SizedBox(width: double.infinity),
      );
    },
  );
}

/// `SkeletonLoader`: `lines` placeholder lines, the last shorter, after a
/// round avatar when `avatar`; shimmering when `shimmer` unless the device
/// reduces motion. Placeholders carry no meaning, so screen readers skip
/// them.
Widget buildSkeletonLoader(NodeContext c) => _Skeleton(
  lines: math.max(1, c.decode(SkeletonLoaderProps.lines, asInt) ?? 3),
  avatar: c.decode(SkeletonLoaderProps.avatar, asBool) ?? false,
  shimmer: c.decode(SkeletonLoaderProps.shimmer, asBool) ?? true,
);

final class _Skeleton extends StatefulWidget {
  const _Skeleton({
    required this.lines,
    required this.avatar,
    required this.shimmer,
  });

  final int lines;
  final bool avatar;
  final bool shimmer;

  @override
  State<_Skeleton> createState() => _SkeletonState();
}

final class _SkeletonState extends State<_Skeleton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _shimmer = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1500),
  );

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _animate();
  }

  @override
  void didUpdateWidget(_Skeleton old) {
    super.didUpdateWidget(old);
    _animate();
  }

  void _animate() {
    if (widget.shimmer && !MediaQuery.disableAnimationsOf(context)) {
      if (!_shimmer.isAnimating) _shimmer.repeat();
    } else {
      _shimmer
        ..stop()
        ..value = 0;
    }
  }

  @override
  void dispose() {
    _shimmer.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final base = scheme.surfaceContainerHighest;
    Widget block(double height, {double? width, bool round = false}) =>
        Container(
          height: height,
          width: width,
          decoration: BoxDecoration(
            color: base,
            borderRadius: round ? null : BorderRadius.circular(4),
            shape: round ? BoxShape.circle : BoxShape.rectangle,
          ),
        );
    final content = Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (widget.avatar) ...[
          block(40, width: 40, round: true),
          const SizedBox(width: 16),
        ],
        Expanded(
          child: LayoutBuilder(
            builder: (context, box) => Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                for (var i = 0; i < widget.lines; i++) ...[
                  if (i > 0) const SizedBox(height: 8),
                  block(
                    12,
                    width: i == widget.lines - 1 && widget.lines > 1
                        ? box.maxWidth * 0.6
                        : box.maxWidth,
                  ),
                ],
              ],
            ),
          ),
        ),
      ],
    );
    return ExcludeSemantics(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: AnimatedBuilder(
          animation: _shimmer,
          child: content,
          builder: (context, child) => _shimmer.value == 0
              ? child!
              : ShaderMask(
                  blendMode: BlendMode.srcATop,
                  shaderCallback: (rect) => LinearGradient(
                    colors: [base, scheme.surfaceContainerLow, base],
                    stops: const [0.35, 0.5, 0.65],
                    begin: Alignment(-1 + 2 * _shimmer.value - 1, 0),
                    end: Alignment(1 + 2 * _shimmer.value - 1, 0),
                  ).createShader(rect),
                  child: child,
                ),
        ),
      ),
    );
  }
}
