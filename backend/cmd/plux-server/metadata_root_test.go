// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// cli runs plux-server commands and checks their exit codes.
type cli struct {
	t   *testing.T
	ctx context.Context
}

// run returns the exit code and both streams.
func (c cli) run(args ...string) (int, string, string) {
	c.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(c.ctx, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// must runs a command that has to succeed and returns its stdout.
func (c cli) must(args ...string) string {
	c.t.Helper()
	code, stdout, stderr := c.run(args...)
	if code != exitOK {
		c.t.Fatalf("%v: exit %d; %s", args[:2], code, stderr)
	}
	return stdout
}

// refuses runs a command that has to fail and returns its stderr.
func (c cli) refuses(args ...string) string {
	c.t.Helper()
	code, _, stderr := c.run(args...)
	if code != exitFailed {
		c.t.Fatalf("%v: exit %d, want a failure; %s", args[:2], code, stderr)
	}
	return stderr
}

// write stores a file and returns its path.
func write(t *testing.T, dir, file, content string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fileHolders gives six development holders backed by file keys.
func fileHolders(dir string) (backend, refs []string) {
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		refs = append(refs, "file:holder-"+n)
	}
	return []string{"-key-dir", filepath.Join(dir, "keys")}, refs
}

// Verifies: SEC-051, SEC-121.
// A 2-of-3 root ceremony with development file keys, against a database:
// each holder signs on their own copy, a single signature is refused, and
// a rotation needs the old and the new threshold.
func TestRootCeremonyWithFileKeys_SEC_121(t *testing.T) {
	dir := t.TempDir()
	backend, refs := fileHolders(dir)
	runCeremony(t, dir, backend, refs)
}

// runCeremony is the rehearsal of SEC-121, whatever holds the keys: six
// holder references, the first three signing root 1 and the last three
// joining the first two in root 2.
func runCeremony(t *testing.T, dir string, backend, refs []string) {
	t.Helper()
	c := cli{t: t, ctx: context.Background()}
	url := storagetest.SchemaURL(t)
	cfg := write(t, dir, "plux-server.yaml", "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \""+url+"\"\n"+
		"objectStorage:\n  directory: \""+filepath.Join(dir, "objects")+"\"\n"+
		"signing:\n  directory: \""+filepath.Join(dir, "online")+"\"\n")
	org, env := seedEnvironment(t, c, cfg)
	db := []string{"-config", cfg, "-organization", org, "-environment", env}

	// The server side: the online keys the roots must list.
	online := write(t, dir, "online-keys.txt", c.must(append([]string{"metadata", "keys"}, db...)...))
	pubs := make([]string, len(refs))
	for i, ref := range refs {
		pem := c.must(append([]string{"metadata", "root-key-export", "-key", ref}, backend...)...)
		if !strings.Contains(pem, "BEGIN PUBLIC KEY") || strings.Contains(pem, "PRIVATE") {
			t.Fatalf("root-key-export printed %q", pem)
		}
		pubs[i] = write(t, dir, "pub-"+strconv.Itoa(i)+".pem", pem)
	}
	newRoot := func(version int, out string, keys ...string) {
		t.Helper()
		args := []string{
			"metadata", "root-new", "-type", "development", "-version", strconv.Itoa(version), "-expires", "720h",
			"-threshold", "2", "-online-keys", online, "-out", out,
		}
		for _, k := range keys {
			args = append(args, "-key", k)
		}
		c.must(args...)
	}
	sign := func(in, out, ref string, extra ...string) {
		t.Helper()
		c.must(append([]string{"metadata", "root-sign", "-in", in, "-out", out, "-key", ref}, append(backend, extra...)...)...)
	}
	path := func(f string) string { return filepath.Join(dir, f) }

	// Root 1: made once, signed by two of the three holders, each in turn.
	newRoot(1, path("r1.json"), pubs[0], pubs[1], pubs[2])
	if got := c.refuses("metadata", "root-verify", "-in", path("r1.json")); !strings.Contains(got, "threshold") {
		t.Errorf("an unsigned root verified: %s", got)
	}
	sign(path("r1.json"), path("r1-a.json"), refs[0])
	if got := c.refuses("metadata", "root-verify", "-in", path("r1-a.json")); !strings.Contains(got, "threshold") {
		t.Errorf("one signature of two verified: %s", got)
	}
	if got := c.refuses(append([]string{"metadata", "upload-root"}, append(db, path("r1-a.json"))...)...); !strings.Contains(got, "threshold") {
		t.Errorf("one signature of two was uploaded: %s", got)
	}
	sign(path("r1-a.json"), path("r1-ab.json"), refs[2])
	if got := c.must("metadata", "root-verify", "-in", path("r1-ab.json")); !strings.Contains(got, "2 of 2 required") {
		t.Errorf("root-verify: %q", got)
	}
	if got := c.must(append([]string{"metadata", "upload-root"}, append(db, path("r1-ab.json"))...)...); !strings.Contains(got, "stored root 1") {
		t.Errorf("upload-root: %q", got)
	}

	// Root 2: the holders a, b and c are replaced by d, e and f; a and c
	// sign for the old root.
	newRoot(2, path("r2.json"), pubs[3], pubs[4], pubs[5])
	sign(path("r2.json"), path("r2-new1.json"), refs[3])
	sign(path("r2-new1.json"), path("r2-new.json"), refs[4])
	sign(path("r2.json"), path("r2-old1.json"), refs[0], "-previous", path("r1-ab.json"))
	sign(path("r2-old1.json"), path("r2-old.json"), refs[2], "-previous", path("r1-ab.json"))
	if got := c.refuses("metadata", "root-verify", "-in", path("r2-new.json"), "-previous", path("r1-ab.json")); !strings.Contains(got, "previous keys") {
		t.Errorf("a rotation signed by the new keys alone verified: %s", got)
	}
	if got := c.refuses("metadata", "root-verify", "-in", path("r2-old.json"), "-previous", path("r1-ab.json")); !strings.Contains(got, "new keys") {
		t.Errorf("a rotation signed by the old keys alone verified: %s", got)
	}
	if got := c.refuses(append([]string{"metadata", "root-sign", "-in", path("r2.json"), "-out", path("x.json"), "-key", refs[0]}, backend...)...); !strings.Contains(got, "not a root key") {
		t.Error("an old holder signed a root that does not list them, without the previous root")
	}
	if got := c.refuses(append([]string{"metadata", "upload-root"}, append(db, path("r2-new.json"))...)...); !strings.Contains(got, "previous keys") {
		t.Errorf("a rotation without the old threshold was uploaded: %s", got)
	}
	sign(path("r2-new.json"), path("r2-full1.json"), refs[0], "-previous", path("r1-ab.json"))
	sign(path("r2-full1.json"), path("r2-full.json"), refs[2], "-previous", path("r1-ab.json"))
	if got := c.must("metadata", "root-verify", "-in", path("r2-full.json"), "-previous", path("r1-ab.json")); !strings.Contains(got, "rotation") {
		t.Errorf("root-verify of the rotation: %q", got)
	}
	if got := c.must(append([]string{"metadata", "upload-root"}, append(db, path("r2-full.json"))...)...); !strings.Contains(got, "stored root 2") {
		t.Errorf("upload-root of the rotation: %q", got)
	}
}

// Verifies: SEC-051.
// Production roots keep the specification's floor: two signatures and an
// expiry within a year; a development root is warned about.
func TestRootNewRules_SEC_051(t *testing.T) {
	dir := t.TempDir()
	c := cli{t: t, ctx: context.Background()}
	backend, refs := fileHolders(dir)
	var pubs []string
	for i, ref := range refs[:3] {
		pubs = append(pubs, write(t, dir, "p"+strconv.Itoa(i)+".pem", c.must(append([]string{"metadata", "root-key-export", "-key", ref}, backend...)...)))
	}
	pubs = append(pubs, pubs[0])
	online := write(t, dir, "online.txt", onlineKeysFile(t))
	base := func(typ, expires string, threshold int, keys ...string) []string {
		a := []string{
			"metadata", "root-new", "-type", typ, "-version", "1", "-expires", expires, "-threshold", strconv.Itoa(threshold),
			"-online-keys", online, "-out", filepath.Join(dir, "root.json"),
		}
		for _, k := range keys {
			a = append(a, "-key", k)
		}
		return a
	}
	if got := c.refuses(base("production", "720h", 1, pubs[0], pubs[1])...); !strings.Contains(got, "threshold of at least 2") {
		t.Errorf("a production root with threshold 1: %s", got)
	}
	if got := c.refuses(base("production", "9000h", 2, pubs[0], pubs[1], pubs[2])...); !strings.Contains(got, "must expire within") {
		t.Errorf("a production root of more than a year: %s", got)
	}
	if got := c.refuses(base("production", "720h", 2, pubs[0], pubs[3])...); !strings.Contains(got, "listed twice") {
		t.Errorf("the same key twice: %s", got)
	}
	if got := c.refuses(base("production", "720h", 4, pubs[0], pubs[1], pubs[2])...); !strings.Contains(got, "threshold") {
		t.Errorf("a threshold above the keys: %s", got)
	}
	c.must(base("production", "8760h", 2, pubs[0], pubs[1], pubs[2])...)
	first, _ := os.ReadFile(filepath.Join(dir, "root.json"))
	code, _, warn := c.run(base("development", time.Now().Add(100*time.Hour).UTC().Format(time.RFC3339), 1, pubs[0])...)
	if code != exitOK || !strings.Contains(warn, "development root") {
		t.Errorf("a development root: exit %d; %s", code, warn)
	}
	if code, _, _ := c.run(base("staging", "720h", 2, pubs[0], pubs[1])...); code != exitUsage {
		t.Errorf("an unknown type: exit %d", code)
	}
	if code, _, _ := c.run("metadata", "root-new"); code != exitUsage {
		t.Errorf("root-new without flags: exit %d", code)
	}
	// The same absolute inputs give the same bytes (CMP-002).
	at := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	c.must(base("production", at, 2, pubs[0], pubs[1], pubs[2])...)
	one, _ := os.ReadFile(filepath.Join(dir, "root.json"))
	c.must(base("production", at, 2, pubs[2], pubs[0], pubs[1])...)
	two, _ := os.ReadFile(filepath.Join(dir, "root.json"))
	if !bytes.Equal(one, two) || bytes.Equal(first, one) {
		t.Error("the same keys in another order gave another document, or the expiry was ignored")
	}
}

// Verifies: SEC-051.
// A holder cannot sign with a key the root does not list, a file key that
// does not exist is not created, and an expired root is neither signed nor
// verified.
func TestRootSignRefusals_SEC_051(t *testing.T) {
	dir := t.TempDir()
	c := cli{t: t, ctx: context.Background()}
	backend, refs := fileHolders(dir)
	var pubs []string
	for i, ref := range refs[:4] {
		pubs = append(pubs, write(t, dir, "p"+strconv.Itoa(i)+".pem", c.must(append([]string{"metadata", "root-key-export", "-key", ref}, backend...)...)))
	}
	online := write(t, dir, "online.txt", onlineKeysFile(t))
	root := filepath.Join(dir, "root.json")
	c.must("metadata", "root-new", "-type", "development", "-version", "1", "-expires", "1h", "-threshold", "2",
		"-online-keys", online, "-out", root, "-key", pubs[0], "-key", pubs[1], "-key", pubs[2])
	sign := func(ref string) []string {
		return append([]string{"metadata", "root-sign", "-in", root, "-out", root, "-key", ref}, backend...)
	}
	if got := c.refuses(sign(refs[3])...); !strings.Contains(got, "not a root key") {
		t.Errorf("a stranger signed: %s", got)
	}
	if got := c.refuses(sign("file:nobody")...); got == "" {
		t.Error("a missing file key signed")
	}
	if _, err := os.Stat(filepath.Join(dir, "keys", "nobody.ed25519")); err == nil {
		t.Error("a missing file key was created by root-sign")
	}
	if code, _, _ := c.run("metadata", "root-sign", "-in", root, "-out", root, "-key", "file:holder-a"); code != exitUsage {
		t.Errorf("a file key without -key-dir: exit %d", code)
	}
	if code, _, _ := c.run("metadata", "root-sign", "-in", root, "-out", root, "-key", "pkcs11:object=x"); code != exitUsage {
		t.Errorf("a pkcs11 key without a socket: exit %d", code)
	}
	if got := c.refuses("metadata", "root-sign", "-in", root, "-out", root, "-key", "pkcs11:object=x", "-pkcs11-socket", filepath.Join(dir, "none.sock")); got == "" {
		t.Error("signing without a helper succeeded")
	}
	c.must(sign(refs[0])...)
	later := func() time.Time { return time.Now().Add(2 * time.Hour) }
	for _, sub := range [][]string{
		{"root-verify", "-in", root},
		{"root-sign", "-in", root, "-out", root, "-key", refs[1], "-key-dir", filepath.Join(dir, "keys")},
	} {
		var stdout, stderr bytes.Buffer
		if code := rootCommand(c.ctx, sub, later, &stdout, &stderr); code != exitFailed {
			t.Errorf("%s of an expired root: exit %d", sub[0], code)
		}
	}
	if code, _, _ := c.run("metadata", "root-verify", "-in", filepath.Join(dir, "absent.json")); code != exitFailed {
		t.Errorf("a missing file: exit %d", code)
	}
}

// seedEnvironment migrates the database and returns the organisation and
// development environment that seed creates.
func seedEnvironment(t *testing.T, c cli, cfg string) (org, env string) {
	t.Helper()
	c.must("migrate", "-config", cfg)
	var app string
	for _, line := range strings.Split(c.must("seed", "-config", cfg, "-out", "-"), "\n") {
		switch {
		case strings.HasPrefix(line, "PLUX_DEV_ORGANIZATION="):
			org = strings.TrimPrefix(line, "PLUX_DEV_ORGANIZATION=")
		case strings.HasPrefix(line, "PLUX_DEV_APP="):
			app = strings.TrimPrefix(line, "PLUX_DEV_APP=")
		}
	}
	return org, developmentEnvironment(t, cfg, org, app)
}

// onlineKeysFile returns what 'metadata keys' prints, for fresh keys.
func onlineKeysFile(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, role := range []string{updatemeta.RoleTargets, updatemeta.RoleSnapshot, updatemeta.RoleTimestamp} {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(role + " " + updatemeta.KeyID(pub) + " " + updatemeta.AlgEd25519 + " " + base64.StdEncoding.EncodeToString(pub) + "\n")
	}
	return b.String()
}
