// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"strconv"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
)

// useRevision checks a use of a registry member added in revision rev of
// an entry whose revisions first shipped in the given runtimes (WGT-004).
// When the runtime is newer than the app's minRuntimeVersion the compiler
// rejects the use, or — if the app allows it — requires the feature
// "<entry>.v<rev>" and warns (PLX-1119, PLX-1120).
func (u *unit) useRevision(entry string, runtimes []string, rev uint16, c vctx) {
	if rev == 0 || int(rev) > len(runtimes) {
		return
	}
	app := u.project.App.Doc
	runtime := runtimes[rev-1]
	if !semverLess(app.MinRuntimeVersion, runtime) {
		return
	}
	feature := entry + ".v" + strconv.Itoa(int(rev))
	if app.RequiredFeatures != schema.RequiredFeaturesPolicyRaise {
		u.report(plxerr.RuntimeTooOld, c.file, c.ptr, "%s needs runtime %s, newer than minRuntimeVersion %s", feature, runtime, app.MinRuntimeVersion)
		return
	}
	set := u.features[c.pl]
	if set == nil {
		set = map[string]bool{}
		u.features[c.pl] = set
	}
	if !set[feature] {
		set[feature] = true
		u.report(plxerr.RequiredFeaturesRaised, c.file, c.ptr,
			"%s needs runtime %s: devices with runtimes older than %s keep their last compatible release", feature, runtime, runtime)
	}
}

// semverLess compares two semantic versions MAJOR.MINOR.PATCH; the
// structural stage has checked their form.
func semverLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, y := part(pa, i), part(pb, i)
		if x != y {
			return x < y
		}
	}
	return false
}

func part(p []string, i int) int {
	if i >= len(p) {
		return 0
	}
	n, _ := strconv.Atoi(p[i])
	return n
}
