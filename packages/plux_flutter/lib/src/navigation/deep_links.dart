// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Deep links and push payloads (NAV-008, ADR-0040): a link or a
/// notification's payload names a plugin page through the app document's
/// mapping, and opens it through the same guarded path as every other
/// entry.
library;

import 'dart:convert';

import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;

/// A link or payload resolved to a route, with its parameters as text, or
/// as values for a payload's non-text ones.
typedef LinkTarget = ({String route, Map<String, Object?> params});

/// The prefix every app answers with a route name:
/// `https://<host>/p/<route-name>?…`.
const String routeLinkPrefix = 'p';

/// Resolves [link] through the app's [links] (NAV-008), or null when
/// nothing maps it.
///
/// - An `https` link must name one of the app's hosts; a custom scheme
///   must be one of its schemes, and its authority is the path's first
///   segment, so `acme://items/42` and `acme:///items/42` both name
///   `/items/42`. Any other scheme maps nothing.
/// - `/p/<route-name>` names the route itself.
/// - Otherwise the first pattern whose segments match: literal segments
///   equal, `{name}` segments capture the parameter, percent-decoded.
/// - Query parameters fill the route's other parameters by name; a path
///   parameter wins over a query parameter of the same name.
///
/// A link is untrusted input: one whose percent-encoding does not decode
/// maps nothing, and nothing here throws.
LinkTarget? resolveDeepLink(fbs.DeepLinks? links, Uri link) {
  try {
    return _resolve(links, link);
  } on FormatException {
    return null;
  }
}

LinkTarget? _resolve(fbs.DeepLinks? links, Uri link) {
  if (links == null) return null;
  final scheme = link.scheme.toLowerCase();
  final List<String> segments;
  if (scheme == 'https') {
    final hosts = links.hosts ?? const <String>[];
    if (!hosts.contains(link.host.toLowerCase())) return null;
    segments = link.pathSegments;
  } else if ((links.schemes ?? const <String>[]).contains(scheme)) {
    segments = [if (link.host.isNotEmpty) link.host, ...link.pathSegments];
  } else {
    return null;
  }
  final path = [
    for (final (i, s) in segments.indexed)
      if (s.isNotEmpty || i < segments.length - 1) s,
  ];
  final query = link.queryParameters;
  if (path.length == 2 && path.first == routeLinkPrefix) {
    return (route: path[1], params: Map<String, Object?>.of(query));
  }
  for (final r in links.routes ?? const <fbs.DeepLinkRoute>[]) {
    final pattern = (r.path ?? '').split('/').where((s) => s.isNotEmpty);
    final captured = _match(pattern.toList(), path);
    if (captured == null || r.route == null) continue;
    return (route: r.route!, params: {...query, ...captured});
  }
  return null;
}

Map<String, String>? _match(List<String> pattern, List<String> path) {
  if (pattern.length != path.length) return null;
  final out = <String, String>{};
  for (var i = 0; i < pattern.length; i++) {
    final p = pattern[i];
    if (p.startsWith('{') && p.endsWith('}')) {
      out[p.substring(1, p.length - 1)] = path[i];
    } else if (p != path[i]) {
      return null;
    }
  }
  return out;
}

/// The default payload key holding `{route, params}`.
const String defaultPushKey = 'plux';

/// Resolves a notification's [payload] through the app's [push] (NAV-008),
/// or null when the app declares no push or the payload names no route.
///
/// The target is under the payload key — the key itself, or a dotted path
/// to it — as an object `{"route": …, "params": {…}}` or as that object's
/// JSON text, the form FCM data messages carry.
LinkTarget? resolvePushPayload(fbs.Push? push, Map<String, Object?> payload) {
  if (push == null || !push.enabled) return null;
  final key = push.payloadKey ?? defaultPushKey;
  var value = payload[key];
  if (value == null && key.contains('.')) {
    Object? at = payload;
    for (final part in key.split('.')) {
      at = at is Map<String, Object?> ? at[part] : null;
    }
    value = at;
  }
  if (value is String) {
    try {
      value = jsonDecode(value);
    } on FormatException {
      return null;
    }
  }
  if (value is! Map<String, Object?>) return null;
  final route = value['route'];
  final params = value['params'] ?? const <String, Object?>{};
  if (route is! String || route.isEmpty || params is! Map<String, Object?>) {
    return null;
  }
  return (route: route, params: params);
}
