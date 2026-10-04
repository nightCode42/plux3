// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The boundary with host code (ADR-0041): values a plugin passes to a
/// native route, slot or custom action are checked against the native
/// catalogue's declaration before host code sees them, and values host
/// code returns are checked before a plugin does. Plugins are remote
/// content, and a host build may differ from the catalogue a release was
/// compiled against.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/native_catalogue/registration.dart';
import 'package:plux_flutter/src/pxl/types.dart';
import 'package:plux_flutter/src/render/renderer.dart' show fromHost;
import 'package:plux_flutter/src/render/sections.dart';

/// The declarations and named types of the active release's native
/// catalogue.
typedef NativeDeclarations = ({
  Map<String, NativeRouteDecl> routes,
  Map<String, NativeSlotDecl> slots,
  Map<String, NativeActionDecl> actions,
  Map<String, NamedType> types,
});

/// [values], in PXL or JSON form, in the JSON form host code receives,
/// checked against [params]: a required value that is missing, a name the
/// catalogue does not declare, or a value of another type fails with a
/// `validation` [ActionError] naming [what].
Map<String, Object?> toHostValues(
  List<NativeParam> params,
  Map<String, Object?> values,
  Map<String, NamedType> types,
  String what,
) {
  final out = <String, Object?>{};
  for (final p in params) {
    final v = values[p.name];
    if (v == null) {
      if (p.required) {
        throw ActionError.validation('$what: ${p.name} is missing');
      }
      out[p.name] = null;
      continue;
    }
    out[p.name] = _checked(p.type, toJson(v), types, '$what: ${p.name}');
  }
  for (final name in values.keys) {
    if (!params.any((p) => p.name == name)) {
      throw ActionError.validation('$what: the catalogue declares no $name');
    }
  }
  return out;
}

/// [value] host code returned, in the PXL form of [type]: Dart values are
/// read as `fromHost` reads route parameters. Null when [type] is null (the
/// catalogue declares no value); a value of another type fails with a
/// `validation` [ActionError] naming [what].
Object? fromHostValue(
  String? type,
  Object? value,
  Map<String, NamedType> types,
  String what,
) {
  if (type == null) return null;
  try {
    return fromJson(PxlType.parse(type, (n) => types[n]), fromHost(value));
  } on FormatException catch (e) {
    throw ActionError.validation('$what is not a $type: ${e.message}');
  }
}

Object? _checked(
  String type,
  Object? json,
  Map<String, NamedType> types,
  String what,
) {
  try {
    fromJson(PxlType.parse(type, (n) => types[n]), json);
  } on FormatException catch (e) {
    throw ActionError.validation('$what is not a $type: ${e.message}');
  }
  return json;
}

/// The host's custom actions (ACT-060): each call is checked against the
/// catalogue on the way in and on the way out.
final class HostActions implements NativeActions {
  /// Creates the lookup over [registered], checked against [declarations].
  HostActions({required this.registered, required this.declarations});

  /// The actions the host registered.
  final Map<String, PluxNativeAction<Object?, Object?>> registered;

  /// The active release's declarations, or null before the first release.
  final NativeDeclarations? Function() declarations;

  @override
  Future<Object?> call(String name, Map<String, Object?> input) async {
    final action = registered[name];
    if (action == null) {
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.nativeActionNotRegistered,
        'the host registers no custom action "$name"',
      );
    }
    final d = declarations();
    final decl = d?.actions[name];
    if (d == null || decl == null) {
      throw ActionError.validation(
        'the native catalogue declares no custom action "$name"',
      );
    }
    final json = toHostValues(
      decl.inputs,
      input,
      d.types,
      'custom action $name',
    );
    final Object? out;
    try {
      out = await action.callWith(json);
    } on ActionError {
      rethrow;
    } on Object catch (e) {
      // Only the exception's type: its message may hold the user's data.
      throw ActionError(
        ActionErrorKind.custom,
        PluxErrorCode.hostCodeFailed,
        'custom action $name failed with ${e.runtimeType}',
      );
    }
    return fromHostValue(
      decl.output,
      out,
      d.types,
      'the output of custom action $name',
    );
  }
}
