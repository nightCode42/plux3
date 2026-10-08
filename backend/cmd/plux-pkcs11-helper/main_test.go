// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verifies: SEC-120.
func TestParseArgsRequiresTheTokenAndTheSocket(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"nothing":     {},
		"no module":   {"--token-label", "t", "--socket", "/s"},
		"no label":    {"--module", "/m.so", "--socket", "/s"},
		"no socket":   {"--module", "/m.so", "--token-label", "t"},
		"unknown":     {"--module", "/m.so", "--token-label", "t", "--socket", "/s", "--pin", "1234"},
		"a PIN value": {"--module", "/m.so", "--token-label", "t", "--socket", "/s", "-pin=1234"},
	} {
		if _, err := parseArgs(args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	o, err := parseArgs([]string{"--module", "/m.so", "--token-label", "t", "--socket", "/s", "--pin-file", "/p"}, &bytes.Buffer{})
	if err != nil || o.module != "/m.so" || o.tokenLabel != "t" || o.socket != "/s" || o.pinFile != "/p" {
		t.Errorf("parseArgs = %+v, %v", o, err)
	}
}

// Verifies: SEC-120.
func TestReadPINFromAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	getenv := func(string) string { return "" }
	if pin, err := readPIN(write("ok", "1234\n", 0o600), getenv); err != nil || pin != "1234" {
		t.Errorf("readPIN = %q, %v", pin, err)
	}
	for name, path := range map[string]string{
		"group-readable": write("open", "1234", 0o640),
		"empty":          write("empty", "\n", 0o600),
		"missing":        filepath.Join(dir, "none"),
	} {
		if _, err := readPIN(path, getenv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Verifies: SEC-120.
func TestReadPINFromTheEnvironment(t *testing.T) {
	t.Setenv(pinEnv, "4321")
	pin, err := readPIN("", os.Getenv)
	if err != nil || pin != "4321" {
		t.Fatalf("readPIN = %q, %v", pin, err)
	}
	if os.Getenv(pinEnv) != "" {
		t.Error("the PIN stays in the environment of the process")
	}
	if _, err := readPIN("", func(string) string { return "" }); err == nil {
		t.Error("accepted no PIN anywhere")
	}
}

// Verifies: SEC-120.
func TestRunNeverEchoesAPIN(t *testing.T) {
	t.Setenv(pinEnv, "secret-pin-9876")
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"--module", "/nonexistent.so", "--token-label", "t", "--socket", filepath.Join(t.TempDir(), "s")}, os.Getenv, &stderr)
	if code != exitFailed {
		t.Errorf("exit code = %d, want %d", code, exitFailed)
	}
	if strings.Contains(stderr.String(), "secret-pin-9876") {
		t.Errorf("stderr holds the PIN: %q", stderr.String())
	}
}
