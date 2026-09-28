// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/bundle"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
)

// maxDocumentDepth bounds JSON nesting when comparing documents.
const maxDocumentDepth = 64

// init records a project's server, organisation and app in plux.json.
func (e env) initProject(args []string) int {
	var c common
	set := flag.NewFlagSet("init", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "development", "the environment `key` publishes go to")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux init --server url --org id --app id-or-key [-C dir] [--env key] [--json]\n\n"+
		"Writes plux.json, which names the server, organisation and app of the project; it holds no secret.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("init", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("init", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("init", err)
	}
	app, err := findApp(context.Background(), cl, c.app)
	if err != nil {
		return e.fail("init", err)
	}
	p := projectConfig{Server: c.server, Organization: firstOf(c.org, app.GetOrganizationId()), App: app.GetId(), Environment: *envKey}
	data, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(filepath.Join(c.dir, projectFile), append(data, '\n'), 0o644); err != nil { //nolint:gosec // G306: project configuration, no secret.
		return e.fail("init", err)
	}
	return e.emit(c.json, p, "Wrote "+filepath.Join(c.dir, projectFile)+" for app "+app.GetKey()+".")
}

// findApp resolves an app by ID or key.
func findApp(ctx context.Context, cl *clients, idOrKey string) (*pluxv1.App, error) {
	res, err := cl.app.ListApps(ctx, connect.NewRequest(&pluxv1.ListAppsRequest{Page: &pluxv1.Page{PageSize: 1000}}))
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	for _, a := range res.Msg.GetApps() {
		if a.GetId() == idOrKey || a.GetKey() == idOrKey {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no app %q in this organisation", idOrKey)
}

// environmentID resolves an environment key of the app.
func environmentID(ctx context.Context, cl *clients, app, key string) (string, error) {
	res, err := cl.app.ListEnvironments(ctx, connect.NewRequest(&pluxv1.ListEnvironmentsRequest{AppId: app, Page: &pluxv1.Page{PageSize: 1000}}))
	if err != nil {
		return "", err //nolint:wrapcheck // reported as is
	}
	for _, en := range res.Msg.GetEnvironments() {
		if en.GetKey() == key || en.GetId() == key {
			return en.GetId(), nil
		}
	}
	return "", usageError("the app has no environment " + strconv.Quote(key))
}

// doctor checks the setup, step by step, and fails when a step does.
func (e env) doctor(args []string) int {
	var c common
	set := flag.NewFlagSet("doctor", flag.ContinueOnError)
	c.register(set, true)
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux doctor [-C dir] [--json]\n\nChecks the project, the server, the credential and the app.", 0); !ok {
		return code
	}
	checks := e.doctorChecks(&c)
	failed := slices.ContainsFunc(checks, func(ch check) bool { return !ch.OK })
	var text strings.Builder
	for _, ch := range checks {
		mark := "ok  "
		if !ch.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&text, "%s %-10s %s\n", mark, ch.Name, ch.Detail)
	}
	code := e.emit(c.json, map[string]any{"ok": !failed, "checks": checks}, text.String())
	if failed && code == exitOK {
		return exitFailed
	}
	return code
}

// check is one step of doctor.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func newCheck(name string, err error, ok string) check {
	if err != nil {
		return check{Name: name, Detail: err.Error()}
	}
	return check{Name: name, OK: true, Detail: ok}
}

// doctorChecks runs the checks in order; each server check needs the
// one before it to have passed.
func (e env) doctorChecks(c *common) []check {
	ctx := context.Background()
	var out []check
	if _, err := os.Stat(filepath.Join(c.dir, projectFile)); err != nil {
		out = append(out, newCheck("project", err, ""))
	} else {
		out = append(out, newCheck("project", nil, projectFile+" found"), newCheck("validate", validateDir(c.dir), "no errors"))
	}
	var cl *clients
	steps := []struct {
		name, ok string
		run      func() error
	}{
		{"config", "found", c.resolve},
		{"server", "ready", func() error { return e.ready(ctx, c.server) }},
		{"credential", "present", func() (err error) { cl, err = e.connect(*c, true); return err }},
		{"sign-in", "accepted", func() error {
			_, err := cl.identity.GetCurrentUser(ctx, connect.NewRequest(&pluxv1.GetCurrentUserRequest{}))
			return err //nolint:wrapcheck // reported as is
		}},
		{"app", "readable", func() error {
			if c.app == "" {
				return usageError("no app in " + projectFile)
			}
			_, err := cl.app.GetApp(ctx, connect.NewRequest(&pluxv1.GetAppRequest{Id: c.app}))
			return err //nolint:wrapcheck // reported as is
		}},
	}
	for _, st := range steps {
		ch := newCheck(st.name, st.run(), st.ok)
		out = append(out, ch)
		if !ch.OK {
			break
		}
	}
	return out
}

