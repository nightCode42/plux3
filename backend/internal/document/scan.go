// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package document

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Scanner checks an uploaded file for malware before it is stored
// (SRV-060). Scan returns ErrInfected, wrapped with what was found, for a
// file it rejects, and any other error when it could not decide.
type Scanner interface {
	Scan(ctx context.Context, data []byte) error
}

// ErrInfected is returned by a Scanner for a file it rejects.
var ErrInfected = errors.New("document: the malware scanner rejected the file")

// Clamd scans with ClamAV's daemon over its INSTREAM command.
type Clamd struct {
	// Network is "tcp" or "unix"; Address is where clamd listens.
	Network, Address string
	// Timeout bounds one scan; zero means a minute.
	Timeout time.Duration
}

// clamdChunk is the size of each INSTREAM chunk.
const clamdChunk = 64 << 10

// Scan sends the file to clamd and reads its verdict.
func (c Clamd) Scan(ctx context.Context, data []byte) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, c.Network, c.Address)
	if err != nil {
		return fmt.Errorf("document: reach the malware scanner: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	w := bufio.NewWriter(conn)
	if _, err := w.WriteString("zINSTREAM\x00"); err != nil {
		return fmt.Errorf("document: malware scanner: %w", err)
	}
	for len(data) > 0 {
		n := min(len(data), clamdChunk)
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(n)) //nolint:gosec // at most clamdChunk
		if _, err := w.Write(size[:]); err != nil {
			return fmt.Errorf("document: malware scanner: %w", err)
		}
		if _, err := w.Write(data[:n]); err != nil {
			return fmt.Errorf("document: malware scanner: %w", err)
		}
		data = data[n:]
	}
	if _, err := w.Write([]byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("document: malware scanner: %w", err)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("document: malware scanner: %w", err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes(0)
	if err != nil {
		return fmt.Errorf("document: read the malware scanner's verdict: %w", err)
	}
	verdict := strings.TrimSpace(string(bytes.TrimSuffix(reply, []byte{0})))
	switch {
	case strings.HasSuffix(verdict, " OK"):
		return nil
	case strings.HasSuffix(verdict, " FOUND"):
		found := strings.TrimSuffix(strings.TrimPrefix(verdict, "stream: "), " FOUND")
		return fmt.Errorf("%w: %s", ErrInfected, found)
	default:
		return fmt.Errorf("document: the malware scanner answered %q", verdict)
	}
}
