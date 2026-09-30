// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The hand-written decoders (ADR-0031): value types whose Flutter
/// counterpart is chosen by the fields that are set, that need the runtime
/// (icons, images), or whose Flutter counterpart is a class of constants
/// rather than an enum. Field IDs come from the generated registry
/// (`render.g.dart`, BND-011).
library;

import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:plux_flutter/src/assets/icon_fonts.dart';
import 'package:plux_flutter/src/assets/image_providers.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/plux_icon.dart';

// ── Geometry ───────────────────────────────────────────────────────────────

/// Insets: directional when `start` or `end` is set; a side falls back to
/// its axis (`horizontal`, `vertical`), then to `all`, then to zero.
EdgeInsetsGeometry? decodeEdgeInsets(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  double? n(int id, String name) => asDouble(d, f.get(id, name));
  final all = n(EdgeInsetsFields.all, 'all') ?? 0;
  final h = n(EdgeInsetsFields.horizontal, 'horizontal') ?? all;
  final vert = n(EdgeInsetsFields.vertical, 'vertical') ?? all;
  final top = n(EdgeInsetsFields.top, 'top') ?? vert;
  final bottom = n(EdgeInsetsFields.bottom, 'bottom') ?? vert;
  final start = n(EdgeInsetsFields.start, 'start');
  final end = n(EdgeInsetsFields.end, 'end');
  if (start != null || end != null) {
    return EdgeInsetsDirectional.fromSTEB(start ?? h, top, end ?? h, bottom);
  }
  return EdgeInsets.fromLTRB(
    n(EdgeInsetsFields.left, 'left') ?? h,
    top,
    n(EdgeInsetsFields.right, 'right') ?? h,
    bottom,
  );
}

/// Insets resolved for the reading direction, where Flutter wants plain
/// [EdgeInsets].
EdgeInsets? decodeEdgeInsetsResolved(Decoding d, Object? v) =>
    decodeEdgeInsets(d, v)?.resolve(d.textDirection);

/// An alignment: directional when `start` is set.
AlignmentGeometry? decodeAlignment(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final y = asDouble(d, f.get(AlignmentFields.y, 'y')) ?? 0;
  final start = asDouble(d, f.get(AlignmentFields.start, 'start'));
  if (start != null) return AlignmentDirectional(start, y);
  return Alignment(asDouble(d, f.get(AlignmentFields.x, 'x')) ?? 0, y);
}

/// An alignment as Flutter's [AlignmentDirectional]; an absolute `x` is
/// converted for the reading direction.
AlignmentDirectional? decodeAlignmentDirectional(Decoding d, Object? v) {
  final a = decodeAlignment(d, v);
  return switch (a) {
    AlignmentDirectional() => a,
    Alignment() => AlignmentDirectional(
      d.textDirection == TextDirection.rtl ? -a.x : a.x,
      a.y,
    ),
    _ => null,
  };
}

/// A radius: circular when `circular` is set, else elliptical.
Radius? decodeRadius(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final c = asDouble(d, f.get(RadiusFields.circular, 'circular'));
  if (c != null) return Radius.circular(c);
  return Radius.elliptical(
    asDouble(d, f.get(RadiusFields.x, 'x')) ?? 0,
    asDouble(d, f.get(RadiusFields.y, 'y')) ?? 0,
  );
}

/// Corner radii: `all` for every corner, a corner's own radius over it.
BorderRadiusGeometry? decodeBorderRadius(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final all = asDouble(d, f.get(BorderRadiusFields.all, 'all'));
  final base = all == null ? Radius.zero : Radius.circular(all);
  Radius r(int id, String name) => decodeRadius(d, f.get(id, name)) ?? base;
  return BorderRadius.only(
    topLeft: r(BorderRadiusFields.topLeft, 'topLeft'),
    topRight: r(BorderRadiusFields.topRight, 'topRight'),
    bottomLeft: r(BorderRadiusFields.bottomLeft, 'bottomLeft'),
    bottomRight: r(BorderRadiusFields.bottomRight, 'bottomRight'),
  );
}

/// Corner radii as Flutter's [BorderRadius].
BorderRadius? decodeBorderRadiusResolved(Decoding d, Object? v) =>
    decodeBorderRadius(d, v)?.resolve(d.textDirection);

