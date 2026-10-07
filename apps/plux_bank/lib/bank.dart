// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Plux Bank reference app (DX-004): a native shell around the published
/// sign-in page, the host's auth delegate that holds the session token, and
/// the clients end-to-end builds use to trust the reference API's test CA.
library;

export 'src/bank_app.dart';
export 'src/config.dart';
export 'src/host.dart';
export 'src/reference_ca.dart';
