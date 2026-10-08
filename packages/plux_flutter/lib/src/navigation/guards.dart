// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Route guards (NAV-009, ADR-0040): before a page is entered, its
/// plugin's kill switch, the assurance level it asks for and its guard
/// graphs decide whether it opens, redirects or shows its fallback. Every
/// entry runs them: `Plux.open`, `navigate`, `PluxView`, `PluxPage`,
/// shells, deep links and push payloads.
library;

import 'dart:async';

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/core/active_release.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/verify/bundle_verifier.dart';

/// What the guards of a route decide.
sealed class GuardVerdict {
  const GuardVerdict();
}

/// Show [route] with [params]: the route asked for, or the one its guards
/// redirected to.
final class GuardEnter extends GuardVerdict {
  /// Creates the verdict.
  const GuardEnter(this.route, this.params);

  /// The route to show.
  final String route;

  /// Its parameters, in the host's form.
  final Map<String, Object?> params;
}

/// Show the fallback of [route] of [plugin]: a guard refused it, its
/// redirects come back to a route already visited (`PLX-4102`), or it asks
/// for an assurance level this device does not have (`PLX-6002`). [reason]
/// says which.
final class GuardRefused extends GuardVerdict {
  /// Creates the verdict.
  const GuardRefused(this.route, this.plugin, this.reason);

  /// The route refused.
  final String route;

  /// Its plugin, whose fallback is shown.
  final String plugin;

  /// Why.
  final PluxException reason;
}

/// What one guard graph decided.
sealed class GuardOutcome {
  const GuardOutcome();
}

/// The guard lets the route open.
final class GuardAllows extends GuardOutcome {
  /// Creates the outcome.
  const GuardAllows();
}

/// The guard shows the route's fallback; [why] says what failed when the
/// guard itself did not decide, which fails closed.
final class GuardFallsBack extends GuardOutcome {
  /// Creates the outcome.
  const GuardFallsBack([this.why]);

  /// What failed, or null when the guard decided so.
  final String? why;
}

/// The guard opens [route] instead, with [params] as text, converted by
/// that route's parameter types as a deep link's are.
final class GuardRedirects extends GuardOutcome {
  /// Creates the outcome.
  const GuardRedirects(this.route, this.params);

  /// The route to open instead.
  final String route;

  /// Its parameters as text.
  final Map<String, String> params;
}

/// Runs guard graph [guard] of [page] over the page's [params].
typedef GuardRunner = Future<GuardOutcome> Function(
  ActiveRelease release,
  PageRef page,
  UuidKey guard,
  Map<String, Object?> params,
);

/// Converts a redirect's text [params] by the parameter types of [page]
/// into the host's form; throws [FormatException] when one does not
/// convert.
typedef TextParams = Map<String, Object?> Function(
  ActiveRelease release,
  PageRef page,
  Map<String, String> params,
);

/// What a page asks of its entry: its guard graphs and the assurance level
/// it requires.
typedef GuardRequirements = ({List<UuidKey> guards, int assurance});

/// Decides each entry's route (NAV-009).
final class RouteGuards {
  /// Creates the guards over the release [release] gives.
  RouteGuards({
    required this.release,
    required this.run,
    required this.convert,
    required this.report,
    required this.assurance,
  });

  /// The active release, or null before the first one.
  final ActiveRelease? Function() release;

  /// Runs one guard graph.
  final GuardRunner run;

  /// Converts a redirect's parameters.
  final TextParams convert;

  /// Reports a refusal.
  final void Function(PluxException error) report;

  /// The assurance level (0 to 3) this device has now, read at every
  /// decision: a page that asks for more takes its fallback (SEC-007).
  final int Function() assurance;

  /// What [page] requires, when that can be read now: its section is
  /// small or already checked, so reading it does no large hashing on the
  /// UI isolate (L-6). Null otherwise, and when the section fails its
  /// check, which the page host reports.
  static GuardRequirements? requirementsNow(
    ActiveRelease release,
    PageRef page,
  ) {
    final s = page.section;
    if (s.data.length > SectionGate.uiIsolateLimit && !release.gate.passed(s)) {
      return null;
    }
    try {
      release.gate.check(s);
    } on PluxException {
      return null;
    }
    final p = fbs.Page(s.data);
    return (
      guards: [for (final g in p.guards ?? const <fbs.Uuid>[]) uuidOf(g)],
      assurance: p.requiresAssurance,
    );
  }

