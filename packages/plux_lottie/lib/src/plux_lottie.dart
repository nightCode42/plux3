// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:lottie/lottie.dart';
import 'package:plux_flutter/plux_flutter.dart';

/// The Lottie native slot (ANI-005, WGT-021).
abstract final class PluxLottie {
  /// The slot's type name in the app's native catalogue.
  static const String slotName = 'PluxLottie';

  /// The slot to pass in `PluxConfig.nativeSlots`.
  static Map<String, PluxNativeSlot> get slots => {
    slotName: PluxNativeSlot(_build),
  };

  static Widget _build(BuildContext context, PluxSlot slot) {
    double? number(String name) {
      final v = slot[name];
      return v is num ? v.toDouble() : null;
    }

    bool flag(String name, {required bool otherwise}) {
      final v = slot[name];
      return v is bool ? v : otherwise;
    }

    final url = slot['url'];
    if (url is! String || !url.startsWith('https://')) {
      // Only https addresses play: an animation is downloaded code-free data,
      // but never over a connection that anyone can change.
      return const SizedBox.shrink();
    }
    return PluxLottieView(
      provider: NetworkLottie(url),
      animate: flag('animate', otherwise: true),
      repeat: flag('repeat', otherwise: true),
      reverse: flag('reverse', otherwise: false),
      progress: number('progress'),
      fit: _fit(slot['fit']),
      width: number('width'),
      height: number('height'),
      reduceMotion: switch (slot['reduceMotion']) {
        'shorten' => PluxReduceMotion.shorten,
        'ignore' => PluxReduceMotion.ignore,
        _ => PluxReduceMotion.skip,
      },
      onCompleted: () => slot.emit('onCompleted'),
    );
  }

  static BoxFit _fit(Object? name) =>
      BoxFit.values.asNameMap()[name] ?? BoxFit.contain;
}

/// What an animation does when the platform asks to reduce motion
/// (ANI-007).
enum PluxReduceMotion {
  /// Show the last frame and do not play.
  skip,

  /// Play four times as fast.
  shorten,

  /// Play as declared.
  ignore,
}

/// A Lottie or dotLottie animation controlled by props (ANI-005): it plays,
/// repeats and reverses, or shows the frame [progress] names, which Plux
/// state can bind.
final class PluxLottieView extends StatefulWidget {
  /// Creates the view of the animation [provider] loads.
  const PluxLottieView({
    required this.provider,
    this.animate = true,
    this.repeat = true,
    this.reverse = false,
    this.progress,
    this.fit = BoxFit.contain,
    this.width,
    this.height,
    this.reduceMotion = PluxReduceMotion.skip,
    this.onCompleted,
    super.key,
  });

  /// Loads the composition: a [NetworkLottie] for a URL, a [MemoryLottie]
  /// for bytes. Both read `.json` and `.lottie` (dotLottie) files.
  final LottieProvider provider;

  /// Whether it plays.
  final bool animate;

  /// Whether it plays again when it ends.
  final bool repeat;

  /// Whether every second play runs backwards.
  final bool reverse;

  /// The frame to show, 0 to 1; when set, the animation does not play.
  final double? progress;

  /// How it fits its box.
  final BoxFit fit;

  /// The width, or null for the animation's own.
  final double? width;

  /// The height, or null for the animation's own.
  final double? height;

  /// What it does under reduce motion.
  final PluxReduceMotion reduceMotion;

  /// Called when an animation that does not repeat ends.
  final VoidCallback? onCompleted;

  @override
  State<PluxLottieView> createState() => _PluxLottieViewState();
}

final class _PluxLottieViewState extends State<PluxLottieView>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(vsync: this)
    ..addStatusListener(_status);
  Duration? _duration;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _apply();
  }

  @override
  void didUpdateWidget(PluxLottieView old) {
    super.didUpdateWidget(old);
    if (old.animate != widget.animate ||
        old.repeat != widget.repeat ||
        old.reverse != widget.reverse ||
        old.progress != widget.progress ||
        old.reduceMotion != widget.reduceMotion) {
      _apply();
    }
  }

  void _status(AnimationStatus status) {
    if (status == AnimationStatus.completed && !widget.repeat) {
      widget.onCompleted?.call();
    }
  }

  /// Puts the controller in the state the props and the platform's
  /// reduce-motion setting ask for.
  void _apply() {
    final duration = _duration;
    if (duration == null) return;
    final reduce = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    final progress = widget.progress;
    if (progress != null) {
      _controller
        ..stop()
        ..value = progress.clamp(0.0, 1.0);
      return;
    }
    if (!widget.animate) {
      _controller
        ..stop()
        ..value = 0;
      return;
    }
    if (reduce && widget.reduceMotion == PluxReduceMotion.skip) {
      _controller
        ..stop()
        ..value = 1;
      return;
    }
    _controller.duration =
        reduce && widget.reduceMotion == PluxReduceMotion.shorten
        ? duration ~/ 4
        : duration;
    if (widget.repeat) {
      _controller.repeat(reverse: widget.reverse);
    } else {
      _controller.forward(from: 0);
    }
  }

  void _loaded(LottieComposition composition) {
    _duration = composition.duration;
    _apply();
  }

  @override
  Widget build(BuildContext context) => LottieBuilder(
    lottie: widget.provider,
    controller: _controller,
    onLoaded: _loaded,
    fit: widget.fit,
    width: widget.width,
    height: widget.height,
    errorBuilder: (_, _, _) => const SizedBox.shrink(),
  );
}
