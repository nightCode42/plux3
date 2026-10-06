// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

/// The Plux Express reference app (DX-004): a native shell around the
/// published catalogue, the host's switch that tells Plux whether the
/// network is available, and the clients end-to-end builds use to trust the
/// reference API's test CA.
library;

export 'src/config.dart';
export 'src/express_app.dart';
export 'src/host.dart';
export 'src/reference_ca.dart';