  /// Whether entering [page] needs [decide]: it has guards or asks for an
  /// assurance level above this device's [assurance], or what it asks
  /// cannot be read without waiting.
  static bool needsDecision(
    ActiveRelease release,
    PageRef page,
    int assurance,
  ) {
    if (release.disabled(page.plugin)) return false; // the kill switch first
    final r = requirementsNow(release, page);
    return r == null || r.guards.isNotEmpty || r.assurance > assurance;
  }

  /// Decides the entry of [route] with [params]: the kill switch is seen
  /// first, then the assurance level, then each guard in order. A redirect
  /// enters its target the same way; one that comes back to a route
  /// already visited is refused. Never throws.
  Future<GuardVerdict> decide(String route, Map<String, Object?> params) async {
    final r = release();
    if (r == null) return GuardEnter(route, params);
    final visited = <String>[];
    var name = route;
    var current = params;
    while (true) {
      if (visited.contains(name)) {
        return _refuse(
          visited.first,
          r,
          'its guards redirect in a cycle: ${[...visited, name].join(' → ')}',
        );
      }
      visited.add(name);
      final PageRef? ref;
      try {
        ref = r.page(name);
      } on PluxException {
        return GuardEnter(name, current); // the page host reports it
      }
      // An unknown route shows the not-found page; a switched-off plugin
      // its fallback page (RT-022), before any guard runs.
      if (ref == null || r.disabled(ref.plugin)) {
        return GuardEnter(name, current);
      }
      try {
        await r.gate.prepare([
          ...r.bundle(ref.plugin).container.sections,
          ...r.bundle('').container.sections,
        ]);
      } on Object {
        return GuardEnter(name, current); // the page host reports it
      }
      final needs = requirementsNow(r, ref);
      if (needs == null) return GuardEnter(name, current);
      final have = assurance();
      if (needs.assurance > have) {
        return _refuse(
          name,
          r,
          'it requires assurance AL${needs.assurance}, and this device has '
          'AL$have (SEC-007)',
          plugin: ref.plugin,
          code: PluxErrorCode.assuranceInsufficient,
        );
      }
      GuardRedirects? redirect;
      for (final g in needs.guards) {
        final outcome = await run(r, ref, g, current);
        switch (outcome) {
          case GuardAllows():
            continue;
          case GuardFallsBack(:final why):
            return _refuse(
              name,
              r,
              why ?? 'a guard refused it',
              plugin: ref.plugin,
            );
          case GuardRedirects():
            redirect = outcome;
        }
        break;
      }
      if (redirect == null) return GuardEnter(name, current);
      final PageRef? target;
      try {
        target = r.page(redirect.route);
      } on PluxException {
        return GuardEnter(redirect.route, redirect.params);
      }
      if (target == null) {
        // The not-found page, reported where it is shown (NAV-011).
        return GuardEnter(redirect.route, redirect.params);
      }
      try {
        await r.gate.prepare(r.bundle(target.plugin).container.sections);
        current = convert(r, target, redirect.params);
      } on PluxException {
        return GuardEnter(
          redirect.route,
          redirect.params,
        ); // its host reports it
      } on FormatException catch (e) {
        return _refuse(
          name,
          r,
          'its guard redirects to ${redirect.route} with parameters that do '
          'not convert: ${e.message}',
          plugin: ref.plugin,
        );
      }
      name = redirect.route;
    }
  }

  GuardRefused _refuse(
    String route,
    ActiveRelease release,
    String why, {
    String? plugin,
    PluxErrorCode code = PluxErrorCode.navigationRefused,
  }) {
    final p = plugin ?? _pluginOf(release, route);
    final e = PluxException(
      code,
      'route $route: $why',
      details: {'route': route, 'plugin': p},
    );
    report(e);
    return GuardRefused(route, p, e);
  }

  static String _pluginOf(ActiveRelease release, String route) {
    try {
      return release.page(route)?.plugin ?? '';
    } on PluxException {
      return '';
    }
  }
}
