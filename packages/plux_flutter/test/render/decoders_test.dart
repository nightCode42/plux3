// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/pxl/decimal.dart';
import 'package:plux_flutter/src/pxl/values.dart';
import 'package:plux_flutter/src/render/decoders.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/plux_icon.dart';

/// The icon fonts [_Rtl] resolves against; none is ever loaded.
final _fonts = IconFonts(VerifiedAssets(), report: (_) {});

/// A decoding that reads right to left and knows an icon of each set and
/// any image.
final class _Rtl implements Decoding {
  const _Rtl();

  @override
  TextDirection get textDirection => TextDirection.rtl;

  @override
  PluxIconSource? icon(String name, String set) =>
      (set, name) == ('material', 'star') ||
          (set, name) == ('cupertino', 'left_chevron')
      ? PluxIconSource(_fonts, set, '/nowhere', name)
      : null;

  @override
  ImageProvider<Object>? image({String? asset, String? url}) =>
      url == null ? null : NetworkImage(url);

  @override
  PluxVectorSource? vector(String asset) =>
      asset == 'svg' ? const PluxVectorSource(null) : null;
}

/// An object literal as a bundle would give it: fields by permanent ID.
final class _Object implements PluxObject {
  const _Object(this.fields);

  final Map<int, Object?> fields;

  @override
  Object? field(int id) => fields[id];

  @override
  Map<String, Object?> toMap() => const {};
}

