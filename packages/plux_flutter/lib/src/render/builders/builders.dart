// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The hand-written builders (ADR-0031): the structural primitives,
/// collections with item templates and status slots (WGT-012), and the
/// widgets whose Flutter constructor is chosen by the props that are set.
/// They read props through the same generated decoders and permanent IDs
/// as the generated builders; `render.g.dart` lists why each is written
/// by hand.
library;

import 'package:flutter/cupertino.dart';
import 'package:flutter/gestures.dart' show DragStartBehavior;
import 'package:flutter/material.dart';
import 'package:plux_flutter/src/bundle/fbs/bundle_fbs_generated.dart' as fbs;
import 'package:plux_flutter/src/render/decoders.dart';
import 'package:plux_flutter/src/render/decoding.dart';
import 'package:plux_flutter/src/render/generated/render.g.dart';
import 'package:plux_flutter/src/render/node_context.dart';
import 'package:plux_flutter/src/render/plux_node.dart';
import 'package:plux_flutter/src/render/scope.dart';

/// The hand-written builders, by permanent widget ID.
const Map<int, NodeBuilder> manualBuilders = {
  WidgetIds.if_: _branch,
  WidgetIds.match: _branch,
  WidgetIds.responsive: _branch,
  WidgetIds.forEach: _forEach,
  WidgetIds.slot: _slot,
  WidgetIds.listView: _listView,
  WidgetIds.gridView: _gridView,
  WidgetIds.pageView: _pageView,
  WidgetIds.sliverList: _sliverList,
  WidgetIds.sliverGrid: _sliverGrid,
  WidgetIds.transform: _transform,
  WidgetIds.image: _image,
  WidgetIds.floatingActionButton: _floatingActionButton,
  WidgetIds.cupertinoSlidingSegmentedControl: _slidingSegmentedControl,
};

// ── Structural primitives ──────────────────────────────────────────────────

/// `If`, `Match` and `Responsive` outside a list of children: the chosen
/// branch, or nothing.
Widget _branch(NodeContext c) {
  final n = c as NodeContextImpl;
  final b = n.branch();
  if (b == null) return const SizedBox.shrink();
  final shown = n.expand(b);
  return shown.isEmpty ? const SizedBox.shrink() : shown.single;
}

/// `ForEach` outside a list of children: its items in a column. In a list
/// of children the items are spliced into the list (NodeContextImpl.expand).
Widget _forEach(NodeContext c) => Column(
  mainAxisSize: MainAxisSize.min,
  children: (c as NodeContextImpl).expand(c.index),
);

/// `Slot` inside a component: the instance's fill of the slot named by
/// `name`, rendered in the instance's scope, or the `fallback` slot.
Widget _slot(NodeContext c) {
  final scope = (c as NodeContextImpl).scope;
  final name = c.decode(SlotProps.name, asString);
  final fills = scope.fills;
  final component = scope.section.component;
  if (name == null || fills == null || component == null) {
    return c.slot(SlotSlots.fallback) ?? const SizedBox.shrink();
  }
  final slots = component.slots ?? const <fbs.ComponentSlot>[];
  var k = -1;
  for (var i = 0; i < slots.length; i++) {
    if (scope.section.string(slots[i].name) == name) k = i;
  }
  List<int>? nodes;
  for (final f in fills.node.slots ?? const <fbs.SlotFill>[]) {
    if (f.id == k) nodes = f.nodes;
  }
  if (nodes == null || nodes.isEmpty) {
    return c.slot(SlotSlots.fallback) ?? const SizedBox.shrink();
  }
  final widgets = [
    for (final i in nodes)
      RenderScopeWidget(
        key: ValueKey(i),
        scope: fills.scope,
        child: PluxNode(i),
      ),
  ];
  return widgets.length == 1
      ? widgets.single
      : Column(mainAxisSize: MainAxisSize.min, children: widgets);
}

// ── Collections (WGT-012) ──────────────────────────────────────────────────