// ── Borders and shapes ─────────────────────────────────────────────────────

/// A box border: `all` for every side, a side's own over it.
BoxBorder? decodeBorder(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final all =
      decodeBorderSide(d, f.get(BorderFields.all, 'all')) ?? BorderSide.none;
  BorderSide side(int id, String name) =>
      decodeBorderSide(d, f.get(id, name)) ?? all;
  return Border(
    top: side(BorderFields.top, 'top'),
    right: side(BorderFields.right, 'right'),
    bottom: side(BorderFields.bottom, 'bottom'),
    left: side(BorderFields.left, 'left'),
  );
}

/// A shape by its `kind`, a rounded rectangle when none is set.
OutlinedBorder? decodeShapeBorder(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final side =
      decodeBorderSide(d, f.get(ShapeBorderFields.side, 'side')) ??
      BorderSide.none;
  final radius =
      decodeBorderRadius(
        d,
        f.get(ShapeBorderFields.borderRadius, 'borderRadius'),
      ) ??
      BorderRadius.zero;
  return switch (nameShapeKind(d, f.get(ShapeBorderFields.kind, 'kind'))) {
    'stadium' => StadiumBorder(side: side),
    'circle' => CircleBorder(
      side: side,
      eccentricity:
          asDouble(d, f.get(ShapeBorderFields.eccentricity, 'eccentricity')) ??
          0,
    ),
    'beveledRectangle' => BeveledRectangleBorder(
      side: side,
      borderRadius: radius,
    ),
    'continuousRectangle' => ContinuousRectangleBorder(
      side: side,
      borderRadius: radius,
    ),
    _ => RoundedRectangleBorder(side: side, borderRadius: radius),
  };
}

/// A shape where Flutter wants an [OutlinedBorder]; every shape is one.
OutlinedBorder? decodeOutlinedBorder(Decoding d, Object? v) =>
    decodeShapeBorder(d, v);

/// An input field border by its `kind`.
InputBorder? decodeInputBorder(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final side =
      decodeBorderSide(d, f.get(InputBorderFields.borderSide, 'borderSide')) ??
      const BorderSide();
  final radius = decodeBorderRadiusResolved(
    d,
    f.get(InputBorderFields.borderRadius, 'borderRadius'),
  );
  return switch (nameInputBorderKind(
    d,
    f.get(InputBorderFields.kind, 'kind'),
  )) {
    'none' => InputBorder.none,
    'underline' => UnderlineInputBorder(
      borderSide: side,
      borderRadius:
          radius ??
          const BorderRadius.only(
            topLeft: Radius.circular(4),
            topRight: Radius.circular(4),
          ),
    ),
    _ => OutlineInputBorder(
      borderSide: side,
      borderRadius: radius ?? const BorderRadius.all(Radius.circular(4)),
      gapPadding:
          asDouble(d, f.get(InputBorderFields.gapPadding, 'gapPadding')) ?? 4,
    ),
  };
}

// ── Paint ──────────────────────────────────────────────────────────────────

/// A gradient by its `kind`: linear, radial or sweep.
Gradient? decodeGradient(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final colors = asColors(d, f.get(GradientFields.colors, 'colors'));
  if (colors == null) return null;
  final stops = asDoubles(d, f.get(GradientFields.stops, 'stops'));
  final tile =
      decodeTileMode(d, f.get(GradientFields.tileMode, 'tileMode')) ??
      TileMode.clamp;
  AlignmentGeometry? at(int id, String name) =>
      decodeAlignment(d, f.get(id, name));
  double? n(int id, String name) => asDouble(d, f.get(id, name));
  return switch (nameGradientKind(d, f.get(GradientFields.kind, 'kind'))) {
    'radial' => RadialGradient(
      colors: colors,
      stops: stops,
      tileMode: tile,
      center: at(GradientFields.center, 'center') ?? Alignment.center,
      radius: n(GradientFields.radius, 'radius') ?? 0.5,
      focal: at(GradientFields.focal, 'focal'),
      focalRadius: n(GradientFields.focalRadius, 'focalRadius') ?? 0,
    ),
    'sweep' => SweepGradient(
      colors: colors,
      stops: stops,
      tileMode: tile,
      center: at(GradientFields.center, 'center') ?? Alignment.center,
      startAngle: n(GradientFields.startAngle, 'startAngle') ?? 0,
      endAngle: n(GradientFields.endAngle, 'endAngle') ?? math.pi * 2,
    ),
    _ => LinearGradient(
      colors: colors,
      stops: stops,
      tileMode: tile,
      begin: at(GradientFields.begin, 'begin') ?? Alignment.centerLeft,
      end: at(GradientFields.end, 'end') ?? Alignment.centerRight,
    ),
  };
}