void main() {
  const d = plainDecoding;
  const rtl = _Rtl();

  test('scalars and lists [WGT-002]', () {
    expect(asBool(d, true), isTrue);
    expect(asBool(d, 1), isNull);
    expect(asInt(d, 3), 3);
    expect(asDouble(d, 3), 3.0);
    expect(asDouble(d, Decimal.tryParse('2.5')), 2.5);
    expect(asDouble(d, 'x'), isNull);
    expect(asString(d, 'a'), 'a');
    expect(asColor(d, const PxlColor(0x11223344)), const Color(0x44112233));
    expect(
      asDuration(d, const PxlDuration(1500)),
      const Duration(milliseconds: 1500),
    );
    expect(asList(d, [1, 2], asInt), [1, 2]);
    expect(
      asList(d, [1, 'x'], asInt),
      isNull,
      reason: 'one bad item fails the list',
    );
    expect(asList(d, 'x', asInt), isNull);
    expect(asStringSet(d, ['a', 'a']), {'a'});
    expect(asColors(d, [const PxlColor(0xFF0000FF)]), [
      const Color(0xFFFF0000),
    ]);
    expect(asDoubles(d, [1, 2.5]), [1.0, 2.5]);
    expect(asLocale(d, 'de-AT'), const Locale('de', 'AT'));
    expect(
      asLocale(d, 'sr-Latn'),
      const Locale.fromSubtags(languageCode: 'sr', scriptCode: 'Latn'),
    );
    expect(
      asLocale(d, 'zh-Hant-TW'),
      const Locale.fromSubtags(
        languageCode: 'zh',
        scriptCode: 'Hant',
        countryCode: 'TW',
      ),
    );
    expect(asLocale(d, 'en'), const Locale('en'));
    expect(asLocale(d, ''), isNull);
    expect(asLocales(d, ['en']), [const Locale('en')]);
  });

  test('enums by permanent ID or by name', () {
    expect(decodeMainAxisAlignment(d, 'center'), MainAxisAlignment.center);
    expect(decodeMainAxisAlignment(d, 'nope'), isNull);
    expect(nameListStatus(d, 'loading'), 'loading');
    expect(decodeFontWeight(d, 'w700'), FontWeight.w700);
    expect(decodeFontWeight(d, 'w1000'), isNull);
    for (final n in [
      'w100',
      'w200',
      'w300',
      'w400',
      'w500',
      'w600',
      'w800',
      'w900',
    ]) {
      expect(decodeFontWeight(d, n), isNotNull, reason: n);
    }
    expect(
      decodeScrollPhysics(d, 'never'),
      isA<NeverScrollableScrollPhysics>(),
    );
    for (final n in ['platform', 'bouncing', 'clamping', 'always', 'page']) {
      expect(decodeScrollPhysics(d, n), isNotNull, reason: n);
    }
    expect(decodeScrollPhysics(d, 'x'), isNull);
    for (final n in ['none', 'underline', 'overline', 'lineThrough']) {
      expect(decodeTextDecoration(d, n), isNotNull, reason: n);
    }
    expect(decodeTextDecoration(d, 'x'), isNull);
    for (final n in [
      'text',
      'multiline',
      'number',
      'decimal',
      'signedNumber',
      'phone',
      'datetime',
      'emailAddress',
      'url',
      'visiblePassword',
      'name',
      'streetAddress',
      'none',
    ]) {
      expect(decodeTextInputType(d, n), isNotNull, reason: n);
    }
    expect(decodeTextInputType(d, 'x'), isNull);
    for (final n in [
      'standard',
      'comfortable',
      'compact',
      'adaptivePlatformDensity',
    ]) {
      expect(decodeVisualDensity(d, n), isNotNull, reason: n);
    }
    expect(decodeVisualDensity(d, 'x'), isNull);
    for (final n in [
      'startTop',
      'miniStartTop',
      'centerTop',
      'miniCenterTop',
      'endTop',
      'miniEndTop',
      'startFloat',
      'miniStartFloat',
      'centerFloat',
      'miniCenterFloat',
      'endFloat',
      'miniEndFloat',
      'startDocked',
      'miniStartDocked',
      'centerDocked',
      'miniCenterDocked',
      'endDocked',
      'miniEndDocked',
      'endContained',
    ]) {
      expect(decodeFloatingActionButtonLocation(d, n), isNotNull, reason: n);
    }
    expect(decodeFloatingActionButtonLocation(d, 'x'), isNull);
    expect(
      decodeFloatingLabelAlignment(d, 'start'),
      FloatingLabelAlignment.start,
    );
    expect(
      decodeFloatingLabelAlignment(d, 'center'),
      FloatingLabelAlignment.center,
    );
    expect(decodeFloatingLabelAlignment(d, 'x'), isNull);
    for (final n in ['top', 'center', 'bottom']) {
      expect(decodeTextAlignVertical(d, n), isNotNull, reason: n);
    }
    expect(decodeTextAlignVertical(d, 'x'), isNull);
  });

  test('insets choose all, axes, sides and directional forms', () {
    expect(decodeEdgeInsets(d, {'all': 4}), const EdgeInsets.all(4));
    expect(
      decodeEdgeInsets(d, {'horizontal': 2, 'vertical': 3, 'top': 1}),
      const EdgeInsets.fromLTRB(2, 1, 2, 3),
    );
    expect(
      decodeEdgeInsets(d, {'start': 5, 'all': 1}),
      const EdgeInsetsDirectional.fromSTEB(5, 1, 1, 1),
    );
    expect(
      decodeEdgeInsets(d, const _Object({EdgeInsetsFields.left: 7})),
      const EdgeInsets.only(left: 7),
    );
    expect(decodeEdgeInsets(d, 3), isNull);
    expect(
      decodeEdgeInsetsResolved(rtl, {'start': 5}),
      const EdgeInsets.only(right: 5),
    );
  });

  test('alignments, radii and borders', () {
    expect(decodeAlignment(d, {'x': 1, 'y': -1}), Alignment.topRight);
    expect(
      decodeAlignment(d, {'start': -1, 'y': 0}),
      AlignmentDirectional.centerStart,
    );
    expect(decodeAlignment(d, null), isNull);
    expect(
      decodeAlignmentDirectional(rtl, {'x': 1, 'y': 0}),
      AlignmentDirectional.centerStart,
    );
    expect(
      decodeAlignmentDirectional(d, {'start': 1, 'y': 0}),
      AlignmentDirectional.centerEnd,
    );
    expect(decodeAlignmentDirectional(d, 'x'), isNull);
    expect(decodeRadius(d, {'circular': 4}), const Radius.circular(4));
    expect(decodeRadius(d, {'x': 1, 'y': 2}), const Radius.elliptical(1, 2));
    expect(decodeRadius(d, 1), isNull);
    expect(decodeBorderRadius(d, {'all': 3}), BorderRadius.circular(3));
    expect(
      decodeBorderRadius(d, {
        'topLeft': {'circular': 2},
      }),
      const BorderRadius.only(topLeft: Radius.circular(2)),
    );
    expect(decodeBorderRadius(d, 1), isNull);
    expect(
      decodeBorderRadiusResolved(rtl, {'all': 3}),
      BorderRadius.circular(3),
    );
    final side = {'color': const PxlColor(0x000000FF), 'width': 2.0};
    expect(decodeBorder(d, {'all': side}), Border.all(width: 2));
    expect(
      decodeBorder(d, {'top': side}),
      const Border(top: BorderSide(width: 2)),
    );
    expect(decodeBorder(d, 1), isNull);
  });

  test('shapes and input borders by kind', () {
    expect(decodeShapeBorder(d, {'kind': 'stadium'}), isA<StadiumBorder>());
    expect(
      decodeShapeBorder(d, {'kind': 'circle', 'eccentricity': 0.5}),
      isA<CircleBorder>(),
    );
    expect(
      decodeShapeBorder(d, {'kind': 'beveledRectangle'}),
      isA<BeveledRectangleBorder>(),
    );
    expect(
      decodeShapeBorder(d, {'kind': 'continuousRectangle'}),
      isA<ContinuousRectangleBorder>(),
    );
    expect(
      decodeShapeBorder(d, {
        'borderRadius': {'all': 4},
      }),
      isA<RoundedRectangleBorder>(),
    );
    expect(decodeShapeBorder(d, 1), isNull);
    expect(decodeOutlinedBorder(d, {'kind': 'stadium'}), isA<StadiumBorder>());
    expect(decodeInputBorder(d, {'kind': 'none'}), InputBorder.none);
    expect(
      decodeInputBorder(d, {'kind': 'underline'}),
      isA<UnderlineInputBorder>(),
    );
    expect(
      decodeInputBorder(d, {'kind': 'outline', 'gapPadding': 2}),
      isA<OutlineInputBorder>(),
    );
    expect(decodeInputBorder(d, 1), isNull);
  });

  test('gradients by kind', () {
    final colors = [const PxlColor(0xFF0000FF), const PxlColor(0x0000FFFF)];
    expect(decodeGradient(d, {'colors': colors}), isA<LinearGradient>());
    expect(
      decodeGradient(d, {'kind': 'radial', 'colors': colors, 'radius': 0.8}),
      isA<RadialGradient>(),
    );
    expect(
      decodeGradient(d, {
        'kind': 'sweep',
        'colors': colors,
        'stops': [0.0, 1.0],
      }),
      isA<SweepGradient>(),
    );
    expect(
      decodeGradient(d, {'kind': 'linear'}),
      isNull,
      reason: 'colors are required',
    );
    expect(decodeGradient(d, 1), isNull);
  });

  test('icons and images come from the runtime [THM-005] [RT-014]', () {
    expect(decodeIconData(rtl, {'name': 'star'})?.name, 'star');
    expect(
      decodeIconData(rtl, {'name': 'left_chevron', 'set': 'cupertino'})?.name,
      'left_chevron',
    );
    expect(decodeIconData(rtl, {'name': 'star', 'set': 'cupertino'}), isNull);
    expect(decodeIconData(rtl, {'name': 'nope'}), isNull);
    expect(decodeIconData(rtl, {'set': 'material'}), isNull);
    expect(decodeIconData(rtl, 1), isNull);
    expect(decodeIconWidget(rtl, {'name': 'star'}), isA<PluxIcon>());
    expect(
      decodeIconWidget(d, {'name': 'star'}),
      isNull,
      reason: 'a plain decoding knows no icons',
    );
    expect(
      decodeImageSource(rtl, {'url': 'https://x/a.png'}),
      isA<NetworkImage>(),
    );
    expect(decodeImageSource(rtl, 1), isNull);
    expect(decodeVectorSource(rtl, {'asset': 'svg'}), isNotNull);
    expect(decodeVectorSource(rtl, {'asset': 'png'}), isNull);
    expect(decodeVectorSource(rtl, {'url': 'https://x/a.svg'}), isNull);
    expect(decodeVectorSource(rtl, 1), isNull);
    expect(plainDecoding.vector('svg'), isNull);
  });

  test('values per widget state, in precedence order', () {
    final colors = decodeWidgetStateColor(d, {
      'value': const PxlColor(0x000000FF),
      'disabled': const PxlColor(0xFFFFFFFF),
      'pressed': const PxlColor(0xFF0000FF),
    })!;
    expect(colors.resolve({}), const Color(0xFF000000));
    expect(colors.resolve({WidgetState.pressed}), const Color(0xFFFF0000));
    expect(
      colors.resolve({WidgetState.pressed, WidgetState.disabled}),
      const Color(0xFFFFFFFF),
    );
    expect(decodeWidgetStateColor(d, 1), isNull);
    expect(decodeWidgetStateDouble(d, {'value': 2.0})!.resolve({}), 2.0);
    expect(
      decodeWidgetStateTextStyle(d, {
        'value': {'fontSize': 12.0},
      })!.resolve({})!.fontSize,
      12,
    );
    expect(
      decodeWidgetStateEdgeInsets(d, {
        'hovered': {'all': 1.0},
      })!.resolve({WidgetState.hovered}),
      const EdgeInsets.all(1),
    );
    expect(
      decodeWidgetStateSize(d, {
        'value': {'width': 1.0, 'height': 2.0},
      })!.resolve({}),
      const Size(1, 2),
    );
    expect(
      decodeWidgetStateBorderSide(d, {
        'value': {'width': 2.0},
      })!.resolve({})!.width,
      2,
    );
    expect(
      decodeWidgetStateShapeBorder(d, {
        'value': {'kind': 'stadium'},
      })!.resolve({}),
      isA<StadiumBorder>(),
    );
  });

  test('every widget-state type has the same field IDs', () {
    // _stateful reads them all with WidgetStateColor's IDs.
    const ids = [
      WidgetStateColorFields.value,
      WidgetStateColorFields.disabled,
      WidgetStateColorFields.error,
      WidgetStateColorFields.dragged,
      WidgetStateColorFields.pressed,
      WidgetStateColorFields.hovered,
      WidgetStateColorFields.focused,
      WidgetStateColorFields.selected,
    ];
    for (final other in [
      [
        WidgetStateDoubleFields.value,
        WidgetStateDoubleFields.disabled,
        WidgetStateDoubleFields.error,
        WidgetStateDoubleFields.dragged,
        WidgetStateDoubleFields.pressed,
        WidgetStateDoubleFields.hovered,
        WidgetStateDoubleFields.focused,
        WidgetStateDoubleFields.selected,
      ],
      [
        WidgetStateEdgeInsetsFields.value,
        WidgetStateEdgeInsetsFields.disabled,
        WidgetStateEdgeInsetsFields.error,
        WidgetStateEdgeInsetsFields.dragged,
        WidgetStateEdgeInsetsFields.pressed,
        WidgetStateEdgeInsetsFields.hovered,
        WidgetStateEdgeInsetsFields.focused,
        WidgetStateEdgeInsetsFields.selected,
      ],
      [
        WidgetStateBorderSideFields.value,
        WidgetStateBorderSideFields.disabled,
        WidgetStateBorderSideFields.error,
        WidgetStateBorderSideFields.dragged,
        WidgetStateBorderSideFields.pressed,
        WidgetStateBorderSideFields.hovered,
        WidgetStateBorderSideFields.focused,
        WidgetStateBorderSideFields.selected,
      ],
      [
        WidgetStateShapeBorderFields.value,
        WidgetStateShapeBorderFields.disabled,
        WidgetStateShapeBorderFields.error,
        WidgetStateShapeBorderFields.dragged,
        WidgetStateShapeBorderFields.pressed,
        WidgetStateShapeBorderFields.hovered,
        WidgetStateShapeBorderFields.focused,
        WidgetStateShapeBorderFields.selected,
      ],
    ]) {
      expect(other, ids);
    }
  });

  test('generic entries hold strings', () {
    final seg = decodeButtonSegment(d, {
      'value': 'a',
      'label': 'A',
      'tooltip': 't',
    })!;
    expect(seg.value, 'a');
    expect(seg.enabled, isTrue);
    expect(
      decodeButtonSegment(d, {'label': 'A'}),
      isNull,
      reason: 'value is required',
    );
    final entry = decodeDropdownMenuEntry(d, {
      'value': 'x',
      'label': 'X',
      'enabled': false,
    })!;
    expect((entry.value, entry.label, entry.enabled), ('x', 'X', false));
    expect(
      decodeDropdownMenuEntry(d, {'value': 'x'}),
      isNull,
      reason: 'label is required',
    );
    expect(decodeDropdownMenuEntry(d, 1), isNull);
  });

  test('generated decoders read objects by ID or by name, with defaults', () {
    final side = decodeBorderSide(d, const _Object({2: 3.0}))!;
    expect(side.width, 3);
    expect(side.style, BorderStyle.solid, reason: 'descriptor default');
    final shadow = decodeBoxShadow(d, {'blurRadius': 2.0})!;
    expect(
      shadow.color,
      const BoxShadow().color,
      reason: "Flutter's private default is kept",
    );
    expect(
      decodeBoxShadow(d, {'color': const PxlColor(0xFF0000FF)})!.color,
      const Color(0xFFFF0000),
    );
    expect(Fields.of(d, 'x'), isNull);
    expect(Fields.of(d, {'a': 1})!.has(0, 'a'), isTrue);
    expect(
      () => Fields.of(d, <String, Object?>{})!.missing('x'),
      throwsA(isA<DecodeError>()),
    );
    expect(const DecodeError('m').toString(), 'm');
    expect(plainDecoding.icon('star', 'material'), isNull);
    expect(plainDecoding.image(url: 'u'), isNull);
    expect(plainDecoding.textDirection, TextDirection.ltr);
  });
}
