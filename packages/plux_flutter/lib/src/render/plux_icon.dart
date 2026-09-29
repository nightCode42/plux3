// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The `Icon` widget of Plux pages (THM-005).
library;

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';

/// Draws an icon of the release's icon font exactly as Flutter's `Icon`
/// draws an `IconData`: sized and coloured by the `IconTheme`, with the
/// font variations of Material Symbols, mirrored in right-to-left text
/// when the icon asks for it, and one semantics node. Flutter's `Icon`
/// cannot be used: its `IconData` must be a compile-time constant, or the
/// host app's release build fails its icon tree shaking.
final class PluxIcon extends StatelessWidget {
  /// Creates the icon.
  const PluxIcon(
    this.icon, {
    super.key,
    this.size,
    this.fill,
    this.weight,
    this.grade,
    this.opticalSize,
    this.color,
    this.shadows,
    this.semanticLabel,
    this.textDirection,
    this.applyTextScaling,
    this.blendMode,
    this.fontWeight,
  });

  /// The icon; null draws an empty square of the icon's size.
  final PluxIconSource? icon;

  /// As `Icon.size`.
  final double? size;

  /// As `Icon.fill`.
  final double? fill;

  /// As `Icon.weight`.
  final double? weight;

  /// As `Icon.grade`.
  final double? grade;

  /// As `Icon.opticalSize`.
  final double? opticalSize;

  /// As `Icon.color`.
  final Color? color;

  /// As `Icon.shadows`.
  final List<Shadow>? shadows;

  /// As `Icon.semanticLabel`.
  final String? semanticLabel;

  /// As `Icon.textDirection`.
  final TextDirection? textDirection;

  /// As `Icon.applyTextScaling`.
  final bool? applyTextScaling;

  /// As `Icon.blendMode`.
  final BlendMode? blendMode;

  /// As `Icon.fontWeight`.
  final FontWeight? fontWeight;

  @override
  Widget build(BuildContext context) {
    final source = icon;
    return source == null
        ? _draw(context, null)
        : ListenableBuilder(
            listenable: source.changes,
            builder: (context, _) => _draw(context, source.glyph),
          );
  }

  Widget _draw(BuildContext context, IconGlyph? glyph) {
    final direction = textDirection ?? Directionality.of(context);
    final theme = IconTheme.of(context);
    final tentative = size ?? theme.size ?? kDefaultFontSize;
    final iconSize = (applyTextScaling ?? theme.applyTextScaling ?? false)
        ? MediaQuery.textScalerOf(context).scale(tentative)
        : tentative;
    if (glyph == null) {
      return Semantics(
        label: semanticLabel,
        child: SizedBox(width: iconSize, height: iconSize),
      );
    }
    final opacity = theme.opacity ?? 1.0;
    Color? iconColor = color ?? theme.color!;
    if (opacity != 1.0) {
      iconColor = iconColor.withValues(alpha: iconColor.a * opacity);
    }
    Paint? foreground;
    if (blendMode case final mode?) {
      foreground = Paint()
        ..blendMode = mode
        ..color = iconColor;
      iconColor = null;
    }
    final iconFill = fill ?? theme.fill;
    final iconWeight = weight ?? theme.weight;
    final iconGrade = grade ?? theme.grade;
    final iconOpticalSize = opticalSize ?? theme.opticalSize;
    Widget drawn = RichText(
      overflow: TextOverflow.visible,
      textDirection: direction,
      text: TextSpan(
        text: String.fromCharCode(glyph.codePoint),
        style: TextStyle(
          fontVariations: [
            if (iconFill != null) FontVariation('FILL', iconFill),
            if (iconWeight != null) FontVariation('wght', iconWeight),
            if (iconGrade != null) FontVariation('GRAD', iconGrade),
            if (iconOpticalSize != null) FontVariation('opsz', iconOpticalSize),
          ],
          inherit: false,
          color: iconColor,
          fontSize: iconSize,
          fontFamily: glyph.family,
          fontWeight: fontWeight,
          shadows: shadows ?? theme.shadows,
          height: 1,
          leadingDistribution: TextLeadingDistribution.even,
          foreground: foreground,
        ),
      ),
    );
    if (glyph.mirrored && direction == TextDirection.rtl) {
      drawn = Transform(
        transform: Matrix4.diagonal3Values(-1, 1, 1),
        alignment: Alignment.center,
        transformHitTests: false,
        child: drawn,
      );
    }
    return Semantics(
      label: semanticLabel,
      child: ExcludeSemantics(
        child: SizedBox(
          width: iconSize,
          height: iconSize,
          child: Center(child: drawn),
        ),
      ),
    );
  }
}