// ── Runtime resources ──────────────────────────────────────────────────────

/// An icon, resolved against the release's icon fonts (THM-005).
PluxIconSource? decodeIconData(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final name = asString(d, f.get(IconDataFields.name, 'name'));
  if (name == null) return null;
  return d.icon(
    name,
    nameIconSet(d, f.get(IconDataFields.set, 'set')) ?? 'material',
  );
}

/// An icon as a widget, where Flutter takes one.
Widget? decodeIconWidget(Decoding d, Object? v) {
  final icon = decodeIconData(d, v);
  return icon == null ? null : PluxIcon(icon);
}

/// An image: an asset of the release or a URL.
ImageProvider<Object>? decodeImageSource(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  return d.image(
    asset: asString(d, f.get(ImageSourceFields.asset, 'asset')),
    url: asString(d, f.get(ImageSourceFields.url, 'url')),
  );
}

/// What the `Image` widget draws from an `ImageSource`: the
/// `vector_graphics` form of an SVG asset (CMP-031), else the image and its
/// ThumbHash placeholder (RT-014). A record, so that a source without a
/// vector form, an image the release cannot supply (reported where it was
/// resolved) or a placeholder is not a failure. Null only for a malformed
/// source.
({
  PluxVectorSource? vector,
  ImageProvider<Object>? image,
  ImageProvider<Object>? placeholder,
})?
decodeImageForm(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final asset = asString(d, f.get(ImageSourceFields.asset, 'asset'));
  if (asset != null) {
    if (d.vector(asset) case final vector?) {
      return (vector: vector, image: null, placeholder: null);
    }
  }
  return (
    vector: null,
    image: d.image(
      asset: asset,
      url: asString(d, f.get(ImageSourceFields.url, 'url')),
    ),
    placeholder: ThumbHashImage.tryParse(
      asString(d, f.get(ImageSourceFields.thumbHash, 'thumbHash')),
    ),
  );
}

// ── Widget states ──────────────────────────────────────────────────────────

/// A value per widget state: the first set of disabled, error, dragged,
/// pressed, hovered, focused and selected that applies, else `value`. The
/// field IDs are the same in every WidgetState type.
WidgetStateProperty<T?>? _stateful<T>(
  Decoding d,
  Object? v,
  T? Function(Decoding d, Object? v) item,
) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  const order = [
    (WidgetState.disabled, WidgetStateColorFields.disabled, 'disabled'),
    (WidgetState.error, WidgetStateColorFields.error, 'error'),
    (WidgetState.dragged, WidgetStateColorFields.dragged, 'dragged'),
    (WidgetState.pressed, WidgetStateColorFields.pressed, 'pressed'),
    (WidgetState.hovered, WidgetStateColorFields.hovered, 'hovered'),
    (WidgetState.focused, WidgetStateColorFields.focused, 'focused'),
    (WidgetState.selected, WidgetStateColorFields.selected, 'selected'),
  ];
  final byState = <(WidgetState, T)>[
    for (final (state, id, name) in order)
      if (item(d, f.get(id, name)) case final T value) (state, value),
  ];
  final base = item(d, f.get(WidgetStateColorFields.value, 'value'));
  return WidgetStateProperty.resolveWith((states) {
    for (final (state, value) in byState) {
      if (states.contains(state)) return value;
    }
    return base;
  });
}

/// A color per widget state.
WidgetStateProperty<Color?>? decodeWidgetStateColor(Decoding d, Object? v) =>
    _stateful(d, v, asColor);

