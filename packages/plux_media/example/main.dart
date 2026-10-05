// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:plux_flutter/plux_flutter.dart';
import 'package:plux_media/plux_media.dart';

/// Starts Plux with the media actions.
Future<void> main() async {
  await Plux.initialize(
    PluxConfig(
      appId: 'app_example',
      endpoint: Uri.parse('https://plux.example.com'),
      devicePackages: [PluxMedia()],
    ),
  );
}
