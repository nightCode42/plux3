// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/errors/plux_exception.dart';
import 'package:plux_flutter/src/render/plux_icon.dart';

/// The golden icon fonts the compiler made for the widget gallery, by set.
Map<String, (String, Uint8List)> goldenFonts() => {
  for (final f in Directory(
    '../../schema/testdata/bundles/widgets/icons',
  ).listSync().whereType<File>())
    if (iconFontNames(f.readAsBytesSync()) case final names?)
      names.containsKey('left_chevron') ? 'cupertino' : 'material': (
        f.path,
        f.readAsBytesSync(),
      ),
};

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final fonts = goldenFonts();

  test('an icon font names its icons, with code points and mirroring '
      '[THM-005]', () {
    expect(iconFontNames(fonts['cupertino']!.$2), {
      'left_chevron': (0xf3d2, true),
    });
    expect(iconFontNames(fonts['material']!.$2), {
      'arrow_back': (0xe5c4, true),
      'star': (0xf09a, false),
    });
    expect(iconFontNames(Uint8List(16)), isNull);
    expect(iconFontKey('material'), '@icons/material');
    expect(isIconFontKey('@icons/cupertino'), isTrue);
    expect(isIconFontKey('logo'), isFalse);
  });

  test(
    'a font loads once, checked against its hash, and then draws its '
    'icons; one that cannot be used is reported [THM-005] [AST-001]',
    () async {
      final loads = <String>[];
      final errors = <PluxException>[];
      final registry = IconFonts(
        VerifiedAssets(),
        report: errors.add,
        load: (family, files) async => loads.add(family),
      );
      var notified = 0;
      registry.addListener(() => notified++);
      final (path, bytes) = fonts['material']!;
      final hash = sha256.convert(bytes).toString();
      expect(registry.glyph(hash, path, 'star'), isNull, reason: 'loading');
      await registry.load(hash, path);
      expect(
        registry.glyph(hash, path, 'star'),
        IconGlyph(
          0xf09a,
          'plux-icons-${hash.substring(0, 16)}',
          mirrored: false,
        ),
      );
      expect(registry.glyph(hash, path, 'arrow_back')?.mirrored, isTrue);
      expect(registry.glyph(hash, path, 'home'), isNull);
      expect(loads, ['plux-icons-${hash.substring(0, 16)}']);
      expect((notified, errors.length), (1, 0));
      final source = PluxIconSource(registry, hash, path, 'star');
      await source.load();
      expect((source.name, source.glyph?.codePoint), ('star', 0xf09a));

      final dir = Directory.systemTemp.createTempSync('icons');
      addTearDown(() => dir.deleteSync(recursive: true));
      final wrong = sha256.convert([1]).toString();
      final tampered = '${dir.path}/$wrong';
      File(tampered).writeAsBytesSync(bytes);
      await registry.load(wrong, tampered);
      final missing = sha256.convert([2]).toString();
      await registry.load(missing, '/no/such/font');
      final plain = '${dir.path}/plain';
      File(plain).writeAsBytesSync(Uint8List(16));
      final plainHash = sha256.convert(Uint8List(16)).toString();
      await registry.load(plainHash, plain);
      expect(errors.map((e) => e.code), [
        PluxErrorCode.assetHashMismatch,
        PluxErrorCode.propValueInvalid,
        PluxErrorCode.propValueInvalid,
      ]);
      expect(registry.glyph(wrong, tampered, 'star'), isNull);
      expect(loads, hasLength(1));
    },
  );

  group('PluxIcon draws a glyph as Flutter draws an Icon [THM-005]', () {
    late IconFonts registry;
    late String hash;

    Future<Widget> icon(
      WidgetTester tester,
      String name, {
      TextDirection direction = TextDirection.ltr,
      IconThemeData theme = const IconThemeData(color: Color(0xFF112233)),
      bool? scaled,
      BlendMode? blendMode,
    }) async {
      final (path, bytes) = fonts['material']!;
      hash = sha256.convert(bytes).toString();
      registry = IconFonts(VerifiedAssets(), report: (_) {});
      await tester.runAsync(() => registry.load(hash, path));
      return MediaQuery(
        data: const MediaQueryData(textScaler: TextScaler.linear(2)),
        child: Directionality(
          textDirection: direction,
          child: IconTheme(
            data: theme,
            child: Center(
              child: PluxIcon(
                PluxIconSource(registry, hash, path, name),
                size: 20,
                fill: 1,
                weight: 700,
                grade: 0,
                opticalSize: 24,
                semanticLabel: name,
                applyTextScaling: scaled,
                blendMode: blendMode,
              ),
            ),
          ),
        ),
      );
    }

    RichText glyph(WidgetTester tester) =>
        tester.widget<RichText>(find.byType(RichText));

    testWidgets('with its variations, size, colour and label', (tester) async {
      await tester.pumpWidget(await icon(tester, 'star'));
      final span = glyph(tester).text as TextSpan;
      expect(span.text, String.fromCharCode(0xf09a));
      expect(span.style!.fontFamily, 'plux-icons-${hash.substring(0, 16)}');
      expect(span.style!.color, const Color(0xFF112233));
      expect(span.style!.fontVariations, const [
        FontVariation('FILL', 1),
        FontVariation('wght', 700),
        FontVariation('GRAD', 0),
        FontVariation('opsz', 24),
      ]);
      expect(tester.getSize(find.byType(SizedBox).first), const Size(20, 20));
      expect(find.bySemanticsLabel('star'), findsOneWidget);
      expect(find.byType(Transform), findsNothing);
    });

    testWidgets('mirrored in right-to-left text when the icon asks', (
      tester,
    ) async {
      await tester.pumpWidget(
        await icon(tester, 'arrow_back', direction: TextDirection.rtl),
      );
      expect(
        tester.widget<Transform>(find.byType(Transform)).transform,
        Matrix4.diagonal3Values(-1, 1, 1),
      );
      await tester.pumpWidget(
        await icon(tester, 'star', direction: TextDirection.rtl),
      );
      expect(find.byType(Transform), findsNothing);
    });

    testWidgets('scaled with text, faded by the theme, blended on request', (
      tester,
    ) async {
      await tester.pumpWidget(
        await icon(
          tester,
          'star',
          scaled: true,
          theme: const IconThemeData(color: Color(0xFF000000), opacity: 0.5),
          blendMode: BlendMode.multiply,
        ),
      );
      final style = (glyph(tester).text as TextSpan).style!;
      expect(style.fontSize, 40);
      expect(style.color, isNull);
      expect(style.foreground!.blendMode, BlendMode.multiply);
      expect(style.foreground!.color.a, closeTo(0.5, 0.01));
    });

    testWidgets('an icon the font does not name, or none, is an empty box '
        'of its size', (tester) async {
      await tester.pumpWidget(await icon(tester, 'home'));
      expect(find.byType(RichText), findsNothing);
      expect(tester.getSize(find.byType(SizedBox)), const Size(20, 20));
      await tester.pumpWidget(
        const Directionality(
          textDirection: TextDirection.ltr,
          child: Center(child: PluxIcon(null, semanticLabel: 'none')),
        ),
      );
      expect(tester.getSize(find.byType(SizedBox)), const Size(24, 24));
      expect(find.bySemanticsLabel('none'), findsOneWidget);
    });
  });
}
