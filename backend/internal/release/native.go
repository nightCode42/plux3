// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/compiler"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// maxHostBuild is the longest host build identifier, as PluxConfig
// allows.
const maxHostBuild = 64

// HostBuild is one build of the host app with its native catalogue
// (ADR-0041).
type HostBuild struct {
	AppID string
	// Build is the string the build's devices report, such as 1.4.0+52.
	Build      string
	SHA256     string
	UploadedBy audit.Actor
	UploadedAt time.Time
	// Devices is how many registered devices report the build.
	Devices int64
}

// UploadNativeCatalogue stores the native catalogue of one host build
// (CLI-006). A build's catalogue never changes once stored: the same
// content again stores nothing and reports created false, and other
// content is refused with PLX-8032. The catalogue is validated as a native
// catalogue document and stored canonicalised. It needs release.publish:
// it changes which releases the build's devices receive, so every channel
// of the app has its manifests signed again (REL-080).
func (s *Service) UploadNativeCatalogue(ctx context.Context, p auth.Principal, appID, build string, data []byte) (HostBuild, bool, error) {
	if err := authorize(p, auth.ReleasePublish, appID); err != nil {
		return HostBuild{}, false, err
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return HostBuild{}, false, err
	}
	if err := validBuild(build); err != nil {
		return HostBuild{}, false, err
	}
	canonical, err := s.parseCatalogue(data)
	if err != nil {
		return HostBuild{}, false, err
	}
	sum := sha256.Sum256(canonical)
	var out HostBuild
	created := false
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		appRow, err := q.GetApp(ctx, app)
		if err != nil {
			return failure(err, "app")
		}
		rows, err := q.InsertNativeCatalogue(ctx, dbgen.InsertNativeCatalogueParams{
			OrganizationID: appRow.OrganizationID, AppID: app, HostBuild: build, Catalogue: canonical, Sha256: sum[:],
			UploadedByKind: p.Kind, UploadedByID: p.ID, UploadedBy: p.Display, UploadedAt: storage.Timestamp(s.now()),
		})
		switch {
		case err == nil:
			created = true
		case !errors.Is(err, pgx.ErrNoRows):
			return failure(err, "native catalogue")
		default:
			if rows, err = q.GetNativeCatalogue(ctx, dbgen.GetNativeCatalogueParams{AppID: app, HostBuild: build}); err != nil {
				return failure(err, "native catalogue")
			}
			if !bytes.Equal(rows.Sha256, sum[:]) {
				return plxerr.New(plxerr.ResourceExists, "host build %s already has another native catalogue; a build's catalogue never changes, so give the new build its own identifier", build)
			}
			out = hostBuildOf(rows, 0)
			return nil
		}
		out = hostBuildOf(rows, 0)
		if err := s.record(ctx, tx, p, audit.Entry{Action: audit.NativeCatalogueUploaded, TargetKind: "app", TargetID: appID, Detail: "host build " + build}); err != nil {
			return err
		}
		return s.resignApp(ctx, tx, q, app)
	})
	return out, created, err
}

// GetNativeCatalogue returns a host build with its canonical catalogue.
func (s *Service) GetNativeCatalogue(ctx context.Context, p auth.Principal, appID, build string) (HostBuild, []byte, error) {
	if err := authorize(p, auth.AppRead, appID); err != nil {
		return HostBuild{}, nil, err
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return HostBuild{}, nil, err
	}
	var out HostBuild
	var data []byte
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetNativeCatalogue(ctx, dbgen.GetNativeCatalogueParams{AppID: app, HostBuild: build})
		if err != nil {
			return failure(err, "host build")
		}
		counts, err := deviceCounts(ctx, q, app)
		if err != nil {
			return err
		}
		out, data = hostBuildOf(row, counts[build]), row.Catalogue
		return nil
	})
	return out, data, err
}

// ListHostBuilds lists an app's host builds, newest upload first, after
// the cursor, with how many devices report each.
func (s *Service) ListHostBuilds(ctx context.Context, p auth.Principal, appID string, before storage.Cursor, size int32) ([]HostBuild, error) {
	if err := authorize(p, auth.AppRead, appID); err != nil {
		return nil, err
	}
	app, err := parseID(appID, "app")
	if err != nil {
		return nil, err
	}
	t, key := before.Time, before.Key
	if t.IsZero() {
		t, key = s.now().Add(time.Hour), "\U0010ffff"
	}
	var out []HostBuild
	err = s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		rows, err := q.ListNativeCatalogues(ctx, dbgen.ListNativeCataloguesParams{
			AppID: app, BeforeTime: storage.Timestamp(t), BeforeBuild: key, PageSize: size,
		})
		if err != nil {
			return failure(err, "host build")
		}
		counts, err := deviceCounts(ctx, q, app)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, hostBuildOf(r, counts[r.HostBuild]))
		}
		return nil
	})
	return out, err
}

// validBuild checks a host build identifier: non-empty, at most 64
// characters, printable and without spaces.
func validBuild(build string) error {
	if build == "" || len(build) > maxHostBuild || strings.IndexFunc(build, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsSpace(r) }) >= 0 {
		return plxerr.New(plxerr.InvalidFormat, "the host build must be 1 to %d printable characters without spaces", maxHostBuild)
	}
	return nil
}

