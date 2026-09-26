// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

import { describe, expect, test } from "bun:test";
import {
  brandColors,
  contrastRatio,
  isHexColor,
  minimumComponentContrast,
  minimumTextContrast,
  relativeLuminance,
} from "./index.ts";

describe("contrastRatio", () => {
  test("black on white is 21:1 and symmetric", () => {
    expect(contrastRatio("#000000", "#FFFFFF")).toBeCloseTo(21, 5);
    expect(contrastRatio("#FFFFFF", "#000000")).toBeCloseTo(21, 5);
  });

  test("identical colours are 1:1", () => {
    expect(contrastRatio("#5B3DF5", "#5B3DF5")).toBeCloseTo(1, 5);
  });

  test("matches a published reference value", () => {
    // WCAG example: #777777 on white is about 4.48:1, just below AA.
    expect(contrastRatio("#777777", "#FFFFFF")).toBeCloseTo(4.48, 2);
  });

  test("rejects malformed colours", () => {
    expect(() => relativeLuminance("#FFF")).toThrow(RangeError);
    expect(() => relativeLuminance("5B3DF5")).toThrow(RangeError);
  });
});

describe("brandColors", () => {
  test("every token is a six-digit hex colour in both themes", () => {
    for (const [name, color] of Object.entries(brandColors)) {
      expect(isHexColor(color.light), `${name} light`).toBe(true);
      expect(isHexColor(color.dark), `${name} dark`).toBe(true);
    }
  });

  test("ink on surface meets AA text contrast in both themes", () => {
    const ink = brandColors["brand.ink"];
    const surface = brandColors["brand.surface"];
    expect(contrastRatio(ink.light, surface.light)).toBeGreaterThanOrEqual(minimumTextContrast);
    expect(contrastRatio(ink.dark, surface.dark)).toBeGreaterThanOrEqual(minimumTextContrast);
  });

  test("violet actions meet AA component contrast on the surface", () => {
    const violet = brandColors["brand.violet"];
    const surface = brandColors["brand.surface"];
    expect(contrastRatio(violet.light, surface.light)).toBeGreaterThanOrEqual(minimumComponentContrast);
    expect(contrastRatio(violet.dark, surface.dark)).toBeGreaterThanOrEqual(minimumComponentContrast);
  });
});