/// A double per widget state.
WidgetStateProperty<double?>? decodeWidgetStateDouble(Decoding d, Object? v) =>
    _stateful(d, v, asDouble);

/// A text style per widget state.
WidgetStateProperty<TextStyle?>? decodeWidgetStateTextStyle(
  Decoding d,
  Object? v,
) => _stateful(d, v, decodeTextStyle);

/// Insets per widget state.
WidgetStateProperty<EdgeInsetsGeometry?>? decodeWidgetStateEdgeInsets(
  Decoding d,
  Object? v,
) => _stateful(d, v, decodeEdgeInsets);

/// A size per widget state.
WidgetStateProperty<Size?>? decodeWidgetStateSize(Decoding d, Object? v) =>
    _stateful(d, v, decodeSize);

/// A border side per widget state.
WidgetStateProperty<BorderSide?>? decodeWidgetStateBorderSide(
  Decoding d,
  Object? v,
) => _stateful(d, v, decodeBorderSide);

/// A shape per widget state.
WidgetStateProperty<OutlinedBorder?>? decodeWidgetStateShapeBorder(
  Decoding d,
  Object? v,
) => _stateful(d, v, decodeShapeBorder);

// ── Generic entries (their value is a string, ADR-0031) ────────────────────

/// A segment of a segmented button.
ButtonSegment<String>? decodeButtonSegment(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  final value = f == null
      ? null
      : asString(d, f.get(ButtonSegmentFields.value, 'value'));
  if (f == null || value == null) return null;
  final label = asString(d, f.get(ButtonSegmentFields.label, 'label'));
  return ButtonSegment<String>(
    value: value,
    icon: decodeIconWidget(d, f.get(ButtonSegmentFields.icon, 'icon')),
    label: label == null ? null : Text(label),
    tooltip: asString(d, f.get(ButtonSegmentFields.tooltip, 'tooltip')),
    enabled: asBool(d, f.get(ButtonSegmentFields.enabled, 'enabled')) ?? true,
  );
}

/// An entry of a dropdown menu.
DropdownMenuEntry<String>? decodeDropdownMenuEntry(Decoding d, Object? v) {
  final f = Fields.of(d, v);
  if (f == null) return null;
  final value = asString(d, f.get(DropdownMenuEntryFields.value, 'value'));
  final label = asString(d, f.get(DropdownMenuEntryFields.label, 'label'));
  if (value == null || label == null) return null;
  return DropdownMenuEntry<String>(
    value: value,
    label: label,
    leadingIcon: decodeIconWidget(
      d,
      f.get(DropdownMenuEntryFields.leadingIcon, 'leadingIcon'),
    ),
    trailingIcon: decodeIconWidget(
      d,
      f.get(DropdownMenuEntryFields.trailingIcon, 'trailingIcon'),
    ),
    enabled:
        asBool(d, f.get(DropdownMenuEntryFields.enabled, 'enabled')) ?? true,
    style: decodeButtonStyle(d, f.get(DropdownMenuEntryFields.style, 'style')),
  );
}

// ── Flutter classes of constants ───────────────────────────────────────────

/// A font weight.
FontWeight? decodeFontWeight(Decoding d, Object? v) =>
    switch (nameFontWeight(d, v)) {
      'w100' => FontWeight.w100,
      'w200' => FontWeight.w200,
      'w300' => FontWeight.w300,
      'w400' => FontWeight.w400,
      'w500' => FontWeight.w500,
      'w600' => FontWeight.w600,
      'w700' => FontWeight.w700,
      'w800' => FontWeight.w800,
      'w900' => FontWeight.w900,
      _ => null,
    };

/// Scroll physics; `platform` is what Flutter's default scroll behaviour
/// picks for the platform.
ScrollPhysics? decodeScrollPhysics(Decoding d, Object? v) =>
    switch (nameScrollPhysics(d, v)) {
      'platform' => switch (defaultTargetPlatform) {
        TargetPlatform.iOS || TargetPlatform.macOS =>
          const BouncingScrollPhysics(parent: RangeMaintainingScrollPhysics()),
        _ => const ClampingScrollPhysics(
          parent: RangeMaintainingScrollPhysics(),
        ),
      },
      'bouncing' => const BouncingScrollPhysics(),
      'clamping' => const ClampingScrollPhysics(),
      'never' => const NeverScrollableScrollPhysics(),
      'always' => const AlwaysScrollableScrollPhysics(),
      'page' => const PageScrollPhysics(),
      _ => null,
    };

