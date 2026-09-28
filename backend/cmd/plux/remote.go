// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// Further exit codes of the server commands (CLI-007).
const (
	// exitAuth: no credential, or the server refused it.
	exitAuth = 3
	// exitUnavailable: the server could not be reached.
	exitUnavailable = 4
)

// Environment variables the server commands read.
const (
	serverEnv = "PLUX_SERVER"
	orgEnv    = "PLUX_ORGANIZATION"
)

// projectFile is where `plux init` records a project's server, organisation
// and app; it holds no secret.
const projectFile = "plux.json"

// projectConfig is the content of plux.json.
type projectConfig struct {
	Server       string `json:"server"`
	Organization string `json:"organization"`
	App          string `json:"app"`
	Environment  string `json:"environment,omitempty"`
}

// env is what every server command stands on.
type env struct {
	stdout, stderr io.Writer
	creds          credentials
	// httpClient is replaceable in tests.
	httpClient *http.Client
}

// newEnv returns the environment of a real run.
func newEnv(stdout, stderr io.Writer) env {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return env{stdout: stdout, stderr: stderr, creds: credentials{dir: filepath.Join(dir, "plux")}, httpClient: &http.Client{Timeout: 5 * time.Minute}}
}

// common are the flags of every server command.
type common struct {
	server, org, app, dir string
	json                  bool
}

func (c *common) register(set *flag.FlagSet, withApp bool) {
	set.StringVar(&c.server, "server", "", "the Plux Server `url` (default $PLUX_SERVER, then plux.json)")
	set.StringVar(&c.org, "org", "", "the organisation `id` (default $PLUX_ORGANIZATION, then plux.json)")
	set.BoolVar(&c.json, "json", false, "print the result as JSON on stdout")
	if withApp {
		set.StringVar(&c.app, "app", "", "the app `id` (default plux.json)")
		set.StringVar(&c.dir, "C", ".", "the project `directory`")
	}
}

// resolve fills unset values from the environment and plux.json.
func (c *common) resolve() error {
	var p projectConfig
	if c.dir != "" {
		if data, err := os.ReadFile(filepath.Join(c.dir, projectFile)); err == nil {
			if err := json.Unmarshal(data, &p); err != nil {
				return fmt.Errorf("read %s: %w", projectFile, err)
			}
		}
	}
	c.server = firstOf(c.server, os.Getenv(serverEnv), p.Server)
	c.org = firstOf(c.org, os.Getenv(orgEnv), p.Organization)
	c.app = firstOf(c.app, p.App)
	if c.server == "" {
		return usageError("no server: pass --server, set " + serverEnv + " or run plux init")
	}
	u, err := url.Parse(c.server)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return usageError("the server must be an http(s) URL")
	}
	c.server = strings.TrimSuffix(c.server, "/")
	return nil
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// usageError marks a failure as a usage error (exit 2).
type usageError string

func (e usageError) Error() string { return string(e) }

// authError marks a missing credential (exit 3).
type authError string

func (e authError) Error() string { return string(e) }

// clients are the typed API clients of one server.
type clients struct {
	identity pluxv1connect.IdentityServiceClient
	app      pluxv1connect.AppServiceClient
	plugin   pluxv1connect.PluginServiceClient
	document pluxv1connect.DocumentServiceClient
	publish  pluxv1connect.PublishServiceClient
	release  pluxv1connect.ReleaseServiceClient
	manifest pluxv1connect.ManifestServiceClient
	http     *http.Client
	server   string
	token    string
}

// connect builds clients for a server; with needToken, a missing token is
// an authError.
func (e env) connect(c common, needToken bool) (*clients, error) {
	token, err := e.creds.load(c.server)
	if err != nil {
		return nil, err
	}
	if needToken && token == "" {
		return nil, authError("not signed in to " + c.server + ": run plux login or set " + tokenEnv)
	}
	opts := connect.WithInterceptors(headers{token: token, org: c.org})
	return &clients{
		identity: pluxv1connect.NewIdentityServiceClient(e.httpClient, c.server, opts),
		app:      pluxv1connect.NewAppServiceClient(e.httpClient, c.server, opts),
		plugin:   pluxv1connect.NewPluginServiceClient(e.httpClient, c.server, opts),
		document: pluxv1connect.NewDocumentServiceClient(e.httpClient, c.server, opts),
		publish:  pluxv1connect.NewPublishServiceClient(e.httpClient, c.server, opts),
		release:  pluxv1connect.NewReleaseServiceClient(e.httpClient, c.server, opts),
		manifest: pluxv1connect.NewManifestServiceClient(e.httpClient, c.server, opts),
		http:     e.httpClient, server: c.server, token: token,
	}, nil
}

// headers adds the credential and organisation to every call.
type headers struct{ token, org string }

func (h headers) set(hd http.Header) {
	if h.token != "" {
		hd.Set("Authorization", "Bearer "+h.token)
	}
	if h.org != "" {
		hd.Set("X-Plux-Organization", h.org)
	}
}

func (h headers) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
		h.set(r.Header())
		return next(ctx, r)
	}
}

func (h headers) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, s connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, s)
		h.set(conn.RequestHeader())
		return conn
	}
}

func (headers) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// fail reports an error and returns its exit code.
func (e env) fail(cmd string, err error) int {
	_, _ = fmt.Fprintf(e.stderr, "%s %s: %v\n", name, cmd, err)
	var u usageError
	var a authError
	var ce *connect.Error
	switch {
	case errors.As(err, &u):
		return exitUsage
	case errors.As(err, &a):
		return exitAuth
	case errors.As(err, &ce) && (ce.Code() == connect.CodeUnauthenticated):
		return exitAuth
	case errors.As(err, &ce) && ce.Code() == connect.CodeUnavailable:
		return exitUnavailable
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return exitUnavailable
	}
	return exitFailed
}

// emit prints v as JSON with --json, else the text.
func (e env) emit(jsonOut bool, v any, text string) int {
	if jsonOut {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return e.fail("output", err)
		}
		return exitOK
	}
	if text != "" {
		_, _ = fmt.Fprint(e.stdout, strings.TrimSuffix(text, "\n")+"\n")
	}
	return exitOK
}

// parse parses a command's flags; positional arguments are returned.
func parse(set *flag.FlagSet, args []string, stderr io.Writer, usage string, positional int) ([]string, int, bool) {
	set.SetOutput(stderr)
	set.Usage = func() {
		_, _ = fmt.Fprint(set.Output(), usage+"\n\nFlags:\n")
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitOK, false
		}
		return nil, exitUsage, false
	}
	if positional >= 0 && set.NArg() != positional {
		set.Usage()
		return nil, exitUsage, false
	}
	return set.Args(), exitOK, true
}

// needApp checks that an app is known.
func needApp(c common) error {
	if c.app == "" {
		return usageError("no app: pass --app or run plux init")
	}
	return nil
}