// validateDir compiles a project and reports whether it has errors.
func validateDir(dir string) error {
	res := compiler.Compile(os.DirFS(dir), options(false))
	if res.Diagnostics.HasErrors() {
		return fmt.Errorf("%d errors; run plux validate", res.Diagnostics.Count(plxerr.SeverityError))
	}
	return nil
}

// ready asks the server's readiness endpoint.
func (e env) ready(ctx context.Context, server string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/readyz", nil)
	if err != nil {
		return fmt.Errorf("readyz: %w", err)
	}
	res, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("readyz: %w", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz answered %d", res.StatusCode)
	}
	return nil
}

// readProject reads the project's documents: every file except plux.json
// and hidden ones.
func readProject(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".") && path != "." {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || path == projectFile {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(dir, path)) //nolint:gosec // G304: the user's own project.
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		out[path] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the project: %w", err)
	}
	return out, nil
}

// exportDraft downloads the app's drafts in the Git layout.
func exportDraft(ctx context.Context, cl *clients, app string) (map[string][]byte, error) {
	stream, err := cl.document.ExportDraft(ctx, connect.NewRequest(&pluxv1.ExportDraftRequest{AppId: app}))
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	out := map[string][]byte{}
	for stream.Receive() {
		out[stream.Msg().GetPath()] = stream.Msg().GetContent()
	}
	if err := stream.Err(); err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	return out, nil
}

// importDraft replaces the app's drafts with the local project.
func importDraft(ctx context.Context, cl *clients, app string, files map[string][]byte) (*pluxv1.ImportDraftResponse, error) {
	stream := cl.document.ImportDraft(ctx)
	first := true
	for _, path := range slices.Sorted(mapKeys(files)) {
		m := &pluxv1.ImportDraftRequest{Path: path, Content: files[path]}
		if first {
			m.AppId, m.Session, first = app, "plux-cli", false
		}
		if err := stream.Send(m); err != nil && !errors.Is(err, io.EOF) {
			return nil, err //nolint:wrapcheck // reported as is
		}
	}
	res, err := stream.CloseAndReceive()
	if err != nil {
		return nil, err //nolint:wrapcheck // reported as is
	}
	return res.Msg, nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// sameDocument compares two files, JSON by canonical form.
func sameDocument(path string, a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	if !strings.HasSuffix(path, ".json") {
		return false
	}
	ca, err1 := jcs.Canonicalize(a, maxDocumentDepth)
	cb, err2 := jcs.Canonicalize(b, maxDocumentDepth)
	return err1 == nil && err2 == nil && bytes.Equal(ca, cb)
}

// diff lists what differs between the local project and the server's
// drafts.
func (e env) diff(args []string) int {
	var c common
	set := flag.NewFlagSet("diff", flag.ContinueOnError)
	c.register(set, true)
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux diff [-C dir] [--json]\n\n"+
		"Lists the files that the local project adds, changes or lacks against the server's drafts.\n"+
		"Exit codes: 0 no differences, 1 differences or a failure.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("diff", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("diff", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("diff", err)
	}
	local, err := readProject(c.dir)
	if err != nil {
		return e.fail("diff", err)
	}
	remote, err := exportDraft(context.Background(), cl, c.app)
	if err != nil {
		return e.fail("diff", err)
	}
	type change struct {
		Path   string `json:"path"`
		Change string `json:"change"`
	}
	changes := []change{}
	for _, p := range slices.Sorted(mapKeys(local)) {
		r, ok := remote[p]
		switch {
		case !ok:
			changes = append(changes, change{p, "added"})
		case !sameDocument(p, local[p], r):
			changes = append(changes, change{p, "changed"})
		}
	}
	for _, p := range slices.Sorted(mapKeys(remote)) {
		if _, ok := local[p]; !ok {
			changes = append(changes, change{p, "removed"})
		}
	}
	var text strings.Builder
	for _, ch := range changes {
		fmt.Fprintf(&text, "%-8s %s\n", ch.Change, ch.Path)
	}
	code := e.emit(c.json, map[string]any{"changes": changes}, text.String())
	if code == exitOK && len(changes) > 0 {
		return exitFailed
	}
	return code
}

