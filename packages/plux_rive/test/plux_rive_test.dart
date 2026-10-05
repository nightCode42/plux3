// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_rive/plux_rive.dart';

final class _Machine implements StateMachineInputs {
  final Map<String, double> numbers = {};
  final Map<String, bool> booleans = {};
  final List<String> fired = [];
  final Set<String> known = {'level', 'on', 'go'};

  @override
  bool setNumber(String name, double value) {
    if (!known.contains(name)) return false;
    numbers[name] = value;
    return true;
  }

  @override
  bool setBoolean(String name, bool value) {
    if (!known.contains(name)) return false;
    booleans[name] = value;
    return true;
  }

  @override
  bool fire(String name) {
    if (!known.contains(name)) return false;
    fired.add(name);
    return true;
  }
}

final class _Slot implements PluxSlot {
  _Slot(this.props);

  final Map<String, Object?> props;

  @override
  Object? operator [](String name) => props[name];

  @override
  void emit(String name, [Object? payload]) {}
}

/// Rive (ANI-005, WGT-021): the slot, and state machine inputs that follow
/// the values the page binds.
void main() {
  test('the first apply sets every input and fires no trigger [ANI-005]', () {
    final m = _Machine();
    PluxRiveInputs(m).apply({'level': 3, 'on': true}, {'go': 0});
    expect(m.numbers, {'level': 3.0});
    expect(m.booleans, {'on': true});
    expect(m.fired, isEmpty);
  });

  test('a later apply sets only what changed [ANI-005]', () {
    final m = _Machine();
    final binding = PluxRiveInputs(m)..apply({'level': 1, 'on': false}, {});
    m.numbers.clear();
    m.booleans.clear();
    binding.apply({'level': 1, 'on': true}, {});
    expect(m.numbers, isEmpty);
    expect(m.booleans, {'on': true});
    binding.apply({'level': 2.5, 'on': true}, {});
    expect(m.numbers, {'level': 2.5});
  });

  test('a trigger fires when its bound value changes [ANI-005]', () {
    final m = _Machine();
    final binding = PluxRiveInputs(m)..apply({}, {'go': 0});
    binding.apply({}, {'go': 0});
    expect(m.fired, isEmpty);
    binding.apply({}, {'go': 1});
    expect(m.fired, ['go']);
    binding.apply({}, {'go': 1});
    expect(m.fired, ['go']);
    binding.apply({}, {'go': 2});
    expect(m.fired, ['go', 'go']);
  });

  test(
    'a name the machine does not know is reported, not thrown [ANI-005]',
    () {
      final m = _Machine();
      final binding = PluxRiveInputs(m)
        ..apply({'nope': 1, 'level': 'text'}, {});
      expect(binding.unknown, {'nope', 'level'});
    },
  );

  testWidgets('the slot is registered under its catalogue name [ANI-005]', (
    tester,
  ) async {
    expect(PluxRive.slots.keys, [PluxRive.slotName]);
    late Widget built;
    await tester.pumpWidget(
      Builder(
        builder: (context) {
          built = PluxRive.slots[PluxRive.slotName]!.builder(
            context,
            _Slot({'url': 'http://example.com/a.riv'}),
          );
          return built;
        },
      ),
    );
    expect(built, isA<SizedBox>(), reason: 'only https addresses play');
  });

  testWidgets('the slot builds a view for an https file [WGT-021]', (
    tester,
  ) async {
    late Widget built;
    await tester.pumpWidget(
      Builder(
        builder: (context) {
          built = PluxRive.slots[PluxRive.slotName]!.builder(
            context,
            _Slot({
              'url': 'https://example.com/a.riv',
              'stateMachine': 'main',
              'inputs': {'level': 2},
            }),
          );
          return const SizedBox.shrink();
        },
      ),
    );
    expect(built, isA<PluxRiveView>());
    final view = built as PluxRiveView;
    expect(view.stateMachine, 'main');
    expect(view.inputs, {'level': 2});
    expect(view.cacheKey, 'https://example.com/a.riv');
  });
}
