// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// Assurance levels (SEC-007): the server computes a device's level from
/// the evidence it verified, and the runtime holds pages, routes and data
/// sources to the level they ask for.
library;

/// The number of an assurance level named `AL0` to `AL3`; 0 for anything
/// else, so that a missing or unknown level never grants more.
int assuranceOf(Object? level) => switch (level) {
  'AL1' => 1,
  'AL2' => 2,
  'AL3' => 3,
  _ => 0,
};
