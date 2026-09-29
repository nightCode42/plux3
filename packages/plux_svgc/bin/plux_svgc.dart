// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'dart:io';

import 'package:plux_svgc/plux_svgc.dart';

Future<void> main(List<String> args) async {
  final code = await run(args, stdin, stdout, stderr);
  await stdout.flush();
  exit(code);
}
