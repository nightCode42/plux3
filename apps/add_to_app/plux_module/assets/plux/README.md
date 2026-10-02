<!--
SPDX-FileCopyrightText: 2026 Plux contributors
SPDX-License-Identifier: Apache-2.0
-->

# Baseline

`plux pull -o apps/add_to_app/plux_module/assets/plux` writes the app's baseline here
(`SYN-007`), so the module renders its pages on first launch without a network; the
add-to-app test does this before it builds the hosts. The files are not committed; without
them the first launch waits for the first sync (`SYN-003`).