// export writes the server's drafts into a directory.
func (e env) export(args []string) int {
	var c common
	set := flag.NewFlagSet("export", flag.ContinueOnError)
	c.register(set, true)
	out := set.String("o", "", "the output `directory` (default the project directory)")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux export [-C dir] [-o out-dir] [--json]\n\nWrites the server's drafts in the Git layout.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("export", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("export", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("export", err)
	}
	files, err := exportDraft(context.Background(), cl, c.app)
	if err != nil {
		return e.fail("export", err)
	}
	dir := firstOf(*out, c.dir)
	for _, p := range slices.Sorted(mapKeys(files)) {
		clean := filepath.Clean(filepath.FromSlash(p))
		if !filepath.IsLocal(clean) {
			return e.fail("export", fmt.Errorf("the server sent an unsafe path %q", p))
		}
		target := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // G301: project files.
			return e.fail("export", err)
		}
		if err := writeFile(target, files[p]); err != nil {
			return e.fail("export", err)
		}
	}
	return e.emit(c.json, map[string]any{"files": len(files), "directory": dir}, fmt.Sprintf("Wrote %d files to %s.", len(files), dir))
}

// importCmd replaces the server's drafts with the local project.
func (e env) importCmd(args []string) int {
	var c common
	set := flag.NewFlagSet("import", flag.ContinueOnError)
	c.register(set, true)
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux import [-C dir] [--json]\n\nReplaces the server's drafts with the local project, one snapshot per draft.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("import", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("import", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("import", err)
	}
	files, err := readProject(c.dir)
	if err != nil {
		return e.fail("import", err)
	}
	res, err := importDraft(context.Background(), cl, c.app, files)
	if err != nil {
		return e.fail("import", err)
	}
	return e.emit(c.json, map[string]any{"files": len(files), "revision": res.GetRevision()}, fmt.Sprintf("Imported %d files.", len(files)))
}

// published is one publish's outcome.
type published struct {
	Plugin      string               `json:"plugin"`
	State       string               `json:"state"`
	Version     int64                `json:"version"`
	Diagnostics []*pluxv1.Diagnostic `json:"diagnostics,omitempty"`
}

// publish imports the local project, publishes the app bundle and every
// plugin to an environment, and optionally creates and promotes a
// release. A publish that fails exits 1.
func (e env) publish(args []string) int {
	var c common
	set := flag.NewFlagSet("publish", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "", "the environment `key` (default plux.json, then development)")
	noImport := set.Bool("no-import", false, "publish the server's drafts as they are")
	ack := set.Bool("acknowledge-warnings", false, "publish despite warnings")
	release := set.Bool("release", false, "create a release of the new versions")
	promote := set.String("promote", "", "promote the release to `env[/channel]`")
	notes := set.String("notes", "", "release notes")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux publish [-C dir] [--env key] [--release] [--promote env[/channel]] [--json]\n\n"+
		"Uploads the local project, publishes the app bundle and every plugin, and waits for each.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("publish", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("publish", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("publish", err)
	}
	ctx := context.Background()
	if !*noImport {
		files, err := readProject(c.dir)
		if err != nil {
			return e.fail("publish", err)
		}
		if _, err := importDraft(ctx, cl, c.app, files); err != nil {
			return e.fail("publish", err)
		}
	}
	envID, err := environmentID(ctx, cl, c.app, firstOf(*envKey, projectEnvironment(c.dir), "development"))
	if err != nil {
		return e.fail("publish", err)
	}
	results, ok, err := publishAll(ctx, cl, c.app, envID, *ack)
	if err != nil {
		return e.fail("publish", err)
	}
	out := map[string]any{"ok": ok, "publishes": results}
	var text strings.Builder
	text.WriteString(publishText(results))
	if ok && (*release || *promote != "") {
		seq, err := releaseAndPromote(ctx, cl, c.app, envID, *notes, *promote, results)
		if err != nil {
			return e.fail("publish", err)
		}
		out["release"] = seq
		fmt.Fprintf(&text, "release %d\n", seq)
		if *promote != "" {
			fmt.Fprintf(&text, "promoted to %s\n", *promote)
		}
	}
	code := e.emit(c.json, out, text.String())
	if !ok && code == exitOK {
		return exitFailed
	}
	return code
}

