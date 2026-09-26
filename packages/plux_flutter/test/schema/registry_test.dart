// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

/// The permanent-ID lock, read from the repository.
Map<String, Object?> _lock() {
  final file = File('../../schema/widgets/ids.lock.json');
  return (jsonDecode(file.readAsStringSync()) as Map<String, Object?>)['ids']!
      as Map<String, Object?>;
}

void main() {
  test('every generated ID matches the permanent-ID lock [BND-011]', () {
    final lock = _lock();
    void expectId(String key, int id) => expect(lock[key], id, reason: key);
    void expectMembers(String prefix, String kind, Map<String, int> ids) =>
        ids.forEach((name, id) => expectId('$prefix/$kind/$name', id));

    for (final w in widgetDescriptors) {
      final key = 'widget/${w.type}';
      expectId(key, w.id);
      expectMembers(key, 'prop', w.props);
      expectMembers(key, 'event', w.events);
      expectMembers(key, 'slot', w.slots);
    }
    for (final t in valueTypeDescriptors) {
      expectId('type/${t.name}', t.id);
      expectMembers('type/${t.name}', 'field', t.fields);
    }
    for (final e in enumDescriptors) {
      expectId('enum/${e.name}', e.id);
      expectMembers('enum/${e.name}', 'value', e.values);
    }
    for (final a in actionDescriptors) {
      expectId('action/${a.name}', a.id);
      expectMembers('action/${a.name}', 'input', a.inputs);
    }
  });

  test(
    'IDs are unique within their kind and the tables are sorted [BND-011]',
    () {
      void expectUnique(String what, Iterable<int> ids) =>
          expect(ids.toSet().length, ids.length, reason: what);
      void expectSorted(String what, List<String> names) =>
          expect(names, [...names]..sort(), reason: what);

      expectUnique('widgets', widgetDescriptors.map((w) => w.id));
      expectUnique('value types', valueTypeDescriptors.map((t) => t.id));
      expectUnique('enums', enumDescriptors.map((e) => e.id));
      expectUnique('actions', actionDescriptors.map((a) => a.id));
      expectSorted('widgets', [for (final w in widgetDescriptors) w.type]);
      expectSorted('actions', [for (final a in actionDescriptors) a.name]);
      for (final w in widgetDescriptors) {
        expectUnique('${w.type} props', w.props.values);
        expect(w.layer, anyOf(1, 2));
        expect(w.revision, greaterThanOrEqualTo(1));
      }
    },
  );

  test('the registry covers the widgets documents use [WGT-002]', () {
    final text = widgetDescriptors.singleWhere((w) => w.type == 'Text');
    expect(text.props, containsPair('data', isPositive));
    final match = widgetDescriptors.singleWhere((w) => w.type == 'Match');
    expect(match.slots.keys, containsAll(['branches', 'otherwise']));
    final setState = actionDescriptors.singleWhere((a) => a.name == 'setState');
    expect(setState.inputs.keys, containsAll(['path', 'value']));
  });
}
