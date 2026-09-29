// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/pxl/vm.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/renderer.dart';
import 'package:plux_flutter/src/render/sections.dart';
import 'package:plux_flutter/src/render/values.dart';

import '../support/harness.dart';

fbs.Value _v(fbs.ValueObjectBuilder b) => fbs.Value(b.toBytes());

void main() {
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  testWidgets(
    'literals, objects and bindings resolve to PXL values [BND-015] [RT-010]',
    (tester) async {
      await tester.runAsync(
        () => h.startFrom(Harness.goldens.bundles['widgets/widgets.pxb']!, {
          'gallery': Harness.goldens.bundles['widgets/gallery.pxb']!,
        }),
      );
      final release = h.runtime.active.value!;
      final plugin = (h.runtime.renderer! as PluxRenderer).view(
        release,
        'gallery',
      );
      const strings = ['', 'hello', 'EUR', 'name', 'Enum'];
      String table(int i) => strings[i];
      final r = ValueResolver(
        plugin: plugin,
        roots: () => {'page': <String, Object?>{}},
        token: (path) => path == 'known' ? 1.0 : null,
        translation: (key) => key == (1, 2) ? 'Hi {name}' : null,
        limits: PxlLimits.defaults(),
      );
      Object? resolve(fbs.ValueObjectBuilder b) => r.resolve(_v(b), table);

      expect(r.resolve(null, table), isNull);
      expect(resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Null)), isNull);
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Bool, i: 1)),
        isTrue,
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Int, i: -3)),
        -3,
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Double, d: 1.5)),
        1.5,
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.String, s: 1)),
        'hello',
      );
      // -12.5: unscaled -125 as two's complement is 0xFF83.
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Decimal,
            unscaled: [0xff, 0x83],
            scale: 1,
          ),
        ),
        Decimal.tryParse('-12.5'),
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Money,
            unscaled: [0x01, 0x2c],
            scale: 2,
            s: 2,
          ),
        ),
        Money(Decimal.tryParse('3.00')!, 'EUR'),
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Date, i: 3)),
        const PxlDate(3),
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.DateTime,
            i: 2000,
            offset: 60,
          ),
        ),
        const PxlDateTime(2, 60),
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Duration, i: 5000)),
        const PxlDuration(5),
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(kind: fbs.ValueKind.Color, i: 0xFF112233),
        ),
        const PxlColor(0x112233FF),
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Enum, i: 4)),
        4,
        reason: 'registry enums by ID',
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Enum, i: 0, s: 4)),
        'Enum',
        reason: 'declared enums by name',
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Asset,
            uuid: fbs.UuidObjectBuilder(hi: 0x0123456789abcdef, lo: -1),
          ),
        ),
        '01234567-89ab-cdef-ffff-ffffffffffff',
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Route, s: 1)),
        'hello',
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.List,
            items: [fbs.ValueObjectBuilder(kind: fbs.ValueKind.Int, i: 1)],
          ),
        ),
        [1],
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Map,
            entries: [
              fbs.EntryObjectBuilder(
                key: 3,
                value: fbs.ValueObjectBuilder(kind: fbs.ValueKind.Int, i: 2),
              ),
            ],
          ),
        ),
        {'name': 2},
      );
      final object =
          resolve(
                fbs.ValueObjectBuilder(
                  kind: fbs.ValueKind.Object,
                  entries: [
                    fbs.EntryObjectBuilder(
                      key: 3,
                      value: fbs.ValueObjectBuilder(
                        kind: fbs.ValueKind.Int,
                        i: 9,
                      ),
                    ),
                  ],
                ),
              )!
              as PluxObject;
      expect(object.field(3), 9);
      expect(object.field(4), isNull);
      expect(object.toMap(), {'name': 9});
      expect(
        toPxl([
          object,
          {'k': object},
        ]),
        [
          {'name': 9},
          {
            'k': {'name': 9},
          },
        ],
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Token, s: 1)),
        isNull,
      );
      expect(
        resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Translation,
            uuid: fbs.UuidObjectBuilder(hi: 1, lo: 2),
            entries: [
              fbs.EntryObjectBuilder(
                key: 3,
                value: fbs.ValueObjectBuilder(kind: fbs.ValueKind.String, s: 1),
              ),
            ],
          ),
        ),
        'Hi hello',
      );
      expect(
        resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Translation)),
        '',
      );
      expect(
        () => resolve(
          fbs.ValueObjectBuilder(
            kind: fbs.ValueKind.Translation,
            uuid: fbs.UuidObjectBuilder(hi: 9, lo: 9),
          ),
        ),
        throwsA(isA<BindingError>()),
      );
      expect(
        () => resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Style, i: 42)),
        throwsA(isA<PluxException>()),
        reason: 'a style the section does not have',
      );
      expect(
        () => resolve(fbs.ValueObjectBuilder(kind: fbs.ValueKind.Expr, i: 42)),
        throwsA(isA<PluxException>()),
        reason: 'a program the section does not have',
      );
      expect(const BindingError('m').toString(), 'm');
      expect(uuidString((0, 1)), '00000000-0000-0000-0000-000000000001');
    },
  );

  testWidgets('bundle views read the shared sections [RT-010]', (tester) async {
    await tester.runAsync(
      () => h.startFrom(Harness.goldens.bundles['widgets/widgets.pxb']!, {
        'gallery': Harness.goldens.bundles['widgets/gallery.pxb']!,
      }),
    );
    final release = h.runtime.active.value!;
    final renderer = h.runtime.renderer! as PluxRenderer;
    final app = renderer.view(release, '');
    expect(identical(app, renderer.view(release, '')), isTrue);
    expect(app.token('color.primary'), isNotNull);
    expect(app.token('brands.acme.color.primary'), isNotNull);
    expect(app.token('nope'), isNull);
    expect(app.locales, ['en']);
    expect(app.message('en', (0, 0)), isNull);
    expect(app.message('fr', (0, 0)), isNull);
    expect(() => app.string(1 << 20), throwsA(isA<PluxException>()));
    expect(app.section(3, (0, 0)), isNull);
    final gallery = renderer.view(release, 'gallery');
    expect(
      gallery.token('color.primary'),
      isNull,
      reason: 'tokens live in the app bundle',
    );
    expect(gallery.types, isEmpty);
    renderer.memoryPressure();
    expect(renderer.cache.length, 0);
    expect(fromHost(DateTime.utc(2026)), '2026-01-01T00:00:00.000Z');
    expect(fromHost(const Duration(seconds: 1)), 1000);
    expect(
      fromHost([
        1,
        {'a': 2},
      ]),
      [
        1,
        {'a': 2},
      ],
    );
    expect(compareUnsigned(-1, 0), 1);
  });
}
