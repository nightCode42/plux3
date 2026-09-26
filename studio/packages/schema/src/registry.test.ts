// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, test } from "bun:test";
import { action, actions, enums, valueTypes, widget, widgets } from "./index.ts";

describe("widget and action registries", () => {
  test("describe every widget with its members [WGT-001, WGT-002]", () => {
    const text = widget("Text");
    expect(text?.layer).toBe(1);
    expect(text?.props.find((p) => p.name === "data")).toMatchObject({ type: "string", required: true });
    expect(widget("Match")?.slots.map((s) => s.name)).toEqual(["branches", "otherwise"]);
    expect(widget("NoSuchWidget")).toBeUndefined();
  });

  test("describe every action of Appendix D", () => {
    expect(actions).toHaveLength(55);
    expect(action("navigate")?.inputs.find((i) => i.name === "route")?.type).toBe("route");
    expect(action("switch")?.branchesFrom).toBe("cases");
    expect(action("noSuchAction")).toBeUndefined();
  });

  test("keep IDs unique within each kind [BND-011]", () => {
    for (const ids of [
      widgets.map((w) => w.id),
      valueTypes.map((t) => t.id),
      enums.map((e) => e.id),
      actions.map((a) => a.id),
    ]) {
      expect(new Set(ids).size).toBe(ids.length);
    }
    for (const w of widgets) {
      const propIds = w.props.map((p) => p.id);
      expect(new Set(propIds).size).toBe(propIds.length);
      expect(w.children !== undefined && w.slots.length > 0).toBe(false);
    }
  });
});
