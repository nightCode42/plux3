// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The handlers of the local database actions (DB-006, DB-009): `dbInsert`,
/// `dbUpdate`, `dbUpsert`, `dbDelete`, `dbQuery`, `kvGet`, `kvSet` and
/// `kvRemove`, run on the [PluxDatabase] service as the plugin whose graph
/// runs (DB-004). A failure is an [ActionError] a step's `onError` reads.
library;

import 'package:plux_flutter/src/actions/action_error.dart';
import 'package:plux_flutter/src/actions/handlers.dart';
import 'package:plux_flutter/src/db/service.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';

PluxDatabase _db(StepContext c, String action) =>
    c.service<PluxDatabase>() ??
    (throw ActionError(
      ActionErrorKind.custom,
      PluxErrorCode.dbUnavailable,
      '$action: this runtime has no local database',
    ));

String _text(Map<String, Object?> i, String name, String action) {
  final v = i[name];
  if (v is String) return v;
  throw ActionError.validation('$action: $name is not a string');
}

Map<String, Object?> _object(
  Map<String, Object?> i,
  String name,
  String action,
) {
  final v = i[name];
  if (v is Map<String, Object?>) return v;
  throw ActionError.validation('$action: $name is not an object');
}

/// Runs [body], turning the database's failures into step errors.
Future<T> _guard<T>(Future<T> Function() body) async {
  try {
    return await body();
  } on PluxException catch (e) {
    throw _asActionError(e);
  }
}

ActionError _asActionError(PluxException e) {
  final kind = switch (e.code) {
    PluxErrorCode.dbRecordInvalid ||
    PluxErrorCode.dbRecordNotFound ||
    PluxErrorCode.dbKeyConflict ||
    PluxErrorCode.dbQueryInvalid ||
    PluxErrorCode.dbValueTypeMismatch ||
    PluxErrorCode.dbLimitExceeded => ActionErrorKind.validation,
    PluxErrorCode.dbCollectionUnknown => ActionErrorKind.permission,
    _ => ActionErrorKind.custom,
  };
  return ActionError(kind, e.code, e.message);
}

/// The database actions' handlers, by action name; `builtInHandlers`
/// includes them.
final Map<String, ActionHandler> dbHandlers = {
  'dbInsert': FunctionHandler((c, i) async {
    final collection = _text(i, 'collection', 'dbInsert');
    final record = _object(i, 'record', 'dbInsert');
    final key = await _guard(
      () => _db(c, 'dbInsert').insert(c.pluginKey, collection, record),
    );
    return StepDone(key);
  }),
  'dbUpdate': FunctionHandler((c, i) async {
    final collection = _text(i, 'collection', 'dbUpdate');
    final key = _text(i, 'key', 'dbUpdate');
    final patch = _object(i, 'patch', 'dbUpdate');
    await _guard(
      () => _db(c, 'dbUpdate').update(c.pluginKey, collection, key, patch),
    );
    return const StepDone();
  }),
  'dbUpsert': FunctionHandler((c, i) async {
    final collection = _text(i, 'collection', 'dbUpsert');
    final key = _text(i, 'key', 'dbUpsert');
    final record = _object(i, 'record', 'dbUpsert');
    await _guard(
      () => _db(c, 'dbUpsert').upsert(c.pluginKey, collection, key, record),
    );
    return const StepDone();
  }),
  'dbDelete': FunctionHandler((c, i) async {
    final collection = _text(i, 'collection', 'dbDelete');
    final key = _text(i, 'key', 'dbDelete');
    await _guard(() => _db(c, 'dbDelete').delete(c.pluginKey, collection, key));
    return const StepDone();
  }),
  'dbQuery': FunctionHandler((c, i) async {
    final collection = _text(i, 'collection', 'dbQuery');
    final where = i['where'];
    final order = i['orderBy'];
    final limit = i['limit'] ?? 100;
    final offset = i['offset'] ?? 0;
    if (limit is! int || offset is! int) {
      throw const ActionError.validation('dbQuery: limit and offset are ints');
    }
    final rows = await _guard(
      () => _db(c, 'dbQuery').query(
        c.pluginKey,
        collection,
        where: where is bool Function(Object?) ? where : null,
        orderBy: order is String ? order : null,
        descending: i['descending'] == true,
        limit: limit,
        offset: offset,
      ),
    );
    return StepDone(rows);
  }),
  'kvGet': FunctionHandler((c, i) async {
    final key = _text(i, 'key', 'kvGet');
    return StepDone(
      await _guard(() => _db(c, 'kvGet').kvGet(c.pluginKey, key)),
    );
  }),
  'kvSet': FunctionHandler((c, i) async {
    final key = _text(i, 'key', 'kvSet');
    await _guard(() => _db(c, 'kvSet').kvSet(c.pluginKey, key, i['value']));
    return const StepDone();
  }),
  'kvRemove': FunctionHandler((c, i) async {
    final key = _text(i, 'key', 'kvRemove');
    await _guard(() => _db(c, 'kvRemove').kvRemove(c.pluginKey, key));
    return const StepDone();
  }),
};