/// A text decoration.
TextDecoration? decodeTextDecoration(Decoding d, Object? v) =>
    switch (nameTextDecoration(d, v)) {
      'none' => TextDecoration.none,
      'underline' => TextDecoration.underline,
      'overline' => TextDecoration.overline,
      'lineThrough' => TextDecoration.lineThrough,
      _ => null,
    };

/// A keyboard type.
TextInputType? decodeTextInputType(Decoding d, Object? v) =>
    switch (nameTextInputType(d, v)) {
      'text' => TextInputType.text,
      'multiline' => TextInputType.multiline,
      'number' => TextInputType.number,
      'decimal' => const TextInputType.numberWithOptions(decimal: true),
      'signedNumber' => const TextInputType.numberWithOptions(signed: true),
      'phone' => TextInputType.phone,
      'datetime' => TextInputType.datetime,
      'emailAddress' => TextInputType.emailAddress,
      'url' => TextInputType.url,
      'visiblePassword' => TextInputType.visiblePassword,
      'name' => TextInputType.name,
      'streetAddress' => TextInputType.streetAddress,
      'none' => TextInputType.none,
      _ => null,
    };

/// A visual density.
VisualDensity? decodeVisualDensity(Decoding d, Object? v) =>
    switch (nameVisualDensity(d, v)) {
      'standard' => VisualDensity.standard,
      'comfortable' => VisualDensity.comfortable,
      'compact' => VisualDensity.compact,
      'adaptivePlatformDensity' => VisualDensity.adaptivePlatformDensity,
      _ => null,
    };

/// Where a floating action button sits.
FloatingActionButtonLocation? decodeFloatingActionButtonLocation(
  Decoding d,
  Object? v,
) => switch (nameFloatingActionButtonLocation(d, v)) {
  'startTop' => FloatingActionButtonLocation.startTop,
  'miniStartTop' => FloatingActionButtonLocation.miniStartTop,
  'centerTop' => FloatingActionButtonLocation.centerTop,
  'miniCenterTop' => FloatingActionButtonLocation.miniCenterTop,
  'endTop' => FloatingActionButtonLocation.endTop,
  'miniEndTop' => FloatingActionButtonLocation.miniEndTop,
  'startFloat' => FloatingActionButtonLocation.startFloat,
  'miniStartFloat' => FloatingActionButtonLocation.miniStartFloat,
  'centerFloat' => FloatingActionButtonLocation.centerFloat,
  'miniCenterFloat' => FloatingActionButtonLocation.miniCenterFloat,
  'endFloat' => FloatingActionButtonLocation.endFloat,
  'miniEndFloat' => FloatingActionButtonLocation.miniEndFloat,
  'startDocked' => FloatingActionButtonLocation.startDocked,
  'miniStartDocked' => FloatingActionButtonLocation.miniStartDocked,
  'centerDocked' => FloatingActionButtonLocation.centerDocked,
  'miniCenterDocked' => FloatingActionButtonLocation.miniCenterDocked,
  'endDocked' => FloatingActionButtonLocation.endDocked,
  'miniEndDocked' => FloatingActionButtonLocation.miniEndDocked,
  'endContained' => FloatingActionButtonLocation.endContained,
  _ => null,
};

/// How a floating label is aligned.
FloatingLabelAlignment? decodeFloatingLabelAlignment(Decoding d, Object? v) =>
    switch (nameFloatingLabelAlignment(d, v)) {
      'start' => FloatingLabelAlignment.start,
      'center' => FloatingLabelAlignment.center,
      _ => null,
    };

/// The vertical alignment of text in a field.
TextAlignVertical? decodeTextAlignVertical(Decoding d, Object? v) =>
    switch (nameTextAlignVertical(d, v)) {
      'top' => TextAlignVertical.top,
      'center' => TextAlignVertical.center,
      'bottom' => TextAlignVertical.bottom,
      _ => null,
    };