// parseCatalogue validates a native catalogue document and returns its
// canonical form.
func (s *Service) parseCatalogue(data []byte) ([]byte, error) {
	src, diags := schema.NewLoader(s.validator, schema.DefaultMigrator(), s.o.Limits).
		ParseDocument("plux.catalogue.json", data, schema.KindNativeCatalogue)
	if diags.HasErrors() || src == nil {
		msg := "the catalogue is not a native catalogue document"
		if len(diags) > 0 {
			msg += ": " + diags[0].Path + ": " + diags[0].Message
		}
		return nil, plxerr.New(plxerr.InvalidFormat, "%s", msg)
	}
	return src.Canonical, nil
}

// buildCatalogue is a stored host build's decoded catalogue.
type buildCatalogue struct {
	build     string
	catalogue *schema.NativeCatalogueDocument
}

// catalogues decodes every host build catalogue of an app, by build.
func catalogues(ctx context.Context, q *dbgen.Queries, app pgtype.UUID) ([]buildCatalogue, error) {
	rows, err := q.ListAllNativeCatalogues(ctx, app)
	if err != nil {
		return nil, failure(err, "native catalogue")
	}
	out := make([]buildCatalogue, 0, len(rows))
	for _, r := range rows {
		var doc schema.NativeCatalogueDocument
		if err := json.Unmarshal(r.Catalogue, &doc); err != nil {
			return nil, fmt.Errorf("release: host build %s: %w", r.HostBuild, err)
		}
		out = append(out, buildCatalogue{build: r.HostBuild, catalogue: &doc})
	}
	return out, nil
}

// nativeUses decodes a version's or release's stored native uses.
func nativeUses(raw []byte) ([]compiler.NativeUse, error) {
	var out []compiler.NativeUse
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("release: native uses: %w", err)
	}
	return out, nil
}

// encodeUses is the stored form of native uses: a JSON array, never null.
func encodeUses(uses []compiler.NativeUse) ([]byte, error) {
	if uses == nil {
		uses = []compiler.NativeUse{}
	}
	b, err := json.Marshal(uses)
	if err != nil {
		return nil, fmt.Errorf("release: native uses: %w", err)
	}
	return b, nil
}

// unionUses merges the native uses of a release's versions, sorted and
// unique; the first version naming an entry gives its types.
func unionUses(versions []dbgen.PluginVersion) ([]compiler.NativeUse, error) {
	var out []compiler.NativeUse
	for _, v := range versions {
		uses, err := nativeUses(v.NativeUses)
		if err != nil {
			return nil, err
		}
		out = append(out, uses...)
	}
	slices.SortStableFunc(out, func(a, b compiler.NativeUse) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	return slices.CompactFunc(out, func(a, b compiler.NativeUse) bool { return a.Kind == b.Kind && a.Name == b.Name }), nil
}

// missing names the uses a catalogue lacks or declares differently, as
// "route profile".
func missing(uses []compiler.NativeUse, c *schema.NativeCatalogueDocument) []string {
	var out []string
	for _, u := range uses {
		if _, same := u.In(c); !same {
			out = append(out, u.Kind+" "+u.Name)
		}
	}
	return out
}

// fallbackFor is the newest release before sequence that a host build
// can run, of the releases a channel of env may receive: in a production
// environment only releases once promoted to production. Zero when none.
func fallbackFor(all []dbgen.Release, sequence int64, production bool, c *schema.NativeCatalogueDocument) (int64, error) {
	var out int64
	for _, r := range all {
		if r.Sequence >= sequence || (production && !r.Production) {
			continue
		}
		uses, err := nativeUses(r.NativeUses)
		if err != nil {
			return 0, err
		}
		if compiler.Compatible(uses, c) {
			out = max(out, r.Sequence)
		}
	}
	return out, nil
}

// resignApp asks the worker to sign every channel of an app again, when a
// new host build changes which releases its devices receive.
func (s *Service) resignApp(ctx context.Context, tx pgx.Tx, q *dbgen.Queries, app pgtype.UUID) error {
	channels, err := q.ListAppChannels(ctx, app)
	if err != nil {
		return failure(err, "channel")
	}
	for _, ch := range channels {
		if ch.ReleaseSequence == 0 {
			continue
		}
		if err := s.enqueueManifest(ctx, tx, ch); err != nil {
			return err
		}
	}
	return nil
}

func deviceCounts(ctx context.Context, q *dbgen.Queries, app pgtype.UUID) (map[string]int64, error) {
	rows, err := q.CountDevicesByHostBuild(ctx, app)
	if err != nil {
		return nil, failure(err, "device")
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.HostBuild] = r.Devices
	}
	return out, nil
}

func hostBuildOf(r dbgen.NativeCatalogue, devices int64) HostBuild {
	return HostBuild{
		AppID: storage.ID(r.AppID), Build: r.HostBuild, SHA256: hex.EncodeToString(r.Sha256),
		UploadedBy: audit.Actor{Kind: r.UploadedByKind, ID: r.UploadedByID, Display: r.UploadedBy},
		UploadedAt: storage.Time(r.UploadedAt), Devices: devices,
	}
}
