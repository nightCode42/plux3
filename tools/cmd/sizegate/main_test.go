// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	// Incompressible: a xorshift generator's low bytes.
	data := make([]byte, n)
	x := uint32(2463534242)
	for i := range data {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		data[i] = byte(x & 0xff)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestGateBudgetAndBaseline checks the 3 MiB budget and the 10% growth
// over the committed overhead, and -update.
// Verifies: RT-061, QA-007.
func TestGateBudgetAndBaseline_RT_061(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.apk")
	write(t, blank, 1000)
	baseline := filepath.Join(dir, "baseline.json")
	for _, c := range []struct {
		name      string
		plux      int
		committed string
		want      int
	}{
		{"within budget and baseline", 1000 + 2<<20, `{"android-arm64-apk": 2097152}`, exitOK},
		{"grew 9%", 1000 + 2<<20*109/100, `{"android-arm64-apk": 2097152}`, exitOK},
		{"grew 11%", 1000 + 2<<20*111/100, `{"android-arm64-apk": 2097152}`, exitFailed},
		{"over budget", 1000 + 3<<20 + 1, `{"android-arm64-apk": 4194304}`, exitFailed},
		{"no baseline", 1000 + 2<<20, `{}`, exitFailed},
	} {
		plux := filepath.Join(dir, c.name+".apk")
		write(t, plux, c.plux)
		if err := os.WriteFile(baseline, []byte(c.committed), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{"-target", "android-arm64-apk", "-blank", blank, "-plux", plux, "-baseline", baseline}, &stdout, &stderr)
		if code != c.want {
			t.Errorf("%s: exit %d, stderr: %s", c.name, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "| added by plux_flutter |") {
			t.Errorf("%s: report:\n%s", c.name, stdout.String())
		}
	}

	// -update records the overhead and passes; other targets are kept.
	if err := os.WriteFile(baseline, []byte(`{"ios-arm64-ipa": 5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plux := filepath.Join(dir, "update.apk")
	write(t, plux, 1000+2<<20)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-target", "android-arm64-apk", "-blank", blank, "-plux", plux, "-baseline", baseline, "-update"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("update: exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"android-arm64-apk\": 2097152,\n  \"ios-arm64-ipa\": 5\n}\n"; string(got) != want {
		t.Errorf("baseline:\n%s", got)
	}
}

// TestMeasuresAppDirectoriesAsIPAs checks that a directory is measured
// as its compressed archive, the same on every run.
func TestMeasuresAppDirectoriesAsIPAs(t *testing.T) {
	t.Parallel()
	app := filepath.Join(t.TempDir(), "Runner.app")
	write(t, filepath.Join(app, "Runner"), 50000)
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), bytes.Repeat([]byte("<key>a</key>"), 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := measure(app)
	if err != nil {
		t.Fatal(err)
	}
	b, err := measure(app)
	if err != nil {
		t.Fatal(err)
	}
	// The random executable does not compress; the repeated plist does.
	if a != b || a < 50000 || a > 52000 {
		t.Errorf("sizes %d, %d", a, b)
	}
}

func TestUsageAndIOErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	apk := filepath.Join(dir, "a.apk")
	write(t, apk, 10)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		nil,
		{"-target", "windows", "-blank", apk, "-plux", apk},
		{"-target", "ios-arm64-ipa", "-blank", apk},
		{"-target", "ios-arm64-ipa", "-blank", filepath.Join(dir, "missing"), "-plux", apk},
		{"-target", "ios-arm64-ipa", "-blank", apk, "-plux", filepath.Join(dir, "missing")},
		{"-target", "ios-arm64-ipa", "-blank", apk, "-plux", apk, "-baseline", bad},
		{"-target", "ios-arm64-ipa", "-blank", apk, "-plux", apk, "-baseline", filepath.Join(dir, "no", "dir.json"), "-update"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitError {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}
