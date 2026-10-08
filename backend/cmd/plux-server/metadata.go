// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/release"
	"github.com/nightCode42/plux3/backend/internal/server"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// maxRootFile bounds a root file read from disk.
const maxRootFile = 1 << 20

// metadataUsage is the usage of the metadata command.
const metadataUsage = `usage: metadata keys -organization <id> -environment <id> [-config <path>]
       metadata upload-root -organization <id> -environment <id> [-config <path>] <file>
`

// metadataCommand runs the update-metadata administration (SEC-050,
// SEC-051): `keys` prints the public online keys a root ceremony lists,
// and `upload-root` stores a root signed offline after verifying it. The
// server never holds a root key; the ceremony runs elsewhere.
func metadataCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "keys" && args[0] != "upload-root") {
		_, _ = fmt.Fprint(stderr, name+": "+metadataUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet(name+" metadata "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "plux-server.yaml", "configuration file")
	org := fs.String("organization", "", "the organisation's identifier")
	env := fs.String("environment", "", "the environment's identifier")
	if err := fs.Parse(args[1:]); err != nil || *org == "" || *env == "" ||
		(args[0] == "keys" && fs.NArg() != 0) || (args[0] == "upload-root" && fs.NArg() != 1) {
		_, _ = fmt.Fprint(stderr, name+": "+metadataUsage)
		return exitUsage
	}
	cfg, err := config.Load(*path, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s:\n%s\n", name, *path, err)
		return exitUsage
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "%s metadata: %s\n", name, err)
		return exitFailed
	}
	db, err := storage.Open(ctx, storage.Options{URL: cfg.Database.URL.Value(), MaxConnections: 2, Log: logger(cfg, stderr)})
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	releases, err := server.BuildReleases(ctx, cfg, db)
	if err != nil {
		return fail(err)
	}
	if releases == nil {
		return fail(fmt.Errorf("the installation has no object storage, so no releases"))
	}
	if args[0] == "keys" {
		return printOnlineKeys(ctx, releases, *org, *env, stdout, fail)
	}
	return uploadRoot(ctx, releases, *org, *env, fs.Arg(0), stdout, fail)
}

// printOnlineKeys prints the online keys of an environment, one a line:
// role, key identifier, algorithm and the public key in base64.
func printOnlineKeys(ctx context.Context, releases *release.Service, org, env string, stdout io.Writer, fail func(error) int) int {
	keys, err := releases.OnlineKeys(ctx, org, env)
	if err != nil {
		return fail(err)
	}
	for _, k := range keys {
		_, _ = fmt.Fprintf(stdout, "%s %s %s %s\n", k.Role, k.KeyID, k.Algorithm, base64.StdEncoding.EncodeToString(k.PublicKey))
	}
	return exitOK
}

// uploadRoot verifies a root file and stores it.
func uploadRoot(ctx context.Context, releases *release.Service, org, env, file string, stdout io.Writer, fail func(error) int) int {
	f, err := os.Open(file) //nolint:gosec // the operator names the file
	if err != nil {
		return fail(err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxRootFile+1))
	if err != nil {
		return fail(err)
	}
	if len(raw) > maxRootFile {
		return fail(fmt.Errorf("%s is larger than %d bytes", file, maxRootFile))
	}
	root, err := releases.UploadRoot(ctx, org, env, raw)
	if err != nil {
		return fail(err)
	}
	_, _ = fmt.Fprintf(stdout, "✓ stored root %d of environment %s, valid until %s\n", root.Version, env, root.Expires)
	return exitOK
}
