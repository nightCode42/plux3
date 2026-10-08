// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nightCode42/plux3/backend/internal/pkcs11helper"
)

// pinEnv is the environment variable that carries the token PIN.
const pinEnv = "PLUX_PKCS11_PIN"

// Exit codes: 0 success, 1 a failure, 2 a usage error.
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// options are the parsed flags.
type options struct {
	module, tokenLabel, socket, pinFile string
	timeout                             time.Duration
}

// main delegates to run so that the logic is testable.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

// run serves until ctx is done and returns the process exit code.
func run(ctx context.Context, args []string, getenv func(string) string, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stderr, "plux-pkcs11-helper:", err)
			return exitUsage
		}
		return exitOK
	}
	pin, err := readPIN(opts.pinFile, getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "plux-pkcs11-helper:", err)
		return exitUsage
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if err := serve(ctx, opts, pin, log); err != nil {
		log.ErrorContext(ctx, "plux-pkcs11-helper failed", slog.Any("error", err))
		return exitFailed
	}
	return exitOK
}

// parseArgs reads and checks the flags.
func parseArgs(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("plux-pkcs11-helper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.module, "module", "", "path of the vendor PKCS#11 library")
	fs.StringVar(&o.tokenLabel, "token-label", "", "label of the token to log in to")
	fs.StringVar(&o.socket, "socket", "", "path of the Unix socket to serve on (mode 0600)")
	fs.StringVar(&o.pinFile, "pin-file", "", "file holding the token PIN; default is the "+pinEnv+" environment variable")
	fs.DurationVar(&o.timeout, "timeout", pkcs11helper.DefaultTimeout, "deadline for one request")
	if err := fs.Parse(args); err != nil {
		return options{}, fmt.Errorf("flags: %w", err)
	}
	if o.module == "" || o.tokenLabel == "" || o.socket == "" {
		return options{}, errors.New("--module, --token-label and --socket are required")
	}
	return o, nil
}

// readPIN returns the PIN from the file when one is named, else from the
// environment, which it then clears. A PIN file readable by group or
// others is refused.
func readPIN(file string, getenv func(string) string) (string, error) {
	if file == "" {
		pin := getenv(pinEnv)
		if pin == "" {
			return "", errors.New("the PIN must be in " + pinEnv + " or in the file named by --pin-file")
		}
		_ = os.Unsetenv(pinEnv)
		return pin, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("the PIN file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("the PIN file must not be readable by group or others")
	}
	data, err := os.ReadFile(file) //nolint:gosec // the operator names the PIN file
	if err != nil {
		return "", fmt.Errorf("the PIN file: %w", err)
	}
	pin := strings.TrimRight(string(data), "\r\n")
	if pin == "" {
		return "", errors.New("the PIN file is empty")
	}
	return pin, nil
}

// serve opens the token and the socket and answers until ctx is done.
func serve(ctx context.Context, o options, pin string, log *slog.Logger) error {
	mod, err := openModule(o.module, o.tokenLabel, pin)
	if err != nil {
		return err
	}
	defer mod.close()
	l, err := pkcs11helper.Listen(ctx, o.socket)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	log.InfoContext(ctx, "plux-pkcs11-helper listening", slog.String("socket", o.socket))
	if err := pkcs11helper.NewServer(mod, pkcs11helper.Options{Timeout: o.timeout, Log: log}).Serve(ctx, l); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
