// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"strings"
	"unicode"
)

// Slug converts a Markdown heading into the anchor GitHub generates for it:
// lower case, letters, digits, spaces, hyphens and underscores kept, spaces
// replaced by hyphens, everything else dropped.
func Slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}
