// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_flutter/src/bundle/container.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/scope.dart';
import 'package:plux_flutter/src/schema/registry.g.dart';

import '../support/harness.dart';

WidgetDescriptor _widget(String type) =>
    widgetDescriptors.firstWhere((d) => d.type == type);

/// The gallery plugin bundle with its `layout` page replaced by a page of
/// [nodes] and [strings] (index 0 must be empty, 1 the route `layout`):
/// runtime cases the compiler never writes, such as a widget from a newer
/// registry or a value of the wrong kind.
Uint8List _galleryWith(
  List<fbs.NodeObjectBuilder> nodes,
  List<String> strings,
) {
  final gallery = Harness.goldens.bundles['widgets/gallery.pxb']!;
  final c = BundleContainer.parse(gallery);
  final sections = <Section>[];
  for (final s in c.sections) {
    if (s.kind == SectionKind.page) {
      final page = fbs.Page(s.data);
      if (page.strings![page.route] == 'layout') {
        final data = fbs.PageObjectBuilder(
          id: fbs.UuidObjectBuilder(hi: page.id!.hi, lo: page.id!.lo),
          key: 1,
          route: 1,
          nodes: nodes,
          strings: strings,
        ).toBytes('PXPG');
        sections.add(Section(s.kind, s.id, s.hash, data));
        continue;
      }
    }
    sections.add(s);
  }
  return encodeBundle(BundleKinds.plugin, sections);
}

fbs.ValueObjectBuilder _str(int s) =>
    fbs.ValueObjectBuilder(kind: fbs.ValueKind.String, s: s);

