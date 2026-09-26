// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package buildinfo

import (
	"runtime"
	"testing"
)

// TestGetReportsDevDefaultsWhenNotStamped checks the values a plain `go build`
// or `go test` produces, where the linker has not set anything.
func TestGetReportsDevDefaultsWhenNotStamped(t *testing.T) {
	t.Parallel()

	got := Get()

	want := Info{
		Version:    "dev",
		Commit:     "unknown",
		CommitDate: "unknown",
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
	}
	if got != want {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}

// TestStringIncludesEveryField checks that the version line carries all the
// information needed to identify a build in a bug report.
func TestStringIncludesEveryField(t *testing.T) {
	t.Parallel()

	info := Info{
		Version:    "1.2.3",
		Commit:     "0123456789abcdef",
		CommitDate: "2026-09-26T10:00:00Z",
		GoVersion:  "go1.27.1",
		Platform:   "linux/arm64",
	}

	got := info.String()

	want := "1.2.3 (commit 0123456789abcdef, 2026-09-26T10:00:00Z, go1.27.1, linux/arm64)"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