/// What a collection shows: its items, or the slot of its status.
final class _Collection {
  _Collection(
    this.c, {
    required this.items,
    required this.status,
    required this.hasMore,
    required this.onEndReached,
    required this.itemSlot,
    required this.empty,
    required this.loading,
    required this.error,
  });

  final NodeContext c;
  final int items,
      status,
      hasMore,
      onEndReached,
      itemSlot,
      empty,
      loading,
      error;

  late final List<Object?> values = switch (c.prop(items)) {
    final List<Object?> v => v,
    _ => const [],
  };

  /// The status slot shown instead of the items, or null.
  Widget? get replacement {
    final s = nameListStatus(c, c.prop(status)) ?? 'ready';
    return switch (s) {
      'loading' => c.slot(loading),
      'error' => c.slot(error),
      _ when values.isEmpty => c.slot(empty),
      _ => null,
    };
  }

  /// Builds item [i]; the last one, while more items exist, fires
  /// `onEndReached` once when it first appears (pagination from P5).
  Widget item(BuildContext context, int i) {
    final w = c.item(itemSlot, values[i], i);
    final more = c.decode(hasMore, asBool) ?? false;
    if (i != values.length - 1 || !more || !c.handles(onEndReached)) return w;
    return _EndReached(onReached: () => c.fire(onEndReached), child: w);
  }
}

final class _EndReached extends StatefulWidget {
  const _EndReached({required this.onReached, required this.child});

  final VoidCallback onReached;
  final Widget child;

  @override
  State<_EndReached> createState() => _EndReachedState();
}

final class _EndReachedState extends State<_EndReached> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) widget.onReached();
    });
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

SliverGridDelegate _gridDelegate(
  NodeContext c, {
  required int count,
  required int maxExtent,
  required int mainSpacing,
  required int crossSpacing,
  required int aspectRatio,
  required int mainExtent,
}) {
  final main = c.decode(mainSpacing, asDouble) ?? 0;
  final cross = c.decode(crossSpacing, asDouble) ?? 0;
  final ratio = c.decode(aspectRatio, asDouble) ?? 1;
  final extent = c.decode(mainExtent, asDouble);
  final n = c.decode(count, asInt);
  if (n != null) {
    return SliverGridDelegateWithFixedCrossAxisCount(
      crossAxisCount: n,
      mainAxisSpacing: main,
      crossAxisSpacing: cross,
      childAspectRatio: ratio,
      mainAxisExtent: extent,
    );
  }
  return SliverGridDelegateWithMaxCrossAxisExtent(
    maxCrossAxisExtent:
        c.decode(maxExtent, asDouble) ?? c.missing('crossAxisCount'),
    mainAxisSpacing: main,
    crossAxisSpacing: cross,
    childAspectRatio: ratio,
    mainAxisExtent: extent,
  );
}

