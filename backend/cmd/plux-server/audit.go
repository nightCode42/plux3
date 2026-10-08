// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/server"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// auditUsage is the usage of the audit command.
const auditUsage = "usage: audit verify [--org <id>] [--json] [-config <path>]"

// auditCommand runs the audit subcommands.
func auditCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, auditUsage)
		return exitUsage
	}
	return auditVerify(ctx, args[1:], stdout, stderr)
}

// auditVerify walks each organisation's audit chain and the signed
// checkpoints over it, and reports the first entry or checkpoint that
// does not hold (SEC-140, SEC-141). It exits non-zero when one does not.
func auditVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" audit verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "plux-server.yaml", "configuration file")
	org := fs.String("org", "", "verify this organisation only")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, auditUsage)
		return exitUsage
	}
	if *org != "" {
		if _, err := storage.UUID(*org); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: --org %q is not an organisation identifier\n", name, *org)
			return exitUsage
		}
	}
	cfg, err := config.Load(*path, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s:\n%s\n", name, *path, err)
		return exitUsage
	}
	report, err := verifyAudit(ctx, cfg, *org, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
			return exitFailed
		}
	} else {
		printAuditReport(stdout, report)
	}
	if !report.OK() {
		return exitFailed
	}
	return exitOK
}

// verifyAudit opens what verification reads and runs it. Verification
// needs the audit key's public half only, which the signing backend
// gives without exposing the private one.
func verifyAudit(ctx context.Context, cfg *config.Config, org string, stderr io.Writer) (server.AuditReport, error) {
	db, err := storage.Open(ctx, storage.Options{URL: cfg.Database.URL.Value(), MaxConnections: 2, Log: logger(cfg, stderr)})
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("%w", err)
	}
	defer db.Close()
	set, err := cfg.LimitSet()
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("%w", err)
	}
	backend, err := server.BuildSigning(cfg)
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("%w", err)
	}
	pub, keyID, err := backend.PublicKey(ctx, cfg.Signing.Keys.Audit)
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("the audit checkpoint key %q: %w", cfg.Signing.Keys.Audit, err)
	}
	services, err := server.BuildServices(ctx, cfg, db, cache.NewMemory(nil), set, backend, server.WorkDeps{})
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("%w", err)
	}
	report, err := server.VerifyAudit(ctx, services, db, audit.PublicKeys{keyID: pub}, org)
	if err != nil {
		return server.AuditReport{}, fmt.Errorf("%w", err)
	}
	return report, nil
}

// printAuditReport writes the report for a person.
func printAuditReport(w io.Writer, r server.AuditReport) {
	var entries int64
	var checkpoints int
	for _, o := range r.Organizations {
		entries += o.Entries
		checkpoints += o.Checkpoints
		if o.Break == nil {
			_, _ = fmt.Fprintf(w, "  organisation %s: %d entries, %d checkpoints: ok\n", o.OrganizationID, o.Entries, o.Checkpoints)
			continue
		}
		_, _ = fmt.Fprintf(w, "✗ organisation %s: broken at %s %d: %s\n", o.OrganizationID, o.Break.Kind, o.Break.Sequence, o.Break.Reason)
	}
	if r.OK() {
		_, _ = fmt.Fprintf(w, "✓ audit verified: %d organisations, %d entries, %d checkpoints\n", len(r.Organizations), entries, checkpoints)
		return
	}
	_, _ = fmt.Fprintf(w, "✗ audit verification failed\n")
}
