// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

// Command refapi is the reference backend of the Plux reference apps: the
// Plux Bank and Plux Express APIs the apps' end-to-end flows call (DX-004).
// It serves HTTPS and WSS only, with a certificate signed by a certificate
// authority it generates at start; the authority's public certificate is
// written where -ca-out says, for the apps to trust.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := runUntilSignal(); err != nil {
		fmt.Fprintln(os.Stderr, "refapi:", err)
		os.Exit(1)
	}
}

// runUntilSignal runs the server until the process is interrupted or
// terminated.
func runUntilSignal() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args[1:], os.Stdout)
}

// run starts the server named by args and serves until ctx ends. It prints
// one line to stdout once it is listening.
func run(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("refapi", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "address to listen on")
	caOut := fs.String("ca-out", "", "file to write the CA certificate (PEM, public) to")
	trackStep := fs.Duration("track-step", defaultTrackStep, "pause between the status steps of an order's tracking stream")
	heartbeat := fs.Duration("heartbeat", defaultHeartbeat, "interval of the notification stream's heartbeat comments")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}
	if *caOut == "" {
		return errors.New("-ca-out is required: the apps need the CA certificate to trust the server")
	}
	now := time.Now()
	ca, err := newAuthority(now)
	if err != nil {
		return err
	}
	cfg, err := ca.issue(now)
	if err != nil {
		return err
	}
	// The certificate is public: readable by whoever runs the apps.
	if err := os.WriteFile(*caOut, ca.certificatePEM(), 0o644); err != nil { //nolint:gosec // G306: a public certificate.
		return fmt.Errorf("writing the CA certificate: %w", err)
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", *addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", *addr, err)
	}
	if _, err := fmt.Fprintf(stdout, "refapi listening on https://%s\n", ln.Addr()); err != nil {
		_ = ln.Close()
		return fmt.Errorf("announcing the address: %w", err)
	}
	return Serve(ctx, ln, New(Options{TrackStep: *trackStep, Heartbeat: *heartbeat}), cfg)
}