Widget _listView(NodeContext c) {
  final l = _Collection(
    c,
    items: ListViewProps.items,
    status: ListViewProps.status,
    hasMore: ListViewProps.hasMore,
    onEndReached: ListViewEvents.onEndReached,
    itemSlot: ListViewSlots.item,
    empty: ListViewSlots.empty,
    loading: ListViewSlots.loading,
    error: ListViewSlots.error,
  );
  final other = l.replacement;
  if (other != null) return other;
  return ListView.builder(
    scrollDirection:
        c.decode(ListViewProps.scrollDirection, decodeAxis) ?? Axis.vertical,
    reverse: c.decode(ListViewProps.reverse, asBool) ?? false,
    primary: c.decode(ListViewProps.primary, asBool),
    physics: c.decode(ListViewProps.physics, decodeScrollPhysics),
    shrinkWrap: c.decode(ListViewProps.shrinkWrap, asBool) ?? false,
    padding: c.decode(ListViewProps.padding, decodeEdgeInsets),
    itemExtent: c.decode(ListViewProps.itemExtent, asDouble),
    prototypeItem: c.slot(ListViewSlots.prototypeItem),
    itemCount: l.values.length,
    itemBuilder: l.item,
    addAutomaticKeepAlives:
        c.decode(ListViewProps.addAutomaticKeepAlives, asBool) ?? true,
    addRepaintBoundaries:
        c.decode(ListViewProps.addRepaintBoundaries, asBool) ?? true,
    addSemanticIndexes:
        c.decode(ListViewProps.addSemanticIndexes, asBool) ?? true,
    semanticChildCount: c.decode(ListViewProps.semanticChildCount, asInt),
    dragStartBehavior:
        c.decode(ListViewProps.dragStartBehavior, decodeDragStartBehavior) ??
        DragStartBehavior.start,
    keyboardDismissBehavior: c.decode(
      ListViewProps.keyboardDismissBehavior,
      decodeScrollViewKeyboardDismissBehavior,
    ),
    clipBehavior:
        c.decode(ListViewProps.clipBehavior, decodeClip) ?? Clip.hardEdge,
    hitTestBehavior:
        c.decode(ListViewProps.hitTestBehavior, decodeHitTestBehavior) ??
        HitTestBehavior.opaque,
  );
}

Widget _gridView(NodeContext c) {
  final l = _Collection(
    c,
    items: GridViewProps.items,
    status: GridViewProps.status,
    hasMore: GridViewProps.hasMore,
    onEndReached: GridViewEvents.onEndReached,
    itemSlot: GridViewSlots.item,
    empty: GridViewSlots.empty,
    loading: GridViewSlots.loading,
    error: GridViewSlots.error,
  );
  final other = l.replacement;
  if (other != null) return other;
  return GridView.builder(
    scrollDirection:
        c.decode(GridViewProps.scrollDirection, decodeAxis) ?? Axis.vertical,
    reverse: c.decode(GridViewProps.reverse, asBool) ?? false,
    primary: c.decode(GridViewProps.primary, asBool),
    physics: c.decode(GridViewProps.physics, decodeScrollPhysics),
    shrinkWrap: c.decode(GridViewProps.shrinkWrap, asBool) ?? false,
    padding: c.decode(GridViewProps.padding, decodeEdgeInsets),
    gridDelegate: _gridDelegate(
      c,
      count: GridViewProps.crossAxisCount,
      maxExtent: GridViewProps.maxCrossAxisExtent,
      mainSpacing: GridViewProps.mainAxisSpacing,
      crossSpacing: GridViewProps.crossAxisSpacing,
      aspectRatio: GridViewProps.childAspectRatio,
      mainExtent: GridViewProps.mainAxisExtent,
    ),
    itemCount: l.values.length,
    itemBuilder: l.item,
    addAutomaticKeepAlives:
        c.decode(GridViewProps.addAutomaticKeepAlives, asBool) ?? true,
    addRepaintBoundaries:
        c.decode(GridViewProps.addRepaintBoundaries, asBool) ?? true,
    addSemanticIndexes:
        c.decode(GridViewProps.addSemanticIndexes, asBool) ?? true,
    semanticChildCount: c.decode(GridViewProps.semanticChildCount, asInt),
    dragStartBehavior:
        c.decode(GridViewProps.dragStartBehavior, decodeDragStartBehavior) ??
        DragStartBehavior.start,
    keyboardDismissBehavior: c.decode(
      GridViewProps.keyboardDismissBehavior,
      decodeScrollViewKeyboardDismissBehavior,
    ),
    clipBehavior:
        c.decode(GridViewProps.clipBehavior, decodeClip) ?? Clip.hardEdge,
    hitTestBehavior:
        c.decode(GridViewProps.hitTestBehavior, decodeHitTestBehavior) ??
        HitTestBehavior.opaque,
  );
}

