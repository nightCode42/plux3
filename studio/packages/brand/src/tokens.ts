// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Brand colour tokens from spec Appendix J. Studio, the documentation site and
 * the reference apps use these values (STU-016); change them only together
 * with the specification.
 */

/** A colour as a six-digit hexadecimal string, e.g. `#5B3DF5`. */
export type HexColor = `#${string}`;

/** A token's value in the light and the dark theme. */
export interface ThemedColor {
  readonly light: HexColor;
  readonly dark: HexColor;
}

/** Names of the brand colour tokens. */
export type BrandToken =
  | "brand.ink"
  | "brand.violet"
  | "brand.violet.subtle"
  | "brand.mint"
  | "brand.amber"
  | "brand.surface";

/** The brand colour tokens, keyed by token name. */
export const brandColors: Readonly<Record<BrandToken, ThemedColor>> = {
  "brand.ink": { light: "#0F1222", dark: "#F5F6FA" },
  "brand.violet": { light: "#5B3DF5", dark: "#8B74FF" },
  "brand.violet.subtle": { light: "#EEEAFE", dark: "#2A2250" },
  "brand.mint": { light: "#12B886", dark: "#38D9A9" },
  "brand.amber": { light: "#F59F00", dark: "#FFC53D" },
  "brand.surface": { light: "#F7F8FB", dark: "#0B0D17" },
};

/** The wordmark typeface (Open Font License). */
export const wordmarkFont = "Sora" as const;
