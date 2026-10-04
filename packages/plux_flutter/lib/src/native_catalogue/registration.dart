// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// What the host app registers for plugins to use, in one place,
/// `PluxConfig` (HST-031, ADR-0041): native routes, native slots and
/// custom actions, and a router adapter that discovers the host's named
/// routes. Every value crossing into host code is checked against the
/// native catalogue's declaration first, and every value coming back is
/// checked before a plugin sees it.
library;

import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/navigation/delegate.dart';

/// A route of the host app that plugins open by name (NAV-002).
///
/// `params` builds the route's typed parameters from their JSON form,
/// already checked against the catalogue; `builder` builds the screen,
/// which returns its result with `Navigator.pop`. `result` converts that
/// result to its JSON form; without it the result must already be one (a
/// string, number, boolean, or a list or map of them). The result is
/// checked against the catalogue's result type before the plugin sees it.
///
/// ```dart
/// 'profile': PluxNativeRoute<ProfileParams, bool>(
///   params: ProfileParams.fromJson,
///   builder: (context, p) => ProfileScreen(userId: p.userId),
/// ),
/// ```
final class PluxNativeRoute<P, R> {
  /// A route whose screen Plux pushes through the navigation delegate.
  PluxNativeRoute({
    required P Function(Map<String, Object?> json) params,
    required Widget Function(BuildContext context, P params) builder,
    Object? Function(R value)? result,
  }) : _open = ((context, delegate, name, json, presentation) async {
         final typed = params(json);
         final value = await delegate.push<Object?>(
           context,
           PluxRouteSpec(
             name: name,
             arguments: json,
             presentation: presentation,
             builder: (c) => builder(c, typed),
           ),
         );
         return value == null || result == null ? value : result(value as R);
       });

  /// A route the host's router opens: [open] navigates there and completes
  /// with the screen's result. Router adapters build these for the routes
  /// they discover (HST-031).
  PluxNativeRoute.opened({
    required Future<R?> Function(
      BuildContext context,
      Map<String, Object?> params,
    )
    open,
    Object? Function(R value)? result,
  }) : _open = ((context, delegate, name, json, presentation) async {
         final value = await open(context, json);
         return value == null || result == null ? value : result(value);
       });

  final Future<Object?> Function(
    BuildContext context,
    PluxNavigationDelegate delegate,
    String name,
    Map<String, Object?> params,
    PluxPresentation presentation,
  )
  _open;

  /// Opens the route [name] with [params], in their JSON form, on the
  /// navigator of [context] through [delegate], presented as
  /// [presentation] unless the host's router opens it; completes with the
  /// screen's result in its JSON form, or null.
  Future<Object?> openWith(
    BuildContext context,
    PluxNavigationDelegate delegate,
    String name,
    Map<String, Object?> params, {
    PluxPresentation presentation = PluxPresentation.page,
  }) => _open(context, delegate, name, params, presentation);
}

/// A widget of the host app placed inside plugin pages (WGT-033). [builder]
/// builds it from the slot's props and events; the host's widget is
/// referenced, never changed.
///
/// ```dart
/// 'MapCard': PluxNativeSlot(
///   (context, slot) => MapCard(
///     zoom: slot['zoom']! as double,
///     onPan: (offset) => slot.emit('onPan', offset),
///   ),
/// ),
/// ```
final class PluxNativeSlot {
  /// Creates a slot.
  const PluxNativeSlot(this.builder);

  /// Builds the widget.
  final Widget Function(BuildContext context, PluxSlot slot) builder;
}

/// What a native slot is built with.
abstract interface class PluxSlot {
  /// The prop [name] in the JSON form of its declared type: strings,
  /// numbers, booleans, lists and maps; decimals and dates as strings.
  /// Null when the page does not set it.
  Object? operator [](String name);

  /// Emits the event [name] with [payload], in the JSON form of its
  /// declared payload type; the page's handler of the event runs with it
  /// as `event`. A payload of the wrong type is reported and dropped.
  void emit(String name, [Object? payload]);
}

/// A function of the host app that plugins call with `callNative`
/// (ACT-060).
///
/// `input` builds the typed input from the action's inputs in their JSON
/// form, already checked against the catalogue; `handler` runs; `output`
/// converts its result to JSON form, which is checked against the
/// catalogue's output type before the plugin sees it. Without [output]
/// the result must already be in JSON form. An exception the handler
/// throws fails the step; it never reaches the plugin as a crash.
///
/// ```dart
/// 'openScanner': PluxNativeAction<ScanIn, String>(
///   input: ScanIn.fromJson,
///   handler: (scan) => scanner.scan(prompt: scan.prompt),
/// ),
/// ```
final class PluxNativeAction<I, O> {
  /// Creates an action.
  PluxNativeAction({
    required I Function(Map<String, Object?> json) input,
    required FutureOr<O> Function(I input) handler,
    Object? Function(O value)? output,
  }) : _call = ((json) async {
         final value = await handler(input(json));
         return output == null ? value : output(value);
       });

  final Future<Object?> Function(Map<String, Object?> json) _call;

  /// Runs the action with [inputs] in their JSON form; completes with its
  /// result in JSON form.
  Future<Object?> callWith(Map<String, Object?> inputs) => _call(inputs);
}

/// A router adapter (ADR-0040, P4 plan A22): `plux_go_router` and
/// `plux_auto_route` wrap the host's router in one, passed as
/// `PluxConfig.router`. Plux then navigates through the router, and
/// plugins open the router's named routes as native routes, with no other
/// registration (HST-031).
abstract interface class PluxRouterAdapter {
  /// The navigation delegate on the router.
  PluxNavigationDelegate get delegate;

  /// The host's named routes the adapter discovered; `nativeRoutes`
  /// entries of the same name win.
  Map<String, PluxNativeRoute<Object?, Object?>> get routes;

  /// The router's root navigator key, where deep links and push payloads
  /// open; `PluxConfig.navigatorKey` wins.
  GlobalKey<NavigatorState>? get navigatorKey;
}
