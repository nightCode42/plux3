// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_rive/src/inputs.dart';
import 'package:rive/rive.dart' as rive;

/// The Rive native slot (ANI-005, WGT-021).
abstract final class PluxRive {
  /// The slot's type name in the app's native catalogue.
  static const String slotName = 'PluxRive';

  /// The slot to pass in `PluxConfig.nativeSlots`.
  static Map<String, PluxNativeSlot> get slots => {
    slotName: PluxNativeSlot(_build),
  };

  static Widget _build(BuildContext context, PluxSlot slot) {
    final url = slot['url'];
    if (url is! String || !url.startsWith('https://')) {
      return const SizedBox.shrink();
    }
    Map<String, Object?> map(String name) {
      final v = slot[name];
      return v is Map ? v.cast<String, Object?>() : const {};
    }

    return PluxRiveView(
      // The Rive renderer draws the animation; the loader reads the URL
      // once and caches the file for the widget's life.
      loader: () =>
          rive.FileLoader.fromUrl(url, riveFactory: rive.Factory.rive),
      cacheKey: url,
      artboard: slot['artboard'] as String?,
      stateMachine: slot['stateMachine'] as String?,
      inputs: map('inputs'),
      triggers: map('triggers'),
      fit: rive.Fit.values.asNameMap()[slot['fit']] ?? rive.Fit.contain,
      ignoreReduceMotion: slot['reduceMotion'] == 'ignore',
      onLoaded: () => slot.emit('onLoaded'),
      onFailed: () => slot.emit('onFailed'),
    );
  }
}

/// A Rive file with a state machine whose inputs follow the values the page
/// binds (ANI-005).
final class PluxRiveView extends StatefulWidget {
  /// Creates the view of the file [loader] makes; [cacheKey] identifies the
  /// file, so a rebuild with the same key keeps the loaded file.
  const PluxRiveView({
    required this.loader,
    required this.cacheKey,
    this.artboard,
    this.stateMachine,
    this.inputs = const {},
    this.triggers = const {},
    this.fit = rive.Fit.contain,
    this.onLoaded,
    this.onFailed,
    this.ignoreReduceMotion = false,
    super.key,
  });

  /// Makes the loader of the Rive file.
  final rive.FileLoader Function() loader;

  /// Identifies the file.
  final String cacheKey;

  /// The artboard to show, or the file's default.
  final String? artboard;

  /// The state machine to run, or the artboard's default.
  final String? stateMachine;

  /// State machine inputs to keep equal to values of Plux state: numbers
  /// and booleans by input name.
  final Map<String, Object?> inputs;

  /// Triggers to fire whenever their value changes.
  final Map<String, Object?> triggers;

  /// How the animation fits its box.
  final rive.Fit fit;

  /// Called when the file is loaded.
  final VoidCallback? onLoaded;

  /// Called when the file cannot be loaded.
  final VoidCallback? onFailed;

  /// Whether the animation keeps playing when the platform asks to reduce
  /// motion (ANI-007); otherwise it freezes.
  final bool ignoreReduceMotion;

  @override
  State<PluxRiveView> createState() => _PluxRiveViewState();
}

final class _PluxRiveViewState extends State<PluxRiveView> {
  late rive.FileLoader _loader = widget.loader();
  PluxRiveInputs? _bound;
  rive.RiveWidgetController? _controller;

  @override
  void didUpdateWidget(PluxRiveView old) {
    super.didUpdateWidget(old);
    if (old.cacheKey != widget.cacheKey) {
      _bound = null;
      _controller = null;
      _loader = widget.loader();
    }
    _bound?.apply(widget.inputs, widget.triggers);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _syncMotion();
  }

  /// Freezes the animation under reduce motion unless it says to ignore it
  /// (ANI-007).
  void _syncMotion() {
    final reduce = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    _controller?.active = !(reduce && !widget.ignoreReduceMotion);
  }

  void _loaded(rive.RiveLoaded state) {
    _controller = state.controller;
    _bound = PluxRiveInputs.of(state.controller.stateMachine)
      ..apply(widget.inputs, widget.triggers);
    _syncMotion();
    widget.onLoaded?.call();
  }

  @override
  Widget build(BuildContext context) => rive.RiveWidgetBuilder(
    fileLoader: _loader,
    artboardSelector: widget.artboard == null
        ? const rive.ArtboardDefault()
        : rive.ArtboardSelector.byName(widget.artboard!),
    stateMachineSelector: widget.stateMachine == null
        ? const rive.StateMachineDefault()
        : rive.StateMachineSelector.byName(widget.stateMachine!),
    onLoaded: _loaded,
    onFailed: (_, _) => widget.onFailed?.call(),
    builder: (context, state) => switch (state) {
      rive.RiveLoaded(:final controller) => rive.RiveWidget(
        controller: controller,
        fit: widget.fit,
      ),
      _ => const SizedBox.shrink(),
    },
  );
}
