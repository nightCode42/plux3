// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import type { DocumentKind, PageDocument } from "./index.ts";

const example = join(import.meta.dir, "../../../../schema/testdata/documents/loan-calculator");

/** Lists the JSON files under dir. */
function jsonFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      return jsonFiles(path);
    }
    return name.endsWith(".json") ? [path] : [];
  });
}

describe("document types", () => {
  test("every example document has a known kind [SCH-001]", () => {
    const kinds: readonly DocumentKind[] = [
      "actionGraph",
      "app",
      "assetIndex",
      "component",
      "nativeCatalogue",
      "page",
      "plugin",
      "template",
      "theme",
      "translationKeys",
      "translations",
    ];
    const files = jsonFiles(example);
    expect(files.length).toBeGreaterThanOrEqual(10);
    for (const file of files) {
      const doc = JSON.parse(readFileSync(file, "utf8")) as { kind: DocumentKind };
      expect(kinds).toContain(doc.kind);
    }
  });

  test("a page reads through its generated type [SCH-022]", () => {
    const page = JSON.parse(
      readFileSync(join(example, "plugins/loans/pages/calculator.page.json"), "utf8"),
    ) as PageDocument;
    expect(page.pageKind).toBe("screen");
    expect(page.root.type).toBe("Scaffold");
    expect(page.route).toBe("loan-calculator");
  });
});