Widget _pageView(NodeContext c) {
  final l = _Collection(
    c,
    items: PageViewProps.items,
    status: PageViewProps.status,
    hasMore: PageViewProps.hasMore,
    onEndReached: PageViewEvents.onEndReached,
    itemSlot: PageViewSlots.item,
    empty: PageViewSlots.empty,
    loading: PageViewSlots.loading,
    error: PageViewSlots.error,
  );
  final other = l.replacement;
  if (other != null) return other;
  return PageView.builder(
    scrollDirection:
        c.decode(PageViewProps.scrollDirection, decodeAxis) ?? Axis.horizontal,
    reverse: c.decode(PageViewProps.reverse, asBool) ?? false,
    physics: c.decode(PageViewProps.physics, decodeScrollPhysics),
    pageSnapping: c.decode(PageViewProps.pageSnapping, asBool) ?? true,
    onPageChanged: c.handles(PageViewEvents.onPageChanged)
        ? (i) => c.fire(PageViewEvents.onPageChanged, i)
        : null,
    itemCount: l.values.length,
    itemBuilder: l.item,
    dragStartBehavior:
        c.decode(PageViewProps.dragStartBehavior, decodeDragStartBehavior) ??
        DragStartBehavior.start,
    allowImplicitScrolling:
        c.decode(PageViewProps.allowImplicitScrolling, asBool) ?? false,
    clipBehavior:
        c.decode(PageViewProps.clipBehavior, decodeClip) ?? Clip.hardEdge,
    hitTestBehavior:
        c.decode(PageViewProps.hitTestBehavior, decodeHitTestBehavior) ??
        HitTestBehavior.opaque,
    padEnds: c.decode(PageViewProps.padEnds, asBool) ?? true,
  );
}

Widget _sliverList(NodeContext c) {
  final l = _Collection(
    c,
    items: SliverListProps.items,
    status: SliverListProps.status,
    hasMore: SliverListProps.hasMore,
    onEndReached: SliverListEvents.onEndReached,
    itemSlot: SliverListSlots.item,
    empty: SliverListSlots.empty,
    loading: SliverListSlots.loading,
    error: SliverListSlots.error,
  );
  final other = l.replacement;
  if (other != null) return SliverToBoxAdapter(child: other);
  return SliverList.builder(
    itemCount: l.values.length,
    itemBuilder: l.item,
    addAutomaticKeepAlives:
        c.decode(SliverListProps.addAutomaticKeepAlives, asBool) ?? true,
    addRepaintBoundaries:
        c.decode(SliverListProps.addRepaintBoundaries, asBool) ?? true,
    addSemanticIndexes:
        c.decode(SliverListProps.addSemanticIndexes, asBool) ?? true,
    semanticIndexOffset:
        c.decode(SliverListProps.semanticIndexOffset, asInt) ?? 0,
  );
}

Widget _sliverGrid(NodeContext c) {
  final l = _Collection(
    c,
    items: SliverGridProps.items,
    status: SliverGridProps.status,
    hasMore: SliverGridProps.hasMore,
    onEndReached: SliverGridEvents.onEndReached,
    itemSlot: SliverGridSlots.item,
    empty: SliverGridSlots.empty,
    loading: SliverGridSlots.loading,
    error: SliverGridSlots.error,
  );
  final other = l.replacement;
  if (other != null) return SliverToBoxAdapter(child: other);
  return SliverGrid.builder(
    gridDelegate: _gridDelegate(
      c,
      count: SliverGridProps.crossAxisCount,
      maxExtent: SliverGridProps.maxCrossAxisExtent,
      mainSpacing: SliverGridProps.mainAxisSpacing,
      crossSpacing: SliverGridProps.crossAxisSpacing,
      aspectRatio: SliverGridProps.childAspectRatio,
      mainExtent: SliverGridProps.mainAxisExtent,
    ),
    itemCount: l.values.length,
    itemBuilder: l.item,
    addAutomaticKeepAlives:
        c.decode(SliverGridProps.addAutomaticKeepAlives, asBool) ?? true,
    addRepaintBoundaries:
        c.decode(SliverGridProps.addRepaintBoundaries, asBool) ?? true,
    addSemanticIndexes:
        c.decode(SliverGridProps.addSemanticIndexes, asBool) ?? true,
    semanticIndexOffset:
        c.decode(SliverGridProps.semanticIndexOffset, asInt) ?? 0,
  );
}

