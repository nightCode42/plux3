// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nightCode42/plux3/backend/internal/importer"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

const importUsage = `Usage: plux import [-C dir] [--json]
       plux import openapi <file> [-o out]
       plux import graphql <schema.graphql> <operations.graphql>... [-o out]

Without a subcommand, replaces the server's drafts with the local project, one snapshot per draft.

'openapi' and 'graphql' read a source document offline and write a data-source fragment
(dataSources, types, variables and baseUrls, as canonical JSON) for app.json or a plugin.
A construct with no Plux type is reported with its location and its operation is left out.`

// importSource runs the import subcommands openapi and graphql (DAT-002).
func (e env) importSource(kind string, args []string) int {
	set := flag.NewFlagSet("import "+kind, flag.ContinueOnError)
	out := set.String("o", "", "write the result to this `file` instead of standard output")
	usage := "Usage: plux import openapi <file> [-o out]"
	least := 1
	if kind == "graphql" {
		usage = "Usage: plux import graphql <schema.graphql> <operations.graphql>... [-o out]"
		least = 2
	}
	pos, code, ok := parseInterleaved(set, args, e.stderr, usage)
	if !ok {
		return code
	}
	if len(pos) < least || (kind == "openapi" && len(pos) != 1) {
		set.Usage()
		return exitUsage
	}
	res, err := runImport(kind, pos)
	if err != nil {
		return e.fail("import "+kind, err)
	}
	for _, d := range res.Diagnostics {
		_, _ = fmt.Fprintln(e.stderr, d.String())
	}
	if res.Diagnostics.HasErrors() || res.Fragment == nil {
		_, _ = fmt.Fprintf(e.stderr, "%s import %s: nothing written: %d errors, %d warnings\n", name, kind,
			res.Diagnostics.Count(plxerr.SeverityError), res.Diagnostics.Count(plxerr.SeverityWarning))
		return exitFailed
	}
	data, err := res.JSON()
	if err != nil {
		return e.fail("import "+kind, err)
	}
	if *out == "" || *out == "-" {
		if _, err := e.stdout.Write(data); err != nil {
			return e.fail("import "+kind, err)
		}
		return exitOK
	}
	if err := writeFile(*out, data); err != nil {
		return e.fail("import "+kind, err)
	}
	_, _ = fmt.Fprintf(e.stdout, "Wrote %s.\n", *out)
	return exitOK
}

// runImport reads the source files named on the command line and imports
// them.
func runImport(kind string, pos []string) (importer.Result, error) {
	schema, err := readSource(pos[0])
	if err != nil {
		return importer.Result{}, err
	}
	if kind == "openapi" {
		return importer.ImportOpenAPI(context.Background(), schema.File, schema.Data), nil
	}
	var docs []importer.Source
	for _, p := range pos[1:] {
		d, err := readSource(p)
		if err != nil {
			return importer.Result{}, err
		}
		docs = append(docs, d)
	}
	return importer.ImportGraphQL(schema, docs), nil
}

// parseInterleaved parses flags that may come before, between or after the
// positional arguments, and returns the positional ones.
func parseInterleaved(set *flag.FlagSet, args []string, stderr io.Writer, usage string) ([]string, int, bool) {
	set.SetOutput(stderr)
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), usage+"\n\nFlags:\n")
		set.PrintDefaults()
	}
	var pos []string
	for {
		if err := set.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, exitOK, false
			}
			return nil, exitUsage, false
		}
		if set.NArg() == 0 {
			return pos, exitOK, true
		}
		pos = append(pos, set.Arg(0))
		args = set.Args()[1:]
	}
}

const mockUsage = "Usage: plux mock <openapi-file> [--addr 127.0.0.1:0] [--seed n]\n\n" +
	"Serves every operation of an OpenAPI document from its examples, or from values generated\n" +
	"from its schemas with a fixed seed, so the same seed always gives the same responses.\n" +
	"It listens on the loopback interface unless --addr names another address."

// mockCmd runs plux mock until interrupted (TST-004).
func (e env) mockCmd(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return e.mockServe(ctx, args)
}

// mockServe serves the mock until ctx ends. It prints the address it listens
// on as "Serving <file> on http://<addr>" on standard output.
func (e env) mockServe(ctx context.Context, args []string) int {
	set := flag.NewFlagSet("mock", flag.ContinueOnError)
	addr := set.String("addr", "127.0.0.1:0", "the `address` to listen on")
	seed := set.Uint64("seed", 1, "the `seed` of generated values")
	pos, code, ok := parseInterleaved(set, args, e.stderr, mockUsage)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		set.Usage()
		return exitUsage
	}
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return e.fail("mock", err)
	}
	m, diags := importer.NewMock(ctx, pos[0], data, *seed)
	if m == nil {
		for _, d := range diags {
			_, _ = fmt.Fprintln(e.stderr, d.String())
		}
		return exitFailed
	}
	if !loopback(*addr) {
		_, _ = fmt.Fprintf(e.stderr, "%s mock: warning: %s is not a loopback address; anyone who can reach it can call the mock\n", name, *addr)
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", *addr)
	if err != nil {
		return e.fail("mock", err)
	}
	srv := &http.Server{Handler: m, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
	_, _ = fmt.Fprintf(e.stdout, "Serving %s on http://%s\n", pos[0], ln.Addr())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return e.fail("mock", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return e.fail("mock", err)
	}
	return exitOK
}

// loopback reports whether addr listens on the loopback interface only.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// readSource reads an input file, keeping its path as the name diagnostics use.
func readSource(path string) (importer.Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the developer's own file
	if err != nil {
		return importer.Source{}, fmt.Errorf("read the source: %w", err)
	}
	return importer.Source{File: path, Data: data}, nil
}
