// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * WCAG 2.2 contrast calculations. The same formula backs the publish-time
 * contrast check of A11Y-003, so Studio can warn while a designer edits.
 */

import type { HexColor } from "./tokens.ts";

const hexPattern = /^#[0-9A-Fa-f]{6}$/;

/** Reports whether a string is a six-digit hexadecimal colour. */
export function isHexColor(value: string): value is HexColor {
  return hexPattern.test(value);
}

/** Converts one sRGB channel (0–255) to linear light, per WCAG 2.2. */
function linearChannel(channel: number): number {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/**
 * Returns the relative luminance of a colour, from 0 (black) to 1 (white).
 * Throws a RangeError for anything but a six-digit hexadecimal colour.
 */
export function relativeLuminance(color: string): number {
  if (!isHexColor(color)) {
    throw new RangeError(`not a six-digit hex colour: ${color}`);
  }
  const r = Number.parseInt(color.slice(1, 3), 16);
  const g = Number.parseInt(color.slice(3, 5), 16);
  const b = Number.parseInt(color.slice(5, 7), 16);
  return 0.2126 * linearChannel(r) + 0.7152 * linearChannel(g) + 0.0722 * linearChannel(b);
}

/** Returns the WCAG contrast ratio of two colours, from 1 to 21. */
export function contrastRatio(a: string, b: string): number {
  const la = relativeLuminance(a);
  const lb = relativeLuminance(b);
  const [light, dark] = la >= lb ? [la, lb] : [lb, la];
  return (light + 0.05) / (dark + 0.05);
}

/** WCAG 2.2 level AA minimum contrast for normal-size text. */
export const minimumTextContrast = 4.5;

/** WCAG 2.2 level AA minimum contrast for large text and UI components. */
export const minimumComponentContrast = 3;