// ── Widgets chosen by their props ──────────────────────────────────────────

/// `Transform` composes the transforms whose props are set: translation,
/// rotation, scaling and flipping, outermost first.
Widget _transform(NodeContext c) {
  final origin = c.decode(TransformProps.origin, decodeOffset);
  final alignment =
      c.decode(TransformProps.alignment, decodeAlignment) ?? Alignment.center;
  final hitTests = c.decode(TransformProps.transformHitTests, asBool) ?? true;
  final quality = c.decode(TransformProps.filterQuality, decodeFilterQuality);
  var out = c.slot(TransformSlots.child);
  final flipX = c.decode(TransformProps.flipX, asBool) ?? false;
  final flipY = c.decode(TransformProps.flipY, asBool) ?? false;
  if (flipX || flipY) {
    out = Transform.flip(
      flipX: flipX,
      flipY: flipY,
      origin: origin,
      transformHitTests: hitTests,
      filterQuality: quality,
      child: out,
    );
  }
  final scale = c.decode(TransformProps.scale, asDouble);
  final scaleX = c.decode(TransformProps.scaleX, asDouble);
  final scaleY = c.decode(TransformProps.scaleY, asDouble);
  if (scale != null || scaleX != null || scaleY != null) {
    out = Transform.scale(
      scale: scale,
      scaleX: scaleX,
      scaleY: scaleY,
      origin: origin,
      alignment: alignment,
      transformHitTests: hitTests,
      filterQuality: quality,
      child: out,
    );
  }
  final angle = c.decode(TransformProps.angle, asDouble);
  if (angle != null) {
    out = Transform.rotate(
      angle: angle,
      origin: origin,
      alignment: alignment,
      transformHitTests: hitTests,
      filterQuality: quality,
      child: out,
    );
  }
  final offset = c.decode(TransformProps.offset, decodeOffset);
  if (offset != null) {
    out = Transform.translate(
      offset: offset,
      transformHitTests: hitTests,
      filterQuality: quality,
      child: out,
    );
  }
  return out ?? const SizedBox.shrink();
}

/// `Image` from an asset of the release or a URL, decoded at the size the
/// document gives (`cacheWidth`, `cacheHeight`), with its `loading` and
/// `error` slots.
Widget _image(NodeContext c) {
  var provider =
      c.decode(ImageProps.source, decodeImageSource) ?? c.missing('source');
  final cacheWidth = c.decode(ImageProps.cacheWidth, asInt);
  final cacheHeight = c.decode(ImageProps.cacheHeight, asInt);
  provider = ResizeImage.resizeIfNeeded(cacheWidth, cacheHeight, provider);
  final loading = c.hasSlot(ImageSlots.loading);
  final width = c.decode(ImageProps.width, asDouble);
  final height = c.decode(ImageProps.height, asDouble);
  return Image(
    image: provider,
    loadingBuilder: loading
        ? (context, child, progress) =>
              progress == null ? child : c.slot(ImageSlots.loading) ?? child
        : null,
    // A failed load is contained: reported, and shown as the error slot or
    // an empty box of the image's size (RT-020).
    errorBuilder: (context, e, stack) {
      c.imageFailed(e);
      return c.slot(ImageSlots.error) ?? SizedBox(width: width, height: height);
    },
    semanticLabel: c.decode(ImageProps.semanticLabel, asString),
    excludeFromSemantics:
        c.decode(ImageProps.excludeFromSemantics, asBool) ?? false,
    width: width,
    height: height,
    color: c.decode(ImageProps.color, asColor),
    colorBlendMode: c.decode(ImageProps.colorBlendMode, decodeBlendMode),
    fit: c.decode(ImageProps.fit, decodeBoxFit),
    alignment:
        c.decode(ImageProps.alignment, decodeAlignment) ?? Alignment.center,
    repeat:
        c.decode(ImageProps.repeat, decodeImageRepeat) ?? ImageRepeat.noRepeat,
    matchTextDirection:
        c.decode(ImageProps.matchTextDirection, asBool) ?? false,
    gaplessPlayback: c.decode(ImageProps.gaplessPlayback, asBool) ?? false,
    isAntiAlias: c.decode(ImageProps.isAntiAlias, asBool) ?? false,
    filterQuality:
        c.decode(ImageProps.filterQuality, decodeFilterQuality) ??
        FilterQuality.medium,
  );
}

