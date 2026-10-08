// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build unix

package pkcs11helper

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
)

// socketMode is the permission of the helper's socket: its owner only.
const socketMode = 0o600

// Listen opens the helper's Unix socket with mode 0600; ctx bounds only the
// set-up, not the listener's life. A stale socket left by an earlier run
// is replaced; anything else at the path is an error, so a mistyped path
// never deletes a file. The umask is narrowed while the socket is
// created, so it is never visible with wider permissions; this is for
// start-up, before other goroutines run.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("pkcs11helper: %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("pkcs11helper: remove the stale socket: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("pkcs11helper: inspect the socket path: %w", err)
	}
	old := syscall.Umask(0o177)
	l, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("pkcs11helper: listen on the socket: %w", err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("pkcs11helper: restrict the socket: %w", err)
	}
	return l, nil
}
