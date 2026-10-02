// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/semantics.dart';
import 'package:flutter/widgets.dart';
import 'package:plux_module/plux_module.dart';

/// The module's entry point, which the host's FlutterEngine runs: the
/// runtime with the host's settings, then the widget tree. Pages the host
/// asks for meanwhile open once both are up.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // The hosts' UI tests find Plux pages by their accessibility labels.
  SemanticsBinding.instance.ensureSemantics();
  final module = PluxModule();
  await module.start();
  runApp(ModuleApp(module: module));
}
