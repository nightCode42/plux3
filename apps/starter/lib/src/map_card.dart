// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';

/// The host's own map card, which plugin pages place as the `MapCard`
/// native slot (WGT-033): a few places to pick, each reported through
/// [onPlace]. The widget knows nothing of Plux.
final class MapCard extends StatelessWidget {
  /// Creates a card titled [title].
  const MapCard({super.key, required this.title, this.onPlace});

  /// The card's title.
  final String title;

  /// Called with the name of the place the user picks.
  final ValueChanged<String>? onPlace;

  /// The places the card offers.
  static const places = ['Harbour', 'Old town', 'Park'];

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            height: 120,
            color: scheme.primaryContainer,
            alignment: Alignment.center,
            child: Icon(Icons.map_outlined, size: 48, color: scheme.primary),
          ),
          Padding(
            padding: const EdgeInsets.all(12),
            child: Text(title, style: Theme.of(context).textTheme.titleMedium),
          ),
          for (final place in places)
            ListTile(
              leading: const Icon(Icons.place_outlined),
              title: Text(place),
              onTap: onPlace == null ? null : () => onPlace!(place),
            ),
        ],
      ),
    );
  }
}
