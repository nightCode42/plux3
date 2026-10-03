// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_module/plux_module.dart';

/// The module's entry point, which the host's FlutterEngine runs: the
/// runtime with the host's settings, then the widget tree. Pages the host
/// asks for meanwhile open once both are up.
///
/// The semantics tree is left to the platform: each native view that
/// shows the engine turns it on as it attaches, when a screen reader or a
/// UI test is running, and the framework then sends that view the whole
/// tree. A handle the module held itself would keep the tree on across
/// views, so a view attached later would never receive it.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final module = PluxModule();
  await module.start();
  runApp(ModuleApp(module: module));
}
