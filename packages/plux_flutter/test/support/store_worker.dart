// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// The kill harness's worker (QA-009): stages and activates releases in a
// loop, printing each sequence once it is active, until the parent kills
// it. It runs as a plain `dart` process; see test/store/kill_test.dart.

import 'dart:io';

import 'package:plux_flutter/src/store/release_store.dart';

import '../store/store_test_support.dart';

void main(List<String> args) {
  final store = ReleaseStore.open(args[0]);
  var seq = (store.pointer.active ?? 0) + 1;
  while (true) {
    writeRelease(store, seq);
    store.activate();
    store.collectGarbage();
    stdout.writeln(seq);
    seq++;
  }
}
