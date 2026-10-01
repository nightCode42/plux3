// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

import 'package:flutter/material.dart';

/// The host of the size job's plux app, without Plux (RT-061).
void main() => runApp(
  const MaterialApp(
    home: Scaffold(body: Center(child: Text('blank'))),
  ),
);
