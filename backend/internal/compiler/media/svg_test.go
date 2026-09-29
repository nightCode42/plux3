// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSVGC writes a shell script standing in for plux-svgc.
func fakeSVGC(t *testing.T, body string) SVGCompiler {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plux-svgc")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // an executable of the test
		t.Fatal(err)
	}
	return SVGCompiler{Path: path, PathOps: "/lib/path_ops.so", Timeout: 5 * time.Second, MaxOutput: 64}
}

// Verifies: CMP-031.
// The worker runs plux-svgc with the path operations library, the SVG on
// its standard input and no environment, and takes its standard output as
// the encoding; the SVG's faults — malformed, too slow, too large — are
// told apart from the helper's.
func TestSVGCompiler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	echo := fakeSVGC(t, `[ "$1 $2" = "--libpathops /lib/path_ops.so" ] && [ -z "$HOME" ] || exit 9
printf 'VG:'; /bin/cat`)
	got, err := echo.Compile(ctx, []byte("<svg/>"))
	if err != nil || string(got) != "VG:<svg/>" {
		t.Fatalf("compiled %q, %v", got, err)
	}
	for name, tc := range map[string]struct {
		body   string
		reject bool
		want   string
	}{
		"malformed": {`echo "plux-svgc: the SVG cannot be compiled: bad XML" >&2; exit 1`, true, "bad XML"},
		"too large": {`/usr/bin/head -c 100000 /dev/zero`, true, "exceeds 64 bytes"},
		"too slow":  {`exec /bin/sleep 10`, true, "took longer"},
		"crashed":   {`echo "Segmentation fault" >&2; exit 139`, false, "Segmentation fault"},
	} {
		// Not in parallel: writing one script while another test forks
		// makes exec fail with "text file busy" (golang/go#22315).
		t.Run(name, func(t *testing.T) {
			c := fakeSVGC(t, tc.body)
			if name == "too slow" {
				c.Timeout = 100 * time.Millisecond
			}
			_, err := c.Compile(ctx, []byte("<svg/>"))
			if err == nil || errors.Is(err, ErrSVGRejected) != tc.reject || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v", err)
			}
		})
	}
	missing := SVGCompiler{Path: "/no/such/plux-svgc", Timeout: time.Second, MaxOutput: 1}
	if _, err := missing.Compile(ctx, nil); err == nil || errors.Is(err, ErrSVGRejected) {
		t.Errorf("a missing helper: %v", err)
	}
}
