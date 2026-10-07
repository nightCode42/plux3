<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Baseline

`make dev` writes the seeded app's baseline here with `plux pull` (`SYN-007`), so the
express renders its pages on first launch without a network. The files are not committed;
without them the first launch waits for the first sync (`SYN-003`).
