// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

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
	"syscall"

	"github.com/nightCode42/plux3/backend/internal/buildinfo"
	"github.com/nightCode42/plux3/backend/internal/cache"
	"github.com/nightCode42/plux3/backend/internal/config"
	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/observability"
	"github.com/nightCode42/plux3/backend/internal/server"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// name is the binary name used in output and usage text.
const name = "plux-server"

// usage lists the commands this binary accepts.
const usage = `Usage: plux-server <command> [flags]

Commands:
  serve             Run the roles named in the configuration
  config validate   Check a configuration file without connecting to anything
  migrate           Apply pending database migrations and exit
  bootstrap         Create the first installation administrator
  version           Print version information
  help              Show this help

Flags:
  -config <path>    Configuration file (default plux-server.yaml)
  -email <address>  The administrator's email address (bootstrap only)

Every value may also come from the environment; run
'plux-server config validate -h' for the variables that are read.
`

// Exit codes: 0 success, 1 a command failed, 2 usage or configuration
// error (CLI-007).
const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// main delegates to run so that the command logic is testable without
// terminating the test process.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the command given by args and returns the process exit
// code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "version", "--version":
		_, _ = fmt.Fprintln(stdout, name, buildinfo.Get())
		return exitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	case "serve":
		return serve(ctx, args[1:], stdout, stderr)
	case "migrate":
		return migrate(ctx, args[1:], stdout, stderr)
	case "bootstrap":
		return bootstrap(ctx, args[1:], stdout, stderr)
	case "config":
		return configCommand(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "%s: unknown command %q\n\n%s", name, args[0], usage)
		return exitUsage
	}
}

// flags parses the flags a command shares and returns the configuration
// path.
func flags(command string, args []string, stderr io.Writer) (string, error) {
	fs := flag.NewFlagSet(name+" "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "plux-server.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return *path, nil
}

// load reads and validates the configuration (SRV-008).
func load(command string, args []string, stderr io.Writer) (*config.Config, int) {
	path, err := flags(command, args, stderr)
	if err != nil {
		return nil, exitUsage
	}
	cfg, err := config.Load(path, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s:\n%s\n", name, path, err)
		return nil, exitUsage
	}
	return cfg, exitOK
}

// configCommand runs the config subcommands.
func configCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "validate" {
		_, _ = fmt.Fprintf(stderr, "%s: usage: config validate [-config <path>]\n", name)
		return exitUsage
	}
	cfg, code := load("config validate", args[1:], stderr)
	if code != exitOK {
		return code
	}
	_, _ = fmt.Fprintf(stdout, "✓ configuration is valid: %s\n", cfg)
	_, _ = fmt.Fprintf(stdout, "  environment variables read: %v\n", config.EnvironNames())
	return exitOK
}

// migrate applies the pending migrations and exits, for deployments
// that migrate in a separate step rather than on start (SRV-021).
func migrate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, code := load("migrate", args, stderr)
	if code != exitOK {
		return code
	}
	log := logger(cfg, stderr)
	db, err := storage.Open(ctx, storage.Options{
		URL:            cfg.Database.URL.Value(),
		MaxConnections: cfg.Database.MaxConnections,
		Log:            log,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	defer db.Close()
	migrations, err := storage.LoadMigrations(storage.Migrations())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	if err := jobs.Migrate(ctx, db.Pool()); err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	_, _ = fmt.Fprintf(stdout, "✓ %d migrations applied or already present\n", len(migrations))
	return exitOK
}

// bootstrap creates the first installation administrator and prints the
// one-time invitation with which they set a password. It refuses once an
// administrator exists, so it cannot add a second one unaudited.
func bootstrap(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "plux-server.yaml", "configuration file")
	email := fs.String("email", "", "the administrator's email address")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *email == "" {
		_, _ = fmt.Fprintf(stderr, "%s: usage: bootstrap -email <address> [-config <path>]\n", name)
		return exitUsage
	}
	cfg, err := config.Load(*path, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s:\n%s\n", name, *path, err)
		return exitUsage
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
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
	backend, err := server.BuildSigning(cfg)
	if err != nil {
		return fail(err)
	}
	// The cache only counts failed sign-ins, which bootstrap never has.
	services, err := server.BuildServices(ctx, cfg, db, cache.NewMemory(nil), set, backend, server.WorkDeps{})
	if err != nil {
		return fail(err)
	}
	user, invitation, err := services.Auth.Bootstrap(ctx, *email)
	if err != nil {
		return fail(err)
	}
	_, _ = fmt.Fprintf(stdout, "✓ created installation administrator %s (%s)\n", user.Email, user.ID)
	_, _ = fmt.Fprintf(stdout, "  invitation (shown once, valid for 7 days): %s\n", invitation)
	_, _ = fmt.Fprintln(stdout, "  accept it with IdentityService.AcceptInvitation to set a password")
	return exitOK
}

// serve runs the configured roles until the process is interrupted.
func serve(ctx context.Context, args []string, _, stderr io.Writer) int {
	cfg, code := load("serve", args, stderr)
	if code != exitOK {
		return code
	}
	log := logger(cfg, stderr)
	built, err := server.Build(ctx, cfg, log, buildinfo.Get().Version)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	defer built.Close()
	if err := built.Server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", name, err)
		return exitFailed
	}
	return exitOK
}

// logger builds the process logger from the configuration (OBS-003).
func logger(cfg *config.Config, w io.Writer) *slog.Logger {
	return observability.NewLogger(w, observability.LogOptions{
		Level:   cfg.Observability.LogLevel,
		Format:  cfg.Observability.LogFormat,
		Role:    server.RoleLabel(cfg),
		Version: buildinfo.Get().Version,
	})
}