// publishText describes each publish and its diagnostics.
func publishText(results []published) string {
	var text strings.Builder
	for _, r := range results {
		fmt.Fprintf(&text, "%-10s %-24s version %d\n", r.State, firstOf(r.Plugin, "(app bundle)"), r.Version)
		for _, d := range r.Diagnostics {
			fmt.Fprintf(&text, "  %s %s: %s\n", d.GetSeverity(), d.GetCode(), d.GetMessage())
		}
	}
	return text.String()
}

// publishAll publishes the app bundle and every plugin.
func publishAll(ctx context.Context, cl *clients, app, envID string, ack bool) ([]published, bool, error) {
	plugins, err := cl.plugin.ListPlugins(ctx, connect.NewRequest(&pluxv1.ListPluginsRequest{AppId: app, Page: &pluxv1.Page{PageSize: 1000}}))
	if err != nil {
		return nil, false, err //nolint:wrapcheck // reported as is
	}
	targets := append([]*pluxv1.Plugin{{Key: ""}}, plugins.Msg.GetPlugins()...)
	var results []published
	ok := true
	for _, p := range targets {
		r, err := publishOne(ctx, cl, app, p, envID, ack)
		if err != nil {
			return nil, false, err
		}
		ok = ok && r.State == "succeeded"
		results = append(results, r)
	}
	return results, ok, nil
}

// releaseAndPromote releases what was published and, when asked,
// promotes it to env[/channel].
func releaseAndPromote(ctx context.Context, cl *clients, app, envID, notes, promote string, results []published) (int64, error) {
	rel, err := cl.release.CreateRelease(ctx, connect.NewRequest(&pluxv1.CreateReleaseRequest{AppId: app, EnvironmentId: envID, Notes: notes, PluginVersions: latestVersions(results)}))
	if err != nil {
		return 0, err //nolint:wrapcheck // reported as is
	}
	seq := rel.Msg.GetRelease().GetSequence()
	if promote == "" {
		return seq, nil
	}
	key, channel, _ := strings.Cut(promote, "/")
	target, err := environmentID(ctx, cl, app, key)
	if err != nil {
		return 0, err
	}
	if _, err := cl.release.PromoteRelease(ctx, connect.NewRequest(&pluxv1.PromoteReleaseRequest{AppId: app, Sequence: seq, EnvironmentId: target, ChannelKey: channel})); err != nil {
		return 0, err //nolint:wrapcheck // reported as is
	}
	return seq, nil
}

// latestVersions pins the release to what was just published.
func latestVersions(rs []published) map[string]int64 {
	out := map[string]int64{}
	for _, r := range rs {
		out[r.Plugin] = r.Version
	}
	return out
}

// projectEnvironment is plux.json's environment, if any.
func projectEnvironment(dir string) string {
	var p projectConfig
	if data, err := os.ReadFile(filepath.Join(dir, projectFile)); err == nil { //nolint:gosec // G304: the user's own project.
		_ = json.Unmarshal(data, &p)
	}
	return p.Environment
}

// publishOne publishes one draft and waits for the job to finish.
func publishOne(ctx context.Context, cl *clients, app string, p *pluxv1.Plugin, envID string, ack bool) (published, error) {
	job, err := cl.publish.Publish(ctx, connect.NewRequest(&pluxv1.PublishRequest{AppId: app, PluginId: p.GetId(), EnvironmentId: envID, AcknowledgeWarnings: ack}))
	if err != nil {
		return published{}, err //nolint:wrapcheck // reported as is
	}
	watch, err := cl.publish.WatchPublish(ctx, connect.NewRequest(&pluxv1.WatchPublishRequest{JobId: job.Msg.GetJob().GetId()}))
	if err != nil {
		return published{}, err //nolint:wrapcheck // reported as is
	}
	last := job.Msg.GetJob()
	for watch.Receive() {
		last = watch.Msg().GetJob()
	}
	if err := watch.Err(); err != nil {
		return published{}, err //nolint:wrapcheck // reported as is
	}
	return published{Plugin: p.GetKey(), State: last.GetState(), Version: last.GetVersion(), Diagnostics: last.GetDiagnostics()}, nil
}

