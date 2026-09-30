// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Frame timings as the engine reports them, and waiting for the frames
/// the benchmark cares about.
library;

import 'dart:async';
import 'dart:ui' show FramePhase, FrameTiming;

import 'package:flutter/scheduler.dart';

/// When the frame's build started on the UI thread, in microseconds of
/// the clock `Timeline.now` reads.
int frameStart(FrameTiming t) =>
    t.timestampInMicroseconds(FramePhase.buildStart);

/// When the frame's build ended: when it was handed to the raster thread,
/// just before its post-frame callbacks run.
int frameEnd(FrameTiming t) =>
    t.timestampInMicroseconds(FramePhase.buildFinish);

/// Collects every [FrameTiming] the engine reports while it is attached.
///
/// In profile and release builds the engine reports timings in batches,
/// so a frame's timing arrives some time after the frame; [frame] waits
/// for it.
final class FrameRecorder {
  /// Creates a recorder; [attach] starts it.
  FrameRecorder();

  final List<FrameTiming> _frames = [];
  final List<(bool Function(FrameTiming), Completer<FrameTiming>)> _waiting =
      [];
  SchedulerBinding? _binding;

  /// Starts receiving the timings of [binding].
  void attach(SchedulerBinding binding) {
    _binding = binding;
    binding.addTimingsCallback(add);
  }

  /// Stops receiving timings.
  void detach() {
    _binding?.removeTimingsCallback(add);
    _binding = null;
  }

  /// Every timing received so far, oldest first.
  List<FrameTiming> get frames => List.unmodifiable(_frames);

  /// Adds reported timings; the engine calls it through the binding.
  void add(List<FrameTiming> timings) {
    _frames.addAll(timings);
    for (final t in timings) {
      _waiting.removeWhere((w) {
        if (!w.$1(t)) return false;
        w.$2.complete(t);
        return true;
      });
    }
  }

  /// The timing of the last frame whose build ended by [micros] (on the
  /// clock of `Timeline.now`), once a later frame's timing has arrived —
  /// the caller draws one. Taken in a post-frame callback, [micros] names
  /// the frame that callback followed.
  ///
  /// Frames are matched by time rather than by the frame's time stamp,
  /// which embedders set differently (the vsync start or its target).
  Future<FrameTiming> frameBefore(
    int micros, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    await _after(micros, timeout);
    FrameTiming? found;
    for (final t in _frames) {
      if (frameEnd(t) <= micros &&
          (found == null || frameEnd(t) > frameEnd(found))) {
        found = t;
      }
    }
    if (found == null) {
      throw StateError('no frame ended by $micros');
    }
    return found;
  }

  /// The timings of the frames whose build started in [from, to], once a
  /// later frame's timing has arrived.
  Future<List<FrameTiming>> between(
    int from,
    int to, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    await _after(to, timeout);
    return [
      for (final t in _frames)
        if (frameStart(t) >= from && frameStart(t) <= to) t,
    ];
  }

  /// Completes once a frame that started after [micros] was reported.
  Future<void> _after(int micros, Duration timeout) {
    bool match(FrameTiming t) => frameStart(t) > micros;
    if (_frames.any(match)) return Future.value();
    final c = Completer<FrameTiming>();
    _waiting.add((match, c));
    return c.future.timeout(
      timeout,
      onTimeout: () {
        _waiting.removeWhere((w) => identical(w.$2, c));
        throw TimeoutException(
          'no frame after $micros was reported '
          '(${_frames.length} timings, the last started at '
          '${_frames.isEmpty ? '-' : frameStart(_frames.last)})',
        );
      },
    );
  }
}
