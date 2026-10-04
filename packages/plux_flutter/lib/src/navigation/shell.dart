// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Tabbed shells (NAV-005, NAV-006, ADR-0040): a shell of the app document
/// shown with one nested `Navigator` per tab, so each tab keeps its own
/// stack, and `switchTab` selects a tab of the enclosing shell.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/plux_icon.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/state/providers.dart';

/// Lets the pages of a shell select one of its tabs.
final class PluxShellScope extends InheritedWidget {
  /// Provides the shell's tab selection.
  const PluxShellScope({
    super.key,
    required this.tabs,
    required this.current,
    required this.onSelect,
    required super.child,
  });

  /// The keys of the shell's tabs, in order.
  final List<String> tabs;

  /// The selected tab's index.
  final int current;

  /// Selects a tab by index.
  final ValueChanged<int> onSelect;

  /// Selects [tab]; false when the shell has no such tab.
  bool select(String tab) {
    final i = tabs.indexOf(tab);
    if (i < 0) return false;
    onSelect(i);
    return true;
  }

  /// The enclosing shell, or null.
  static PluxShellScope? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<PluxShellScope>();

  @override
  bool updateShouldNotify(PluxShellScope old) =>
      old.current != current || old.tabs.length != tabs.length;
}

/// Shows the shell [shell] of the app document: a bottom navigation bar
/// of its tabs, each tab a nested `Navigator` that starts at the tab's
/// initial route and keeps its stack while other tabs are shown.
///
/// ```dart
/// MaterialApp(home: PluxScope(child: PluxShell('main')))
/// ```
final class PluxShell extends ConsumerStatefulWidget {
  /// Creates the shell named [shell], starting at [initialTab] or its
  /// first tab.
  const PluxShell(this.shell, {super.key, this.initialTab})
    : body = null,
      currentIndex = null,
      onSelect = null;

  /// The bar of the shell [shell] around [body], for a router that keeps
  /// each tab's stack itself, such as `plux_go_router`'s
  /// `StatefulShellRoute` (ADR-0040): tab [currentIndex] is selected, and
  /// selecting a tab, or a `switchTab` step, calls [onSelect] with its
  /// index.
  const PluxShell.routed(
    this.shell, {
    super.key,
    required Widget this.body,
    required int this.currentIndex,
    required ValueChanged<int> this.onSelect,
  }) : initialTab = null;

  /// The shell's key in the app document.
  final String shell;

  /// The key of the tab shown first.
  final String? initialTab;

  /// The tabs' content when a router keeps their stacks; null when the
  /// shell keeps one `Navigator` per tab.
  final Widget? body;

  /// The selected tab's index when a router keeps the stacks.
  final int? currentIndex;

  /// Selects a tab when a router keeps the stacks.
  final ValueChanged<int>? onSelect;

  @override
  ConsumerState<PluxShell> createState() => _PluxShellState();
}

final class _PluxShellState extends ConsumerState<PluxShell> {
  int? _current;
  final Map<String, GlobalKey<NavigatorState>> _navigators = {};

  fbs.Shell? _find(ActiveRelease release) {
    for (final s in release.meta('').shells ?? const <fbs.Shell>[]) {
      if (s.key == widget.shell) return s;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final release = ref.watch(activeReleaseProvider);
    final rt = ref.watch(pluxRuntimeProvider);
    if (release == null || rt == null) return const SizedBox.shrink();
    final shell = _find(release);
    final tabs = shell?.tabs ?? const <fbs.ShellTab>[];
    if (shell == null || tabs.isEmpty) {
      final e = PluxException(
        PluxErrorCode.routeNotFound,
        'the app has no shell ${widget.shell}',
        details: {'route': widget.shell},
      );
      rt.reportProblem(e);
      return rt.config.fallbackBuilder?.call(context, e) ??
          const SizedBox.shrink();
    }
    final keys = [for (final t in tabs) t.key ?? ''];
    final routed = widget.body;
    final current = routed != null
        ? widget.currentIndex!.clamp(0, tabs.length - 1)
        : _current ??= () {
            final i = keys.indexOf(widget.initialTab ?? '');
            return i < 0 ? 0 : i;
          }();
    void select(int i) =>
        routed != null ? widget.onSelect!(i) : setState(() => _current = i);
    final renderer = rt.renderer;
    final env = ref.watch(environmentProvider);
    final labels = renderer is PluxRenderer
        ? [for (final t in tabs) renderer.shellLabel(release, t, env) ?? '']
        : [for (final t in tabs) t.key ?? ''];
    final icons = renderer is PluxRenderer
        ? [for (final t in tabs) renderer.shellIcon(release, t)]
        : [for (final _ in tabs) null];
    final navigators = [
      if (routed == null)
        for (final t in tabs)
          Navigator(
            key: _navigators.putIfAbsent(
              '${t.key}',
              GlobalKey<NavigatorState>.new,
            ),
            onGenerateInitialRoutes: (navigator, _) => [
              rt.router
                  .spec(t.initialRoute ?? '', const {})
                  .toRoute<Object?>(navigator.context),
            ],
          ),
    ];
    final scaffold = Scaffold(
      body: routed ?? IndexedStack(index: current, children: navigators),
      bottomNavigationBar: NavigationBar(
        selectedIndex: current,
        onDestinationSelected: select,
        destinations: [
          for (var i = 0; i < tabs.length; i++)
            NavigationDestination(
              icon: icons[i] == null
                  ? const SizedBox.square(dimension: 24)
                  : PluxIcon(icons[i]),
              label: labels[i],
            ),
        ],
      ),
    );
    return PluxShellScope(
      tabs: keys,
      current: current,
      onSelect: select,
      // A router that keeps the stacks handles the back button itself.
      child: routed != null
          ? scaffold
          : NavigatorPopHandler(
              onPopWithResult: (_) =>
                  _navigators[keys[current]]?.currentState?.maybePop(),
              child: scaffold,
            ),
    );
  }
}

/// The first page of the tab [tab] of the shell [shell]: the tab's initial
/// route, entered in place through its guards, as `PluxShell` shows it. A
/// router adapter shows it at the root of each tab's branch (ADR-0040). A
/// shell or tab the app document lacks shows the fallback and reports
/// `PLX-4100`.
final class PluxShellTab extends ConsumerWidget {
  /// Creates the first page of [tab] in [shell].
  const PluxShellTab(this.shell, this.tab, {super.key});

  /// The shell's key in the app document.
  final String shell;

  /// The tab's key in the shell.
  final String tab;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final release = ref.watch(activeReleaseProvider);
    final rt = ref.watch(pluxRuntimeProvider);
    if (release == null || rt == null) return const SizedBox.shrink();
    String? route;
    for (final s in release.meta('').shells ?? const <fbs.Shell>[]) {
      if (s.key != shell) continue;
      for (final t in s.tabs ?? const <fbs.ShellTab>[]) {
        if (t.key == tab) route = t.initialRoute;
      }
    }
    if (route == null) {
      final e = PluxException(
        PluxErrorCode.routeNotFound,
        'the app has no tab $tab in shell $shell',
        details: {'route': '$shell/$tab'},
      );
      rt.reportProblem(e);
      return rt.config.fallbackBuilder?.call(context, e) ??
          const SizedBox.shrink();
    }
    return rt.router.spec(route, const {}).builder(context);
  }
}