// baseline describes what pull wrote.
type baseline struct {
	App         string          `json:"app"`
	Environment string          `json:"environment"`
	Channel     string          `json:"channel"`
	Sequence    int64           `json:"releaseSequence"`
	Bundles     []baselineEntry `json:"bundles"`
	Keys        []baselineKey   `json:"keys"`
}

// baselineEntry is one bundle of a baseline with the signature publish
// made over its bundle hash, so the runtime verifies it under the
// embedded keys before loading it (SEC-052, ADR-0029).
type baselineEntry struct {
	Plugin    string `json:"plugin"`
	Version   int64  `json:"version"`
	SHA256    string `json:"sha256"`
	File      string `json:"file"`
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

type baselineKey struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Role      string `json:"role"`
	PublicKey string `json:"publicKey"`
}

// pull downloads the release a channel points at, with the root public
// keys, into the host project as its baseline (CLI-004, SYN-007). Every
// bundle is checked against the hash the server recorded for it.
func (e env) pull(args []string) int {
	var c common
	set := flag.NewFlagSet("pull", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "production", "the environment `key`")
	channel := set.String("channel", "production", "the channel `key`")
	out := set.String("o", filepath.Join("assets", "plux"), "the output `directory` in the host app (relative to the working directory)")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux pull [-C dir] [--env key] [--channel key] [-o dir] [--json]\n\n"+
		"Writes the channel's current release and the root public keys into the host app's\n"+
		"assets (run it from the host app, or pass -o):\n"+
		"<out>/bundles/<key>.pxb, <out>/keys.json and <out>/baseline.json.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("pull", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("pull", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("pull", err)
	}
	b, err := pullBaseline(context.Background(), cl, c, *envKey, *channel, *out)
	if err != nil {
		return e.fail("pull", err)
	}
	return e.emit(c.json, b, fmt.Sprintf("Pulled release %d (%d bundles, %d keys) into %s.", b.Sequence, len(b.Bundles), len(b.Keys), *out))
}

func pullBaseline(ctx context.Context, cl *clients, c common, envKey, channel, dir string) (baseline, error) {
	envID, err := environmentID(ctx, cl, c.app, envKey)
	if err != nil {
		return baseline{}, err
	}
	seq, err := channelSequence(ctx, cl, envID, channel)
	if err != nil {
		return baseline{}, err
	}
	if seq == 0 {
		return baseline{}, fmt.Errorf("channel %s/%s has no release", envKey, channel)
	}
	rel, err := cl.release.GetRelease(ctx, connect.NewRequest(&pluxv1.GetReleaseRequest{AppId: c.app, Sequence: seq}))
	if err != nil {
		return baseline{}, err //nolint:wrapcheck // reported as is
	}
	out := baseline{App: c.app, Environment: envKey, Channel: channel, Sequence: seq, Bundles: []baselineEntry{}, Keys: []baselineKey{}}
	if err := os.MkdirAll(filepath.Join(dir, "bundles"), 0o755); err != nil { //nolint:gosec // G301: project files.
		return baseline{}, fmt.Errorf("create %s: %w", dir, err)
	}
	if out.Bundles, err = cl.writeBundles(ctx, dir, rel.Msg.GetVersions()); err != nil {
		return baseline{}, err
	}
	keys, err := cl.manifest.GetRootKeys(ctx, connect.NewRequest(&pluxv1.GetRootKeysRequest{AppId: c.app, Environment: envKey}))
	if err != nil {
		return baseline{}, err //nolint:wrapcheck // reported as is
	}
	for _, k := range keys.Msg.GetKeys() {
		out.Keys = append(out.Keys, baselineKey{KeyID: k.GetKeyId(), Algorithm: k.GetAlgorithm(), Role: k.GetRole(), PublicKey: hex.EncodeToString(k.GetPublicKey())})
	}
	if len(out.Keys) == 0 {
		return baseline{}, errors.New("the environment has no signing key yet")
	}
	for name, v := range map[string]any{"keys.json": out.Keys, "baseline.json": out} {
		data, _ := json.MarshalIndent(v, "", "  ")
		if err := writeFile(filepath.Join(dir, name), append(data, '\n')); err != nil {
			return baseline{}, err
		}
	}
	return out, nil
}

