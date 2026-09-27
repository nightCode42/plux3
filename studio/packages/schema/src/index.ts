// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Plux contracts for Studio, generated from the language-neutral sources in
 * `schema/` by `make gen` (ADR-0025). Nothing here is written by hand except
 * this index and the helpers below.
 */

export type * from "./document.gen.ts";
export { type LimitDefinition, type LimitKey, type LimitScope, type LimitUnit, limits } from "./limits.gen.ts";
export * from "./registry.gen.ts";

import { type LimitDefinition, type LimitKey, limits } from "./limits.gen.ts";
import {
  type ActionDescriptor,
  type ActionName,
  actions,
  type WidgetDescriptor,
  type WidgetType,
  widgets,
} from "./registry.gen.ts";

/** Returns the registry entry for a limit key (LIM-001). */
export function limit(key: LimitKey): LimitDefinition {
  const found = limits.find((l) => l.key === key);
  if (found === undefined) {
    throw new Error(`unknown limit ${key}`);
  }
  return found;
}

/** Returns the warning threshold of a limit: explicit, or 80% of its default (LIM-003). */
export function warningThreshold(key: LimitKey): number {
  const def = limit(key);
  return def.warning > 0 ? def.warning : Math.floor((def.default * 8) / 10);
}

/** Returns the descriptor of a widget type, or undefined for an unknown type (WGT-002). */
export function widget(type: WidgetType | string): WidgetDescriptor | undefined {
  return widgets.find((w) => w.type === type);
}

/** Returns the descriptor of a built-in action, or undefined for an unknown action. */
export function action(name: ActionName | string): ActionDescriptor | undefined {
  return actions.find((a) => a.name === name);
}