/// `FloatingActionButton`: extended when its `label` slot is filled.
Widget _floatingActionButton(NodeContext c) {
  final onPressed = c.handles(FloatingActionButtonEvents.onPressed)
      ? () => c.fire(FloatingActionButtonEvents.onPressed)
      : null;
  final label = c.slot(FloatingActionButtonSlots.label);
  if (label != null) {
    return FloatingActionButton.extended(
      label: label,
      icon: c.slot(FloatingActionButtonSlots.icon),
      onPressed: onPressed,
      tooltip: c.decode(FloatingActionButtonProps.tooltip, asString),
      foregroundColor: c.decode(
        FloatingActionButtonProps.foregroundColor,
        asColor,
      ),
      backgroundColor: c.decode(
        FloatingActionButtonProps.backgroundColor,
        asColor,
      ),
      focusColor: c.decode(FloatingActionButtonProps.focusColor, asColor),
      hoverColor: c.decode(FloatingActionButtonProps.hoverColor, asColor),
      splashColor: c.decode(FloatingActionButtonProps.splashColor, asColor),
      elevation: c.decode(FloatingActionButtonProps.elevation, asDouble),
      focusElevation: c.decode(
        FloatingActionButtonProps.focusElevation,
        asDouble,
      ),
      hoverElevation: c.decode(
        FloatingActionButtonProps.hoverElevation,
        asDouble,
      ),
      highlightElevation: c.decode(
        FloatingActionButtonProps.highlightElevation,
        asDouble,
      ),
      disabledElevation: c.decode(
        FloatingActionButtonProps.disabledElevation,
        asDouble,
      ),
      shape: c.decode(FloatingActionButtonProps.shape, decodeShapeBorder),
      isExtended:
          c.decode(FloatingActionButtonProps.isExtended, asBool) ?? true,
      materialTapTargetSize: c.decode(
        FloatingActionButtonProps.materialTapTargetSize,
        decodeMaterialTapTargetSize,
      ),
      clipBehavior:
          c.decode(FloatingActionButtonProps.clipBehavior, decodeClip) ??
          Clip.none,
      autofocus: c.decode(FloatingActionButtonProps.autofocus, asBool) ?? false,
      extendedIconLabelSpacing: c.decode(
        FloatingActionButtonProps.extendedIconLabelSpacing,
        asDouble,
      ),
      extendedPadding: c.decode(
        FloatingActionButtonProps.extendedPadding,
        decodeEdgeInsets,
      ),
      extendedTextStyle: c.decode(
        FloatingActionButtonProps.extendedTextStyle,
        decodeTextStyle,
      ),
      enableFeedback: c.decode(
        FloatingActionButtonProps.enableFeedback,
        asBool,
      ),
    );
  }
  return FloatingActionButton(
    onPressed: onPressed,
    tooltip: c.decode(FloatingActionButtonProps.tooltip, asString),
    foregroundColor: c.decode(
      FloatingActionButtonProps.foregroundColor,
      asColor,
    ),
    backgroundColor: c.decode(
      FloatingActionButtonProps.backgroundColor,
      asColor,
    ),
    focusColor: c.decode(FloatingActionButtonProps.focusColor, asColor),
    hoverColor: c.decode(FloatingActionButtonProps.hoverColor, asColor),
    splashColor: c.decode(FloatingActionButtonProps.splashColor, asColor),
    elevation: c.decode(FloatingActionButtonProps.elevation, asDouble),
    focusElevation: c.decode(
      FloatingActionButtonProps.focusElevation,
      asDouble,
    ),
    hoverElevation: c.decode(
      FloatingActionButtonProps.hoverElevation,
      asDouble,
    ),
    highlightElevation: c.decode(
      FloatingActionButtonProps.highlightElevation,
      asDouble,
    ),
    disabledElevation: c.decode(
      FloatingActionButtonProps.disabledElevation,
      asDouble,
    ),
    mini: c.decode(FloatingActionButtonProps.mini, asBool) ?? false,
    shape: c.decode(FloatingActionButtonProps.shape, decodeShapeBorder),
    clipBehavior:
        c.decode(FloatingActionButtonProps.clipBehavior, decodeClip) ??
        Clip.none,
    autofocus: c.decode(FloatingActionButtonProps.autofocus, asBool) ?? false,
    materialTapTargetSize: c.decode(
      FloatingActionButtonProps.materialTapTargetSize,
      decodeMaterialTapTargetSize,
    ),
    isExtended: c.decode(FloatingActionButtonProps.isExtended, asBool) ?? false,
    enableFeedback: c.decode(FloatingActionButtonProps.enableFeedback, asBool),
    child: c.slot(FloatingActionButtonSlots.child),
  );
}