// channelSequence is the release a channel points at, 0 for none.
func channelSequence(ctx context.Context, cl *clients, envID, channel string) (int64, error) {
	chs, err := cl.app.ListChannels(ctx, connect.NewRequest(&pluxv1.ListChannelsRequest{EnvironmentId: envID, Page: &pluxv1.Page{PageSize: 1000}}))
	if err != nil {
		return 0, err //nolint:wrapcheck // reported as is
	}
	for _, ch := range chs.Msg.GetChannels() {
		if ch.GetKey() == channel {
			return ch.GetReleaseSequence(), nil
		}
	}
	return 0, nil
}

// writeBundles downloads and writes a release's bundles; the app bundle
// is bundles/_app.pxb, since no plugin key starts with an underscore.
func (cl *clients) writeBundles(ctx context.Context, dir string, versions []*pluxv1.PluginVersion) ([]baselineEntry, error) {
	out := []baselineEntry{}
	for _, v := range versions {
		data, err := cl.download(ctx, v.GetBundleSha256())
		if err != nil {
			return nil, err
		}
		name := firstOf(v.GetPluginKey(), "_app") + ".pxb"
		if err := writeFile(filepath.Join(dir, "bundles", name), data); err != nil {
			return nil, err
		}
		out = append(out, baselineEntry{
			Plugin: v.GetPluginKey(), Version: v.GetVersion(), SHA256: v.GetBundleSha256(), File: "bundles/" + name,
			KeyID: v.GetKeyId(), Algorithm: v.GetAlgorithm(), Signature: base64.StdEncoding.EncodeToString(v.GetSignature()),
		})
	}
	return out, nil
}

// download fetches a bundle by its hash and checks it.
func (cl *clients) download(ctx context.Context, sha string) ([]byte, error) {
	if len(sha) != 64 {
		return nil, fmt.Errorf("the server named an invalid bundle hash %q", sha)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.server+"/v1/objects/bundles/"+sha[:2]+"/"+sha, nil)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	res, err := cl.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", sha, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", sha, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", sha, err)
	}
	// The bundle hash covers the header and the section directory, which
	// holds every section's hash (BND-005); ReadStructure checks both.
	b, err := bundle.ReadStructure(data)
	if err != nil || hex.EncodeToString(b.Hash[:]) != sha {
		return nil, fmt.Errorf("bundle %s does not match its hash", sha)
	}
	return data, nil
}

// releaseMove promotes a release, or rolls a channel back to one.
func (e env) releaseMove(ctx context.Context, cl *clients, c common, sub, arg, envID, envKey, channel, notes string) int {
	if envID == "" {
		return e.fail("release", usageError("--env is required"))
	}
	seq, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || seq <= 0 {
		return e.fail("release", usageError("the sequence must be a positive number"))
	}
	if sub == "promote" {
		if _, err := cl.release.PromoteRelease(ctx, connect.NewRequest(&pluxv1.PromoteReleaseRequest{AppId: c.app, Sequence: seq, EnvironmentId: envID, ChannelKey: channel})); err != nil {
			return e.fail("release", err)
		}
		return e.emit(c.json, map[string]any{"sequence": seq, "environment": envKey, "channel": firstOf(channel, "production")},
			fmt.Sprintf("Release %d is now on %s/%s.", seq, envKey, firstOf(channel, "production")))
	}
	res, err := cl.release.RollbackRelease(ctx, connect.NewRequest(&pluxv1.RollbackReleaseRequest{AppId: c.app, EnvironmentId: envID, ChannelKey: channel, ToSequence: seq, Notes: notes}))
	if err != nil {
		return e.fail("release", err)
	}
	n := res.Msg.GetRelease().GetSequence()
	return e.emit(c.json, map[string]any{"sequence": n, "rollbackOf": seq}, fmt.Sprintf("Release %d, with the content of %d, is now on %s/%s.", n, seq, envKey, firstOf(channel, "production")))
}

