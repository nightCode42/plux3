// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, test } from "bun:test";
import { type LimitKey, limit, limits, warningThreshold } from "./index.ts";

describe("limits registry", () => {
  test("holds the specified defaults [LIM-001]", () => {
    expect(limit("page.nodes").default).toBe(5000);
    expect(warningThreshold("page.nodes")).toBe(1000);
    expect(warningThreshold("plugin.pages")).toBe(400);
    expect(limit("pxl.operationBudget").unit).toBe("operations");
  });

  test("keys are unique and sorted", () => {
    const keys = limits.map((l) => l.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect([...keys].sort()).toEqual(keys);
  });

  test("rejects unknown keys", () => {
    expect(() => limit("no.such" as LimitKey)).toThrow("unknown limit");
  });
});
