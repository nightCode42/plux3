// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/server"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// seed prepares an empty development installation for `make dev`
// (DEP-020): an administrator with a random password, an organisation,
// two apps — one for the loan calculator, one for the starter host app
// (apps/starter) — and a personal access token, written to files only
// the user can read. It runs against a migrated database (the server migrates on
// start) and refuses any installation whose signing backend may sign for
// production, so it cannot touch a real one.
func seed(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "plux-server.yaml", "configuration file")
	email := fs.String("email", "dev@plux.localhost", "the administrator's email address")
	org := fs.String("org", "acme", "the organisation key")
	app := fs.String("app", "demo", "the app key")
	starter := fs.String("starter", "starter", "the key of the starter host app's app")
	out := fs.String("out", ".plux-dev", "the `directory` the password and token are written to; - prints them once as KEY=value lines")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "%s: usage: seed [-config path] [-email address] [-org key] [-app key] [-starter key] [-out dir]\n", name)
		return exitUsage
	}
	cfg, err := config.Load(*path, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s:\n%s\n", name, *path, err)
		return exitUsage
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "%s seed: %s\n", name, err)
		return exitFailed
	}
	backend, err := server.BuildSigning(cfg)
	if err != nil {
		return fail(err)
	}
	if backend.AllowedInProduction() {
		return fail(fmt.Errorf("the %s signing backend is for real installations; seed only prepares development ones", backend.Name()))
	}
	db, err := storage.Open(ctx, storage.Options{URL: cfg.Database.URL.Value(), MaxConnections: 2, Log: logger(cfg, stderr)})
	if err != nil {
		return fail(err)
	}
	defer db.Close()
	set, err := cfg.LimitSet()
	if err != nil {
		return fail(err)
	}
	svc, err := server.BuildServices(ctx, cfg, db, cache.NewMemory(nil), set, backend, server.WorkDeps{})
	if err != nil {
		return fail(err)
	}
	values, err := seedInstallation(ctx, svc, *email, *org, *app, *starter)
	if err != nil {
		return fail(err)
	}
	if *out == "-" {
		// For a caller that runs seed inside a container and keeps the
		// values itself; they are shown this once, like any new token.
		for _, k := range []string{"organization", "app", "starter", "password", "token"} {
			_, _ = fmt.Fprintf(stdout, "PLUX_DEV_%s=%s\n", strings.ToUpper(k), values[k])
		}
		return exitOK
	}
	if err := writeSeed(*out, values); err != nil {
		return fail(err)
	}
	_, _ = fmt.Fprintf(stdout, "✓ seeded %s: organisation %s, apps %s and %s; password and token in %s\n", *email, *org, *app, *starter, *out)
	return exitOK
}

// writeSeed writes each value to a file only its owner can read.
func writeSeed(dir string, values map[string]string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	for file, value := range values {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(value+"\n"), 0o600); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	return nil
}

// seedInstallation creates the administrator, organisation, apps and
// token, and returns what the developer needs to use them.
func seedInstallation(ctx context.Context, svc *server.Services, email, org, app, starter string) (map[string]string, error) {
	password := make([]byte, 18)
	if _, err := rand.Read(password); err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	user, invitation, err := svc.Auth.Bootstrap(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("%w (an installation is seeded once; reset its database to seed again)", err)
	}
	if _, err := svc.Auth.AcceptInvitation(ctx, invitation, "Developer", hex.EncodeToString(password)); err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	id := auth.Identity{Kind: auth.KindUser, ID: user.ID, UserID: user.ID, Display: "Developer", SecondFactor: true, InstallationAdmin: true}
	o, err := svc.Tenancy.CreateOrganization(ctx, id, org, org)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	owner, err := svc.Auth.Resolve(ctx, id, o.ID)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	a, err := svc.Tenancy.CreateApp(ctx, owner, app, app)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	s, err := svc.Tenancy.CreateApp(ctx, owner, starter, starter)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	token, err := svc.Auth.CreateAccessToken(ctx, owner, "make dev", nil, 30*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	return map[string]string{"password": hex.EncodeToString(password), "token": token.Secret, "organization": o.ID, "app": a.ID, "starter": s.ID}, nil
}
