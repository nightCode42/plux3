// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Package policy checks repository-level rules that no compiler or linter
// sees: the coordinated disclosure policy (SEC-192) and automated dependency
// updates for every manifest in the monorepo (CI-007). Its tests run the
// checks against the real repository, so a new module without Dependabot
// coverage fails CI.
package policy