// releaseList lists releases, newest first.
func (e env) releaseList(ctx context.Context, cl *clients, c common, envID string) int {
	res, err := cl.release.ListReleases(ctx, connect.NewRequest(&pluxv1.ListReleasesRequest{AppId: c.app, EnvironmentId: envID, Page: &pluxv1.Page{PageSize: 100}}))
	if err != nil {
		return e.fail("release", err)
	}
	type item struct {
		Sequence   int64  `json:"sequence"`
		RollbackOf int64  `json:"rollbackOf,omitempty"`
		Notes      string `json:"notes"`
		AppBundle  string `json:"appBundleSha256"`
		CreatedBy  string `json:"createdBy"`
	}
	items := []item{}
	var text strings.Builder
	for _, r := range res.Msg.GetReleases() {
		items = append(items, item{r.GetSequence(), r.GetRollbackOf(), r.GetNotes(), r.GetAppBundleSha256(), r.GetCreatedBy().GetDisplay()})
		fmt.Fprintf(&text, "%5d  %-20s %s\n", r.GetSequence(), r.GetCreatedBy().GetDisplay(), r.GetNotes())
	}
	return e.emit(c.json, map[string]any{"releases": items}, text.String())
}

// keys prints an environment's public keys.
func (e env) keys(args []string) int {
	var c common
	set := flag.NewFlagSet("keys", flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "production", "the environment `key`")
	if _, code, ok := parse(set, args, e.stderr, "Usage: plux keys [-C dir] [--env key] [--json]\n\nLists the public keys an environment's manifests and bundles are signed with.", 0); !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("keys", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("keys", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("keys", err)
	}
	res, err := cl.manifest.GetRootKeys(context.Background(), connect.NewRequest(&pluxv1.GetRootKeysRequest{AppId: c.app, Environment: *envKey}))
	if err != nil {
		return e.fail("keys", err)
	}
	keys := []baselineKey{}
	var text strings.Builder
	for _, k := range res.Msg.GetKeys() {
		bk := baselineKey{KeyID: k.GetKeyId(), Algorithm: k.GetAlgorithm(), Role: k.GetRole(), PublicKey: hex.EncodeToString(k.GetPublicKey())}
		keys = append(keys, bk)
		fmt.Fprintf(&text, "%s  %s  %s  %s\n", bk.KeyID, bk.Algorithm, bk.Role, bk.PublicKey)
	}
	return e.emit(c.json, map[string]any{"keys": keys}, text.String())
}

// release lists, promotes and rolls back releases.
func (e env) release(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(e.stderr, "Usage: plux release list|promote|rollback [flags]\n")
		return exitUsage
	}
	var c common
	set := flag.NewFlagSet("release "+args[0], flag.ContinueOnError)
	c.register(set, true)
	envKey := set.String("env", "", "the environment `key`")
	channel := set.String("channel", "", "the channel `key` (default production)")
	notes := set.String("notes", "", "notes for a rollback")
	var usage string
	positional := 0
	switch args[0] {
	case "list":
		usage = "Usage: plux release list [-C dir] [--env key] [--json]"
	case "promote":
		usage, positional = "Usage: plux release promote --env key [--channel key] [-C dir] [--json] <sequence>", 1
	case "rollback":
		usage, positional = "Usage: plux release rollback --env key [--channel key] [--notes text] [-C dir] [--json] <to-sequence>", 1
	default:
		_, _ = fmt.Fprintf(e.stderr, "%s release: unknown subcommand %q\n", name, args[0])
		return exitUsage
	}
	rest, code, ok := parse(set, args[1:], e.stderr, usage, positional)
	if !ok {
		return code
	}
	if err := c.resolve(); err != nil {
		return e.fail("release", err)
	}
	if err := needApp(c); err != nil {
		return e.fail("release", err)
	}
	cl, err := e.connect(c, true)
	if err != nil {
		return e.fail("release", err)
	}
	ctx := context.Background()
	var envID string
	if *envKey != "" {
		if envID, err = environmentID(ctx, cl, c.app, *envKey); err != nil {
			return e.fail("release", err)
		}
	}
	if args[0] == "list" {
		return e.releaseList(ctx, cl, c, envID)
	}
	return e.releaseMove(ctx, cl, c, args[0], rest[0], envID, *envKey, *channel, *notes)
}