void main() {
  late Harness h;
  setUp(() async => h = await Harness.create());
  tearDown(() => h.close());

  final column = _widget('Column');
  final text = _widget('Text');

  Future<void> show(WidgetTester tester, Uint8List gallery) async {
    await tester.runAsync(
      () => h.startFrom(Harness.goldens.bundles['widgets/widgets.pxb']!, {
        'gallery': gallery,
      }),
    );
    await tester.pumpWidget(
      const MaterialApp(home: PluxScope(child: PluxView('layout'))),
    );
    await settle(tester);
  }

  List<PluxErrorCode> codes() => [
    for (final e in h.errors)
      if (e.code != PluxErrorCode.syncFailed) e.code,
  ];

  testWidgets(
    'unknown widgets, native slots and bad values are contained; testId and semantics apply [WGT-014] [WGT-013] [RT-020]',
    (tester) async {
      await show(
        tester,
        _galleryWith(
          [
            fbs.NodeObjectBuilder(widget: column.id, children: [1, 2, 3, 4]),
            fbs.NodeObjectBuilder(widget: 9999),
            fbs.NodeObjectBuilder(widget: 0, nativeSlot: 3),
            fbs.NodeObjectBuilder(
              widget: text.id,
              props: [
                fbs.PropObjectBuilder(id: text.props['data'], value: _str(2)),
              ],
              testId: 4,
              hints: 1,
            ),
            fbs.NodeObjectBuilder(
              widget: text.id,
              props: [
                fbs.PropObjectBuilder(id: text.props['data'], value: _str(2)),
                // maxLines is an int; a string cannot be decoded.
                fbs.PropObjectBuilder(
                  id: text.props['maxLines'],
                  value: _str(2),
                ),
              ],
            ),
          ],
          ['', 'layout', 'survivor', 'MapView', 'survivor-id'],
        ),
      );
      expect(find.text('survivor'), findsNWidgets(2));
      expect(find.textContaining('widget 9999'), findsOneWidget);
      // A native slot the host does not register (ADR-0041).
      expect(find.textContaining('MapView'), findsOneWidget);
      expect(
        codes(),
        containsAll([
          PluxErrorCode.unknownWidget,
          PluxErrorCode.nativeSlotNotRegistered,
          PluxErrorCode.propValueInvalid,
        ]),
      );
      expect(codes(), isNot(contains(PluxErrorCode.nodeBuildFailed)));
      expect(
        tester
            .widget<Semantics>(
              find
                  .ancestor(
                    of: find.text('survivor').first,
                    matching: find.byType(Semantics),
                  )
                  .first,
            )
            .properties
            .identifier,
        'survivor-id',
      );
      expect(
        find.ancestor(
          of: find.text('survivor').first,
          matching: find.byType(RepaintBoundary),
        ),
        findsWidgets,
      );
    },
  );

  testWidgets(
    'a required value that cannot be produced shows the page fallback [RT-020] [SYN-006]',
    (tester) async {
      await show(
        tester,
        _galleryWith(
          [
            fbs.NodeObjectBuilder(widget: column.id, children: [1]),
            fbs.NodeObjectBuilder(
              widget: text.id,
              props: [
                // data is required; an int is not a string.
                fbs.PropObjectBuilder(
                  id: text.props['data'],
                  value: fbs.ValueObjectBuilder(kind: fbs.ValueKind.Int, i: 3),
                ),
              ],
            ),
          ],
          ['', 'layout'],
        ),
      );
      expect(find.text('fallback PLX-4001'), findsOneWidget);
      expect(codes(), contains(PluxErrorCode.nodeBuildFailed));
      expect(
        h.errors.map((e) => e.message),
        contains(contains('gallery/layout/1')),
        reason: 'the report names the node path',
      );
    },
  );

  testWidgets(
    'sections above 64 KiB are checked off the UI isolate before the page builds [SEC-052] [BND-006]',
    (tester) async {
      final big = 'x' * (70 * 1024);
      await show(
        tester,
        _galleryWith(
          [
            fbs.NodeObjectBuilder(
              widget: text.id,
              props: [
                fbs.PropObjectBuilder(id: text.props['data'], value: _str(2)),
              ],
            ),
          ],
          ['', 'layout', 'large page', big],
        ),
      );
      expect(find.text('large page'), findsOneWidget);
    },
  );

  testWidgets('a list builds only the items on screen [RT-011] [WGT-012]', (
    tester,
  ) async {
    final list = _widget('ListView');
    await show(
      tester,
      _galleryWith(
        [
          fbs.NodeObjectBuilder(
            widget: list.id,
            props: [
              fbs.PropObjectBuilder(
                id: list.props['items'],
                value: fbs.ValueObjectBuilder(
                  kind: fbs.ValueKind.List,
                  items: [
                    for (var i = 0; i < 1000; i++)
                      fbs.ValueObjectBuilder(kind: fbs.ValueKind.Int, i: i),
                  ],
                ),
              ),
            ],
            slots: [
              fbs.SlotFillObjectBuilder(id: list.slots['item'], nodes: [1]),
            ],
          ),
          fbs.NodeObjectBuilder(
            widget: text.id,
            props: [
              fbs.PropObjectBuilder(id: text.props['data'], value: _str(2)),
            ],
          ),
        ],
        ['', 'layout', 'row'],
      ),
    );
    final built = find.text('row').evaluate().length;
    expect(built, greaterThan(5));
    expect(built, lessThan(100), reason: 'of 1000 items, only those in view');
  });

  testWidgets(
    'an image URL off the plugin\'s domains, or not HTTPS, is blocked and '
    'contained [SEC-080] [AST-002]',
    (tester) async {
      final image = _widget('Image');
      fbs.NodeObjectBuilder img(int url) => fbs.NodeObjectBuilder(
        widget: image.id,
        props: [
          fbs.PropObjectBuilder(
            id: image.props['source'],
            value: fbs.ValueObjectBuilder(
              kind: fbs.ValueKind.Object,
              entries: [
                fbs.EntryObjectBuilder(
                  key: ImageSourceFields.url,
                  value: _str(url),
                ),
              ],
            ),
          ),
          fbs.PropObjectBuilder(
            id: image.props['width'],
            value: fbs.ValueObjectBuilder(kind: fbs.ValueKind.Double, d: 20),
          ),
        ],
      );
      await show(
        tester,
        _galleryWith(
          [
            fbs.NodeObjectBuilder(widget: column.id, children: [1, 2, 3]),
            img(2),
            img(3),
            fbs.NodeObjectBuilder(
              widget: text.id,
              props: [
                fbs.PropObjectBuilder(id: text.props['data'], value: _str(4)),
              ],
            ),
          ],
          [
            '',
            'layout',
            'https://evil.example.net/a.png',
            'http://example.com/a.png',
            'still here',
          ],
        ),
      );
      expect(find.text('still here'), findsOneWidget);
      expect(find.byType(Image), findsNothing);
      expect(
        h.errors.where((e) => e.code == PluxErrorCode.outboundRequestBlocked),
        hasLength(2),
      );
    },
  );

  testWidgets('a node outside a page is a programming error', (tester) async {
    await tester.pumpWidget(const SizedBox());
    expect(
      () => RenderScope.of(tester.element(find.byType(SizedBox))),
      throwsStateError,
    );
  });
}
