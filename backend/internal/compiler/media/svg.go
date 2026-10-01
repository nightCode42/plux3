// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SVGCompiler compiles SVGs to vector_graphics with plux-svgc, the Dart
// helper built into the server image (CMP-031, ADR-0027 Revision). Each
// SVG is one run of the helper, bounded in time and output.
type SVGCompiler struct {
	// Path is the plux-svgc executable.
	Path string
	// PathOps is the libpath_ops library its optimisers load.
	PathOps string
	// Timeout bounds one compilation.
	Timeout time.Duration
	// MaxOutput bounds the encoding, in bytes.
	MaxOutput int
}

// ErrSVGRejected wraps the reason an SVG cannot be compiled: the file, not
// the server, is at fault, so the compilation is not worth retrying.
var ErrSVGRejected = errors.New("media: the SVG cannot be compiled")

// Compile returns the vector_graphics encoding of svg. An error wrapping
// ErrSVGRejected is the SVG's fault (malformed, too slow, too large);
// any other error is the helper's (not installed, killed).
func (c SVGCompiler) Compile(ctx context.Context, svg []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Path, "--libpathops", c.PathOps) //nolint:gosec // the server's own helper, from its configuration
	cmd.Stdin = bytes.NewReader(svg)
	out := &limitedBuffer{max: c.MaxOutput}
	var stderr limitedBuffer
	stderr.max = 4096
	cmd.Stdout, cmd.Stderr = out, &stderr
	cmd.Env = []string{}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case out.over:
		return nil, fmt.Errorf("%w: its encoding exceeds %d bytes", ErrSVGRejected, c.MaxOutput)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("%w: it took longer than %s", ErrSVGRejected, c.Timeout)
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return nil, fmt.Errorf("%w: %s", ErrSVGRejected, strings.TrimSpace(strings.TrimPrefix(stderr.String(), "plux-svgc: ")))
	case err != nil:
		return nil, fmt.Errorf("media: run %s: %w: %s", c.Path, err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// limitedBuffer keeps at most max bytes and records that more came. It
// is only a Writer: a ReadFrom (as an embedded bytes.Buffer would add)
// would let io.Copy bypass the limit.
type limitedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

// Write keeps what fits and drops the rest; it never fails, so the
// helper runs to its exit status rather than blocking on a full pipe.
func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.buf.Len(); n > room {
		b.over = true
		p = p[:max(room, 0)]
	}
	b.buf.Write(p)
	return n, nil
}

// Bytes returns what was kept.
func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

// String returns what was kept.
func (b *limitedBuffer) String() string { return b.buf.String() }
