// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// `PluxView` (HST-001, NAV-004 from P4) and the page host behind it and
/// behind `Plux.open`: a page's lease on the release it renders from
/// (SYN-004), its root error boundary (RT-020), fallbacks for switched-off
/// and failing plugins (RT-022), and its timeline events (RT-015).
library;

import 'dart:developer' as developer;

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/core/runtime.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// Shows a Plux page, by app-wide route name, inside any widget tree.
final class PluxView extends ConsumerWidget {
  /// Creates a view of [route] with [params].
  const PluxView(
    this.route, {
    super.key,
    this.params = const {},
    this.loadingBuilder,
    this.fallbackBuilder,
  });

  /// The route name (SCH-025).
  final String route;

  /// The page parameters.
  final Map<String, Object?> params;

  /// Shown while no release is available yet; empty when null.
  final WidgetBuilder? loadingBuilder;

  /// Shown instead of a page that cannot render; the app-level fallback of
  /// `PluxConfig.fallbackBuilder` when null (RT-022).
  final Widget Function(BuildContext context, PluxException error)?
  fallbackBuilder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final rt = ref.watch(pluxRuntimeProvider);
    final release = ref.watch(activeReleaseProvider);
    if (rt == null || release == null) {
      return loadingBuilder?.call(context) ?? const SizedBox.shrink();
    }
    Widget fallback(PluxException e) =>
        (fallbackBuilder ?? rt.config.fallbackBuilder)?.call(context, e) ??
        const SizedBox.shrink();
    final PageRef? page;
    try {
      page = release.page(route);
    } on PluxException catch (e) {
      return fallback(e);
    }
    if (page == null) {
      return fallback(
        PluxException(
          PluxErrorCode.resourceNotFound,
          'no page has the route $route',
        ),
      );
    }
    var shown = page;
    if (release.disabled(page.plugin)) {
      final message = release.control.message;
      final off = PluxException(
        PluxErrorCode.pluginDisabled,
        message.isEmpty ? 'plugin ${page.plugin} is switched off' : message,
        details: {'plugin': page.plugin, 'reason': 'killSwitch'},
      );
      // The plugin's own fallback page, unless the whole app is switched
      // off; otherwise the app-level fallback (RT-022).
      final PageRef? declared;
      try {
        declared = release.control.appKillSwitch
            ? null
            : release.fallbackPage(page.plugin);
      } on PluxException catch (e) {
        return fallback(e);
      }
      if (declared == null) return fallback(off);
      shown = declared;
    }
    return PluxPageHost(
      key: ValueKey((release.sequence, route, shown.pageKey)),
      runtime: rt,
      page: shown,
      params: params,
      fallback: fallback,
    );
  }
}

/// Hosts one page: holds a lease on its release while mounted, checks the
/// page section before first use (BND-006), contains failures and records
/// build and first-frame timing.
final class PluxPageHost extends StatefulWidget {
  /// Creates a host.
  const PluxPageHost({
    super.key,
    required this.runtime,
    required this.page,
    required this.params,
    required this.fallback,
  });

  /// The runtime.
  final PluxRuntime runtime;

  /// The page.
  final PageRef page;

  /// Its parameters.
  final Map<String, Object?> params;

  /// Builds the fallback for a failure.
  final Widget Function(PluxException error) fallback;

  @override
  State<PluxPageHost> createState() => _PluxPageHostState();
}

final class _PluxPageHostState extends State<PluxPageHost> {
  ActiveRelease? _release;
  PluxException? _failure;
  final Stopwatch _sinceMount = Stopwatch()..start();
  bool _reportedFrame = false;

  @override
  void initState() {
    super.initState();
    _release = widget.runtime.mount();
    try {
      _release?.gate.check(widget.page.section);
    } on PluxException catch (e) {
      _fail(e);
    }
  }

  @override
  void dispose() {
    final r = _release;
    if (r != null) widget.runtime.unmount(r);
    super.dispose();
  }

  void _fail(PluxException e) {
    _failure = e;
    widget.runtime.failure(e).ignore();
  }

  @override
  Widget build(BuildContext context) {
    final release = _release;
    final renderer = widget.runtime.renderer;
    if (_failure != null) return widget.fallback(_failure!);
    if (release == null || renderer == null) {
      return widget.fallback(
        const PluxException(PluxErrorCode.syncFailed, 'no release to render'),
      );
    }
    final task = developer.TimelineTask()
      ..start('plux.page.build', arguments: {'route': widget.page.route});
    Widget child;
    try {
      child = renderer.build(context, release, widget.page, widget.params);
    } on Object catch (e, stack) {
      final error = e is PluxException
          ? e
          : PluxException(
              PluxErrorCode.nodeBuildFailed,
              'page ${widget.page.route}: $e',
              details: {'route': widget.page.route},
            );
      developer.log('$error', name: 'plux', error: e, stackTrace: stack);
      _fail(error);
      child = widget.fallback(error);
    } finally {
      task.finish();
    }
    if (!_reportedFrame) {
      _reportedFrame = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        developer.Timeline.instantSync(
          'plux.page.firstFrame',
          arguments: {
            'route': widget.page.route,
            'ms': '${_sinceMount.elapsedMilliseconds}',
          },
        );
        widget.runtime.firstFrame();
      });
    }
    return child;
  }
}