/// Flutter's defaults for the colors and padding of a sliding segmented
/// control, which it keeps private: read from a default instance.
final _segmentDefaults = CupertinoSlidingSegmentedControl<String>(
  children: const {'a': SizedBox(), 'b': SizedBox()},
  onValueChanged: (_) {},
);

/// `CupertinoSlidingSegmentedControl`: its segments are the `segments`
/// slot's nodes keyed by `values`, in order.
Widget _slidingSegmentedControl(NodeContext c) {
  final values =
      c.decode(CupertinoSlidingSegmentedControlProps.values, asStrings) ??
      c.missing('values');
  final segments = c.slotList(CupertinoSlidingSegmentedControlSlots.segments);
  final children = <String, Widget>{
    for (var i = 0; i < values.length && i < segments.length; i++)
      values[i]: segments[i],
  };
  return c.controlled<String?>(
    CupertinoSlidingSegmentedControlEvents.onValueChanged,
    c.decode(CupertinoSlidingSegmentedControlProps.groupValue, asString),
    (value, onChanged) => CupertinoSlidingSegmentedControl<String>(
      children: children,
      onValueChanged: (v) => onChanged?.call(v),
      disabledChildren:
          c.decode(
            CupertinoSlidingSegmentedControlProps.disabledChildren,
            asStringSet,
          ) ??
          (onChanged == null ? children.keys.toSet() : const {}),
      groupValue: value,
      thumbColor:
          c.decode(CupertinoSlidingSegmentedControlProps.thumbColor, asColor) ??
          _segmentDefaults.thumbColor,
      padding:
          c.decode(
            CupertinoSlidingSegmentedControlProps.padding,
            decodeEdgeInsets,
          ) ??
          _segmentDefaults.padding,
      backgroundColor:
          c.decode(
            CupertinoSlidingSegmentedControlProps.backgroundColor,
            asColor,
          ) ??
          CupertinoColors.tertiarySystemFill,
      proportionalWidth:
          c.decode(
            CupertinoSlidingSegmentedControlProps.proportionalWidth,
            asBool,
          ) ??
          false,
      isMomentary:
          c.decode(CupertinoSlidingSegmentedControlProps.isMomentary, asBool) ??
          false,
    ),
  );
}
