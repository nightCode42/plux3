// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package buildinfo

import (
	"fmt"
	"runtime"
)

// Link-time values. They are unexported so that nothing can change them at
// run time; the linker sets them with -X.
var (
	version    = "dev"
	commit     = "unknown"
	commitDate = "unknown"
)

// Info describes the binary that is running. It is a plain value and safe for
// concurrent use.
type Info struct {
	// Version is the semantic version of the component, or "dev" for local builds.
	Version string
	// Commit is the full commit hash the binary was built from.
	Commit string
	// CommitDate is the commit timestamp in RFC 3339 format.
	CommitDate string
	// GoVersion is the version of the Go toolchain that built the binary.
	GoVersion string
	// Platform is the target operating system and architecture, e.g. "linux/amd64".
	Platform string
}

// Get returns the build information of the running binary.
func Get() Info {
	return Info{
		Version:    version,
		Commit:     commit,
		CommitDate: commitDate,
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String formats the information as a single line for `version` commands.
func (i Info) String() string {
	return fmt.Sprintf("%s (commit %s, %s, %s, %s)", i.Version, i.Commit, i.CommitDate, i.GoVersion, i.Platform)
}
