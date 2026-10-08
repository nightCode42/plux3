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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// Verifies: SEC-050, SEC-051.
// The metadata command prints the environment's online keys and stores a
// root signed offline, after verifying it; a repeat or an unsigned root is
// refused, and misuse is a usage error.
func TestMetadataCommand_SEC_051(t *testing.T) {
	url := storagetest.SchemaURL(t)
	dir := t.TempDir()
	path := configFile(t, "server:\n  publicBaseURL: \"https://p.example\"\ndatabase:\n  url: \""+url+"\"\n"+
		"objectStorage:\n  directory: \""+filepath.Join(dir, "objects")+"\"\n"+
		"signing:\n  directory: \""+filepath.Join(dir, "keys")+"\"\n")
	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	if code := run(ctx, []string{"migrate", "-config", path}, &stdout, &stderr); code != exitOK {
		t.Fatalf("migrate: %d; %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run(ctx, []string{"seed", "-config", path, "-out", "-"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("seed: %d; %s", code, stderr.String())
	}
	var org, app string
	for _, line := range strings.Split(stdout.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "PLUX_DEV_ORGANIZATION="):
			org = strings.TrimPrefix(line, "PLUX_DEV_ORGANIZATION=")
		case strings.HasPrefix(line, "PLUX_DEV_APP="):
			app = strings.TrimPrefix(line, "PLUX_DEV_APP=")
		}
	}
	env := developmentEnvironment(t, path, org, app)

	stdout.Reset()
	args := []string{"-config", path, "-organization", org, "-environment", env}
	if code := run(ctx, append([]string{"metadata", "keys"}, args...), &stdout, &stderr); code != exitOK {
		t.Fatalf("keys: %d; %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("keys printed %q", stdout.String())
	}
	root := updatemeta.Root{
		Type: updatemeta.RoleRoot, Version: 1, Expires: updatemeta.FormatTime(time.Now().Add(24 * time.Hour)),
		SpecVersion: updatemeta.SpecVersion, Keys: map[string]updatemeta.Key{},
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 4 || f[2] != updatemeta.AlgEd25519 {
			t.Fatalf("a key line: %q", line)
		}
		root.Keys[f[1]] = updatemeta.Key{Alg: f[2], Public: f[3]}
		keys := updatemeta.RoleKeys{KeyIDs: []string{f[1]}, Threshold: 1}
		switch f[0] {
		case updatemeta.RoleTargets:
			root.Roles.Targets = keys
		case updatemeta.RoleSnapshot:
			root.Roles.Snapshot = keys
		case updatemeta.RoleTimestamp:
			root.Roles.Timestamp = keys
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := updatemeta.KeyID(pub)
	root.Keys[id] = updatemeta.Key{Alg: updatemeta.AlgEd25519, Public: base64.StdEncoding.EncodeToString(pub)}
	root.Roles.Root = updatemeta.RoleKeys{KeyIDs: []string{id}, Threshold: 1}
	canonical, err := updatemeta.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := updatemeta.Marshal(root, []updatemeta.Signature{{KeyID: id, Alg: updatemeta.AlgEd25519, Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))}})
	if err != nil {
		t.Fatal(err)
	}
	unsigned, _ := updatemeta.Marshal(root, nil)
	good, bad := filepath.Join(dir, "root.json"), filepath.Join(dir, "unsigned.json")
	if err := os.WriteFile(good, signed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, unsigned, 0o600); err != nil {
		t.Fatal(err)
	}

	stderr.Reset()
	if code := run(ctx, append([]string{"metadata", "upload-root"}, append(args, bad)...), &stdout, &stderr); code != exitFailed ||
		!strings.Contains(stderr.String(), "threshold") {
		t.Errorf("an unsigned root: %d; %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run(ctx, append([]string{"metadata", "upload-root"}, append(args, good)...), &stdout, &stderr); code != exitOK ||
		!strings.Contains(stdout.String(), "stored root 1") {
		t.Errorf("upload-root: %d; %q; %s", code, stdout.String(), stderr.String())
	}
	if code := run(ctx, append([]string{"metadata", "upload-root"}, append(args, good)...), &stdout, &stderr); code != exitFailed {
		t.Errorf("the same root twice: exit %d", code)
	}
	for _, usage := range [][]string{
		{"metadata"}, {"metadata", "sign"}, {"metadata", "keys", "-config", path}, {"metadata", "upload-root", "-organization", org, "-environment", env},
	} {
		if code := run(ctx, usage, &stdout, &stderr); code != exitUsage {
			t.Errorf("%v: exit %d", usage, code)
		}
	}
}

// developmentEnvironment finds the identifier of an app's development
// environment.
func developmentEnvironment(t *testing.T, configPath, org, app string) string {
	t.Helper()
	cfg, err := config.Load(configPath, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), storage.Options{URL: cfg.Database.URL.Value(), MaxConnections: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	err = db.InTx(context.Background(), storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id::text FROM environments WHERE app_id = $1 AND NOT production ORDER BY key LIMIT 1`, app).Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
