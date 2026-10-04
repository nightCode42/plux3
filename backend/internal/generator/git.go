// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package generator

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// CheckGitTarget reports whether remote and branch can be handed to git as
// a repository and a branch name, and never read as options (GEN-003): the
// remote may not start with "-" or hold spaces or control characters, and
// the branch must be a valid branch name. Some git options run commands,
// so the check matters wherever the values come from someone other than
// the person running git, as when P11's server runs the generator.
func CheckGitTarget(remote, branch string) error {
	if remote == "" || strings.HasPrefix(remote, "-") || strings.ContainsFunc(remote, unsafeRune) {
		return fmt.Errorf("the remote %q is not a repository URL or path", remote)
	}
	if err := checkBranch(branch); err != nil {
		return fmt.Errorf("the branch %q is not a valid branch name: %w", branch, err)
	}
	return nil
}

// checkBranch applies git's rules for a branch name (git-check-ref-format).
func checkBranch(b string) error {
	switch {
	case b == "" || b == "@":
		return errors.New("it is empty or @")
	case strings.HasPrefix(b, "-"):
		return errors.New("it starts with -")
	case strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, "."):
		return errors.New("it starts or ends with / or ends with a dot")
	case strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{"):
		return errors.New("it holds .., // or @{")
	case strings.ContainsFunc(b, func(r rune) bool { return unsafeRune(r) || strings.ContainsRune(`~^:?*[\`, r) }):
		return errors.New(`it holds a space, a control character or one of ~^:?*[\`)
	}
	for c := range strings.SplitSeq(b, "/") {
		if strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return errors.New("a part starts with . or ends with .lock")
		}
	}
	return nil
}

func unsafeRune(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
