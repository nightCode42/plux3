// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lottie/lottie.dart';
import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_lottie/plux_lottie.dart';

/// A two-second animation with one empty layer.
final Uint8List _json = Uint8List.fromList(
  utf8.encode(
    '{"v":"5.5.2","fr":30,"ip":0,"op":60,"w":100,"h":100,"nm":"t","ddd":0,'
    '"assets":[],"layers":[]}',
  ),
);

/// A dotLottie archive: a stored (uncompressed) ZIP with the manifest and
/// the animation.
Uint8List _dotLottie() {
  final files = {
    'manifest.json': utf8.encode('{"version":"1","animations":[{"id":"a"}]}'),
    'animations/a.json': _json,
  };
  final out = BytesBuilder();
  final central = BytesBuilder();
  var offset = 0;
  void u16(BytesBuilder b, int v) => b.add([v & 0xff, (v >> 8) & 0xff]);
  void u32(BytesBuilder b, int v) =>
      b.add([v & 0xff, (v >> 8) & 0xff, (v >> 16) & 0xff, (v >> 24) & 0xff]);
  for (final e in files.entries) {
    final name = utf8.encode(e.key);
    final crc = _crc32(e.value);
    final local = BytesBuilder();
    u32(local, 0x04034b50);
    u16(local, 20);
    u16(local, 0);
    u16(local, 0);
    u32(local, 0);
    u32(local, crc);
    u32(local, e.value.length);
    u32(local, e.value.length);
    u16(local, name.length);
    u16(local, 0);
    local
      ..add(name)
      ..add(e.value);
    u32(central, 0x02014b50);
    u16(central, 20);
    u16(central, 20);
    u16(central, 0);
    u16(central, 0);
    u32(central, 0);
    u32(central, crc);
    u32(central, e.value.length);
    u32(central, e.value.length);
    u16(central, name.length);
    u16(central, 0);
    u16(central, 0);
    u16(central, 0);
    u16(central, 0);
    u32(central, 0);
    u32(central, offset);
    central.add(name);
    offset += local.length;
    out.add(local.toBytes());
  }
  final directory = central.toBytes();
  out.add(directory);
  u32(out, 0x06054b50);
  u16(out, 0);
  u16(out, 0);
  u16(out, files.length);
  u16(out, files.length);
  u32(out, directory.length);
  u32(out, offset);
  u16(out, 0);
  return out.toBytes();
}

int _crc32(List<int> data) {
  var crc = 0xffffffff;
  for (final b in data) {
    crc ^= b;
    for (var i = 0; i < 8; i++) {
      crc = (crc & 1) != 0 ? (crc >> 1) ^ 0xedb88320 : crc >> 1;
    }
  }
  return crc ^ 0xffffffff;
}

final class _Slot implements PluxSlot {
  _Slot(this.props);

  final Map<String, Object?> props;
  final List<String> events = [];

  @override
  Object? operator [](String name) => props[name];

  @override
  void emit(String name, [Object? payload]) => events.add(name);
}

