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
	"path/filepath"
)

// socketMode is the permission of the helper's socket: its owner only.
const socketMode = 0o600

// Listen opens the helper's Unix socket with mode 0600; ctx bounds only the
// set-up, not the listener's life. A stale socket left by an earlier run
// is replaced; anything else at the path is an error, so a mistyped path
// never deletes a file. The socket is bound inside a fresh directory only
// its owner can enter, restricted, and then renamed into place, so it is
// never reachable with wider permissions; the process umask is left alone,
// since it is shared by every goroutine.
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
	private, err := os.MkdirTemp(filepath.Dir(path), ".pkcs11-")
	if err != nil {
		return nil, fmt.Errorf("pkcs11helper: create the socket directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(private) }()
	staged := filepath.Join(private, "s")
	l, err := (&net.ListenConfig{}).Listen(ctx, "unix", staged)
	if err != nil {
		return nil, fmt.Errorf("pkcs11helper: listen on the socket: %w", err)
	}
	if err := os.Chmod(staged, socketMode); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("pkcs11helper: restrict the socket: %w", err)
	}
	if err := os.Rename(staged, path); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("pkcs11helper: move the socket into place: %w", err)
	}
	return &socketListener{Listener: l, path: path}, nil
}

// socketListener removes the socket at its final path when it is closed;
// the listener itself only knows the path it was bound to.
type socketListener struct {
	net.Listener
	path string
}

// Close closes the listener and removes its socket.
func (l *socketListener) Close() error {
	err := l.Listener.Close()
	if rmErr := os.Remove(l.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) && err == nil {
		err = fmt.Errorf("pkcs11helper: remove the socket: %w", rmErr)
	}
	return err //nolint:wrapcheck // the listener's own error, or one wrapped above
}
