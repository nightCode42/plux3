// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/widgets.dart';
import 'package:plux_module/plux_module.dart';

/// The module's entry point, which the host's FlutterEngine runs: the
/// widget tree at once, then the runtime with the host's settings. Pages
/// the host asks for meanwhile open once the runtime is up. An Android
/// FlutterActivity draws nothing until Flutter's first frame, so the
/// first frame does not wait on the start.
///
/// The semantics tree is left to the platform: each native view that
/// shows the engine turns it on as it attaches, when a screen reader or a
/// UI test is running, and the framework then sends that view the whole
/// tree. A handle the module held itself would keep the tree on across
/// views, so a view attached later would never receive it.
Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final module = PluxModule();
  runApp(ModuleApp(module: module));
  await module.start();
}