/// Lottie and dotLottie (ANI-005, WGT-021): the slot, props that play, loop
/// or freeze a frame, dotLottie archives, reduce motion and completion.
void main() {
  Future<void> show(
    WidgetTester tester,
    Widget child, {
    bool reduce = false,
  }) async {
    await tester.pumpWidget(
      MaterialApp(
        home: MediaQuery(
          data: MediaQueryData(disableAnimations: reduce),
          child: child,
        ),
      ),
    );
    // The composition decodes off the frame; give it time to arrive.
    for (var i = 0; i < 5; i++) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 20)),
      );
      await tester.pump();
    }
  }

  double progress(WidgetTester tester) =>
      tester.widget<RawLottie>(find.byType(RawLottie)).progress;

  MemoryLottie memory([Uint8List? bytes]) =>
      MemoryLottie(bytes ?? _json, backgroundLoading: false);

  testWidgets('the slot is registered under its catalogue name [ANI-005]', (
    tester,
  ) async {
    expect(PluxLottie.slots.keys, [PluxLottie.slotName]);
    final slot = _Slot({'url': 'http://example.com/a.json'});
    late Widget built;
    await tester.pumpWidget(
      Builder(
        builder: (context) {
          built = PluxLottie.slots[PluxLottie.slotName]!.builder(context, slot);
          return built;
        },
      ),
    );
    expect(built, isA<SizedBox>(), reason: 'only https addresses play');
    final secure = _Slot({'url': 'https://example.com/a.lottie'});
    await tester.pumpWidget(
      Builder(
        builder: (context) =>
            PluxLottie.slots[PluxLottie.slotName]!.builder(context, secure),
      ),
    );
    expect(find.byType(PluxLottieView), findsOneWidget);
  });

  testWidgets('an animation plays and repeats [ANI-005]', (tester) async {
    await show(tester, PluxLottieView(provider: memory()));
    expect(find.byType(RawLottie), findsOneWidget);
    final first = progress(tester);
    await tester.pump(const Duration(seconds: 1));
    final mid = progress(tester);
    expect(mid, greaterThan(first));
    await tester.pump(const Duration(milliseconds: 1500));
    expect(progress(tester), lessThan(mid), reason: 'it started over');
  });

  testWidgets('a dotLottie archive loads through the same widget [ANI-005]', (
    tester,
  ) async {
    await show(tester, PluxLottieView(provider: memory(_dotLottie())));
    expect(find.byType(RawLottie), findsOneWidget);
    await tester.pump(const Duration(seconds: 1));
    expect(progress(tester), greaterThan(0));
  });

  testWidgets('progress binds the frame and stops playback [ANI-005]', (
    tester,
  ) async {
    await show(tester, PluxLottieView(provider: memory(), progress: 0.25));
    expect(progress(tester), closeTo(0.25, 1e-9));
    await tester.pump(const Duration(seconds: 1));
    expect(progress(tester), closeTo(0.25, 1e-9));
    await show(tester, PluxLottieView(provider: memory(), progress: 0.75));
    expect(progress(tester), closeTo(0.75, 1e-9));
  });

  testWidgets('animate false shows the first frame [ANI-005]', (tester) async {
    await show(tester, PluxLottieView(provider: memory(), animate: false));
    await tester.pump(const Duration(seconds: 1));
    expect(progress(tester), 0);
  });

  testWidgets('a play that does not repeat reports its completion [ANI-005]', (
    tester,
  ) async {
    var completed = 0;
    await show(
      tester,
      PluxLottieView(
        provider: memory(),
        repeat: false,
        onCompleted: () => completed++,
      ),
    );
    expect(completed, 0);
    await tester.pump(const Duration(seconds: 3));
    expect(completed, 1);
    expect(progress(tester), 1);
  });

  testWidgets('reduce motion shows the last frame, or shortens [ANI-007]', (
    tester,
  ) async {
    await show(tester, PluxLottieView(provider: memory()), reduce: true);
    await tester.pump(const Duration(milliseconds: 500));
    expect(progress(tester), 1);
    await show(
      tester,
      PluxLottieView(
        provider: memory(),
        reduceMotion: PluxReduceMotion.shorten,
        repeat: false,
      ),
      reduce: true,
    );
    await tester.pump(const Duration(milliseconds: 600));
    expect(progress(tester), 1, reason: 'a quarter of two seconds is half a');
  });

  testWidgets('reduce motion ignore plays as declared [ANI-007]', (
    tester,
  ) async {
    await show(
      tester,
      PluxLottieView(
        provider: memory(),
        reduceMotion: PluxReduceMotion.ignore,
        repeat: false,
      ),
      reduce: true,
    );
    await tester.pump(const Duration(seconds: 1));
    expect(progress(tester), closeTo(0.5, 0.1));
  });
}
