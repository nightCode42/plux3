// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The seam every Plux navigation goes through (ADR-0040): the
/// operations of NAV-005 over routes Plux describes, with the host's plain
/// `Navigator` as the default; router adapters implement the same
/// interface in their own packages.
library;

import 'package:flutter/material.dart';

/// How a route is shown (NAV-005).
enum PluxPresentation {
  /// A full page.
  page,

  /// A dialog over the current page.
  dialog,

  /// A modal bottom sheet.
  bottomSheet,

  /// A full-screen dialog.
  fullscreenDialog,
}

/// A page transition (NAV-010), as `routeOptions.transition` names it.
enum PluxTransition {
  /// The platform's default; Android's predictive back where supported.
  platform,

  /// A cross-fade.
  fade,

  /// The page slides in from the right, towards the left.
  slideLeft,

  /// The page slides in from the left, towards the right.
  slideRight,

  /// The page slides in from the bottom.
  slideUp,

  /// The page slides in from the top.
  slideDown,

  /// The page grows from the centre.
  scale,

  /// Material's shared-axis transition along the horizontal axis.
  sharedAxis,

  /// No animation.
  none;

  /// The transition a page names, or [platform] for none or one this
  /// runtime does not know.
  static PluxTransition parse(String? name) =>
      PluxTransition.values.asNameMap()[name] ?? platform;
}

/// A route Plux asks the delegate to show: its app-wide name, how it is
/// presented and moves in, and how to build its content.
final class PluxRouteSpec {
  /// Creates a route.
  const PluxRouteSpec({
    required this.name,
    required this.builder,
    this.presentation = PluxPresentation.page,
    this.transition = PluxTransition.platform,
    this.dismissible = true,
    this.arguments,
  });

  /// The app-wide route name, the route's `RouteSettings.name`.
  final String name;

  /// Builds the content.
  final WidgetBuilder builder;

  /// How it is shown.
  final PluxPresentation presentation;

  /// How it moves in.
  final PluxTransition transition;

  /// Whether a dialog or sheet closes when tapped outside or swiped away.
  final bool dismissible;

  /// The route's parameters, as `RouteSettings.arguments`.
  final Object? arguments;

  /// The Flutter route that shows this spec, with [settings] when a
  /// `Page` owns it.
  Route<T> toRoute<T>(BuildContext context, {RouteSettings? settings}) {
    settings ??= RouteSettings(name: name, arguments: arguments);
    switch (presentation) {
      case PluxPresentation.dialog:
        return DialogRoute<T>(
          context: context,
          settings: settings,
          barrierDismissible: dismissible,
          builder: (c) => Dialog(child: builder(c)),
        );
      case PluxPresentation.bottomSheet:
        return ModalBottomSheetRoute<T>(
          settings: settings,
          isDismissible: dismissible,
          enableDrag: dismissible,
          isScrollControlled: true,
          builder: builder,
        );
      case PluxPresentation.fullscreenDialog:
        return MaterialPageRoute<T>(
          settings: settings,
          fullscreenDialog: true,
          builder: builder,
        );
      case PluxPresentation.page:
        return PluxPageRoute<T>(
          settings: settings,
          transition: transition,
          builder: builder,
        );
    }
  }
}

/// The page route of a Plux page: Material's, with the page's transition
/// (NAV-010). The platform default uses Android's predictive-back
/// transition, which follows the back gesture on Android 14 and later and
/// fades forwards elsewhere on Android; other platforms keep the host
/// theme's.
final class PluxPageRoute<T> extends MaterialPageRoute<T> {
  /// Creates the route.
  PluxPageRoute({
    required super.builder,
    required this.transition,
    super.settings,
  });

  /// The transition.
  final PluxTransition transition;

  @override
  Duration get transitionDuration => transition == PluxTransition.none
      ? Duration.zero
      : super.transitionDuration;

  @override
  Duration get reverseTransitionDuration => transition == PluxTransition.none
      ? Duration.zero
      : super.reverseTransitionDuration;

