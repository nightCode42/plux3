// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';
import 'package:plux_starter/plux/plux.g.dart';

/// A native screen holding two views of the plugin's exported
/// `counter-badge` component (NAV-004). Both read the app's exposed
/// `counter` entry; the screen's native button writes it, and both views
/// show the new value in the same frame (HST-021, STA-030). Both go
/// through the typed API plux codegen writes (HST-030).
final class MixedScreen extends StatelessWidget {
  /// Creates the screen.
  const MixedScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final counter = PluxAppState.counter;
    return Scaffold(
      appBar: AppBar(title: const Text('Mixed screen')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text('Two plugin views, one shared value:'),
          const SizedBox(height: 12),
          PluxComponents.counterBadge(label: 'Left'),
          const SizedBox(height: 8),
          PluxComponents.counterBadge(label: 'Right'),
          const SizedBox(height: 16),
          StreamBuilder<int?>(
            stream: counter.watch(),
            builder: (context, snapshot) => Text(
              'Native view of the counter: ${snapshot.data ?? counter.value}',
            ),
          ),
          const SizedBox(height: 16),
          FilledButton.icon(
            key: const ValueKey('add-one'),
            icon: const Icon(Icons.add),
            label: const Text('Add one'),
            onPressed: () => counter.set((counter.value ?? 0) + 1),
          ),
        ],
      ),
    );
  }
}
