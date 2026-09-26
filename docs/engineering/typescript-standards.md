# TypeScript and Studio Standards

How TypeScript in Plux Studio (`studio/`) is written. Biome ([studio/biome.json](../../studio/biome.json)) and the TypeScript compiler ([studio/tsconfig.json](../../studio/tsconfig.json)) enforce most of this document.

---

## 1. Toolchain

- **Bun** is the runtime, package manager and test runner; its version is pinned in `studio/package.json` (`packageManager`), the Makefile and CI.
- **TypeScript** type-checks (`bunx tsc`); Bun executes TypeScript directly, so there is no separate emit step for packages.
- **Biome** formats and lints (`make studio-fmt`, `make studio-lint`).
- Installs are reproducible: `bun install --frozen-lockfile`, exact versions, `bun.lock` committed.

## 2. Strictness

- `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `noImplicitOverride`, `noPropertyAccessFromIndexSignature` and `verbatimModuleSyntax` are on.
- No `any`, no non-null assertions (`!`), no `@ts-ignore`. Narrow with type guards instead.
- Public functions declare their parameter and return types; exported values are `readonly` where possible.

## 3. Modules

- ES modules only; relative imports include the `.ts` extension.
- One responsibility per module; packages expose their API through `src/index.ts`.
- Shared constants that the specification defines (brand tokens, limits, error codes) live in one package and are imported, never copied.

## 4. React (from P11)

- Function components and hooks only. Server state lives in TanStack Query, local UI state in component state or a small store (`STU-004`).
- Components are accessible by construction: semantic elements, labels, keyboard support and visible focus (`STU-011`).
- The canvas keeps rendering off React's render path; React renders panels and chrome, Plux Canvas renders the scene (`STU-003`).

## 5. Security

- No secrets or tokens in browser code; the backend-for-frontend holds them (`SEC-101`).
- No `innerHTML` with untrusted content; Trusted Types and the Content Security Policy are respected.
- No `console` in shipped code; diagnostics go through the logging facility.

## 6. Documentation and formatting

- Every file starts with the SPDX header; exported symbols carry TSDoc comments stating what they do.
- Formatting: two-space indent, double quotes, semicolons, 120-column lines (Biome).