  @override
  Widget buildTransitions(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) {
    Widget slideIn(Offset from) => SlideTransition(
      position: Tween(
        begin: from,
        end: Offset.zero,
      ).chain(CurveTween(curve: Curves.easeOutCubic)).animate(animation),
      child: child,
    );
    switch (transition) {
      case PluxTransition.platform:
        if (Theme.of(context).platform == TargetPlatform.android) {
          return const PredictiveBackPageTransitionsBuilder().buildTransitions(
            this,
            context,
            animation,
            secondaryAnimation,
            child,
          );
        }
        return super.buildTransitions(
          context,
          animation,
          secondaryAnimation,
          child,
        );
      case PluxTransition.fade:
        return FadeTransition(opacity: animation, child: child);
      case PluxTransition.slideLeft:
        return slideIn(const Offset(1, 0));
      case PluxTransition.slideRight:
        return slideIn(const Offset(-1, 0));
      case PluxTransition.slideUp:
        return slideIn(const Offset(0, 1));
      case PluxTransition.slideDown:
        return slideIn(const Offset(0, -1));
      case PluxTransition.scale:
        return ScaleTransition(
          scale: CurveTween(curve: Curves.easeOutCubic).animate(animation),
          child: child,
        );
      case PluxTransition.sharedAxis:
        // The incoming page fades in while moving 30 logical pixels along
        // the axis; the outgoing one fades out (Material motion).
        return FadeTransition(
          opacity: CurveTween(curve: Curves.easeOut).animate(animation),
          child: AnimatedBuilder(
            animation: animation,
            builder: (_, c) => Transform.translate(
              offset: Offset(30 * (1 - animation.value), 0),
              child: c,
            ),
            child: FadeTransition(
              opacity: ReverseAnimation(secondaryAnimation),
              child: child,
            ),
          ),
        );
      case PluxTransition.none:
        return child;
    }
  }
}

/// The seam every navigation goes through (ADR-0040). Plux resolves the
/// route, runs its checks and builds a [PluxRouteSpec]; the delegate
/// changes the navigation stack. Implementations never throw into Plux
/// for a stack that cannot change: they return false or null.
abstract interface class PluxNavigationDelegate {
  /// Pushes [route]; completes with the value it pops with.
  Future<T?> push<T extends Object?>(BuildContext context, PluxRouteSpec route);

  /// Replaces the current route with [route].
  Future<T?> replace<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  );

  /// Clears the stack, then pushes [route].
  Future<T?> clearAndPush<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  );

  /// Pops routes until the route named [name] is on top, or only the first
  /// route is left.
  void popUntil(BuildContext context, String name);

  /// Pops the route of [context] with [result]; false when there is
  /// nothing to pop.
  bool pop(BuildContext context, [Object? result]);
}

/// The default delegate: the nearest `Navigator`, with the plain API, so
/// it needs no router and works in any app, `MaterialApp.router` apps
/// included (NAV-006).
final class PluxNavigatorDelegate implements PluxNavigationDelegate {
  /// Creates the delegate.
  const PluxNavigatorDelegate();

  @override
  Future<T?> push<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) => Navigator.of(context).push<T>(route.toRoute<T>(context));

  @override
  Future<T?> replace<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) =>
      Navigator.of(context)
          .pushReplacement<T, Object?>(route.toRoute<T>(context));

  @override
  Future<T?> clearAndPush<T extends Object?>(
    BuildContext context,
    PluxRouteSpec route,
  ) =>
      Navigator.of(context)
          .pushAndRemoveUntil<T>(route.toRoute<T>(context), (_) => false);

  @override
  void popUntil(BuildContext context, String name) =>
      Navigator.of(context)
          .popUntil((r) => r.isFirst || r.settings.name == name);

  @override
  bool pop(BuildContext context, [Object? result]) {
    final nav = Navigator.of(context);
    if (!nav.canPop()) return false;
    nav.pop(result);
    return true;
  }
}
