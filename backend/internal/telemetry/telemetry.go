// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package telemetry ingests the runtime's batched events (Appendix G.2)
// and keeps them for a bounded time. P2 provides the endpoint,
// validation and storage; dashboards and rollups arrive in P9.
//
// Events never carry payloads: fields are flat, scalar and small, and a
// field whose name marks it as sensitive is refused rather than stored
// (SCH-012, ANL-003).
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// Retention is how long events are kept.
const Retention = 30 * 24 * time.Hour

// Bounds of one event's fields.
const (
	maxFieldsBytes = 4096
	maxFields      = 32
	maxText        = 256
)

// names is the event catalogue of Appendix G.2.
var names = map[string]bool{
	"session_start": true, "session_end": true, "screen_view": true, "render_perf": true, "action_run": true,
	"api_call": true, "function_call": true, "sync_result": true, "error": true, "experiment_exposure": true,
	"rasp_detection": true, "custom": true,
}

// fieldName is the form of a field name.
var fieldName = regexp.MustCompile(`^[a-z][A-Za-z0-9_]{0,63}$`)

// sensitiveWords mark a field name that must never be stored.
var sensitiveWords = []string{"password", "passcode", "secret", "token", "pin", "otp", "cvv", "card", "iban", "ssn", "email", "phone"}

// IDs generates identifiers.
type IDs interface {
	New() (string, error)
}

// Options configures a Service.
type Options struct {
	DB     *storage.DB
	IDs    IDs
	Limits limits.Set
	Now    func() time.Time
}

// Service ingests and lists events.
type Service struct {
	o   Options
	now func() time.Time
}

// NewService returns the service.
func NewService(o Options) (*Service, error) {
	if o.DB == nil || o.IDs == nil {
		return nil, errors.New("telemetry: a database and identifiers are required")
	}
	if o.Limits.IsZero() {
		o.Limits = limits.Defaults()
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Service{o: o, now: now}, nil
}

// Event is one runtime event.
type Event struct {
	ID, Name, DeviceID, PluginKey, Route string
	Time                                 time.Time
	ReleaseSequence                      int64
	// Fields is a flat JSON object of scalar values.
	Fields []byte
}

// Source is the device that sent a batch.
type Source struct {
	OrganizationID, AppID, EnvironmentID, DeviceID string
}

// Ingest stores the valid events of a batch and reports each refused
// one as a diagnostic whose path is its index. The device is always the
// sender; an event naming another device is refused.
func (s *Service) Ingest(ctx context.Context, src Source, events []Event) (int, plxerr.Diagnostics, error) {
	if max := s.o.Limits.Get(limits.TelemetryEventsPerRequest); int64(len(events)) > max {
		return 0, nil, plxerr.New(plxerr.LimitExceeded, "%d events, more than %s = %d", len(events), limits.TelemetryEventsPerRequest, max)
	}
	var diags plxerr.Diagnostics
	accepted := 0
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: src.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		for i, e := range events {
			fields, err := s.check(src, e)
			if err != nil {
				diags = append(diags, plxerr.NewDiagnostic(plxerr.InvalidFormat, plxerr.Location{Path: fmt.Sprintf("/events/%d", i)}, "%v", err))
				continue
			}
			id, err := s.o.IDs.New()
			if err != nil {
				return fmt.Errorf("telemetry: %w", err)
			}
			if err := q.InsertTelemetryEvent(ctx, dbgen.InsertTelemetryEventParams{
				ID: storage.MustUUID(id), OrganizationID: storage.MustUUID(src.OrganizationID), AppID: storage.MustUUID(src.AppID),
				EnvironmentID: storage.MustUUID(src.EnvironmentID), DeviceID: storage.MustUUID(src.DeviceID),
				Name: e.Name, Time: storage.Timestamp(e.Time), ReleaseSequence: e.ReleaseSequence,
				PluginKey: e.PluginKey, Route: e.Route, Fields: fields,
			}); err != nil {
				return fmt.Errorf("telemetry: %w", err)
			}
			accepted++
		}
		return nil
	})
	if err != nil {
		return 0, nil, err //nolint:wrapcheck // wrapped inside
	}
	return accepted, diags, nil
}

// check validates one event and returns its fields to store.
func (s *Service) check(src Source, e Event) ([]byte, error) {
	switch {
	case !names[e.Name]:
		return nil, fmt.Errorf("%q is not a catalogued event", e.Name)
	case e.DeviceID != "" && e.DeviceID != src.DeviceID:
		return nil, errors.New("an event names another device")
	case e.Time.IsZero() || e.Time.After(s.now().Add(time.Hour)) || e.Time.Before(s.now().Add(-Retention)):
		return nil, errors.New("the event time is missing or out of range")
	case e.ReleaseSequence < 0 || len(e.PluginKey) > 64 || len(e.Route) > maxText:
		return nil, errors.New("a field is out of range")
	}
	return checkFields(e.Fields)
}

// checkFields accepts a flat object of at most maxFields scalar values
// with well-formed, non-sensitive names.
func checkFields(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	if len(raw) > maxFieldsBytes {
		return nil, fmt.Errorf("the fields exceed %d bytes", maxFieldsBytes)
	}
	var fields map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, errors.New("the fields are not a JSON object")
	}
	if len(fields) > maxFields {
		return nil, fmt.Errorf("more than %d fields", maxFields)
	}
	for k, v := range fields {
		if !fieldName.MatchString(k) {
			return nil, fmt.Errorf("field name %q is not allowed", k)
		}
		if Sensitive(k) {
			return nil, fmt.Errorf("field %q looks sensitive and is refused", k)
		}
		switch x := v.(type) {
		case string:
			if len(x) > maxText {
				return nil, fmt.Errorf("field %q is longer than %d bytes", k, maxText)
			}
		case json.Number, bool, nil:
		default:
			return nil, fmt.Errorf("field %q is not a scalar", k)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	return out, nil
}

// Sensitive reports whether a field name marks sensitive data.
func Sensitive(name string) bool {
	lower := strings.ToLower(name)
	for _, w := range sensitiveWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// List lists an environment's events, newest first, optionally by name
// and since a time.
func (s *Service) List(ctx context.Context, p auth.Principal, appID, envID, name string, since time.Time, before storage.Cursor, size int32) ([]Event, error) {
	if err := p.AuthorizeApp(auth.AppRead, appID); err != nil {
		return nil, fmt.Errorf("telemetry: %w", err)
	}
	app, err1 := storage.UUID(appID)
	env, err2 := storage.UUID(envID)
	if err1 != nil || err2 != nil {
		return nil, plxerr.New(plxerr.InvalidFormat, "the app or environment identifier is not valid")
	}
	t := before.Time
	if t.IsZero() {
		t = s.now().Add(2 * time.Hour)
	}
	if since.IsZero() {
		since = s.now().Add(-Retention)
	}
	id := before.AfterID()
	if before.ID == "" {
		for i := range id.Bytes {
			id.Bytes[i] = 0xff
		}
	}
	var out []Event
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: p.OrganizationID, UserID: p.UserID}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := dbgen.New(tx).ListTelemetryEvents(ctx, dbgen.ListTelemetryEventsParams{
			AppID: app, EnvironmentID: env, Name: name, Since: storage.Timestamp(since),
			BeforeTime: storage.Timestamp(t), BeforeID: id, PageSize: size,
		})
		if err != nil {
			return fmt.Errorf("telemetry: %w", err)
		}
		for _, r := range rows {
			out = append(out, Event{
				ID: storage.ID(r.ID), Name: r.Name, DeviceID: storage.ID(r.DeviceID), PluginKey: r.PluginKey, Route: r.Route,
				Time: storage.Time(r.Time), ReleaseSequence: r.ReleaseSequence, Fields: r.Fields,
			})
		}
		return nil
	})
	return out, err //nolint:wrapcheck // wrapped inside
}

// Purge deletes an organisation's events past their retention.
func (s *Service) Purge(ctx context.Context, org string) (int64, error) {
	var n int64
	err := s.o.DB.InTx(ctx, storage.Tenant{OrganizationID: org}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = dbgen.New(tx).PurgeTelemetry(ctx, storage.Timestamp(s.now().Add(-Retention)))
		return err //nolint:wrapcheck // one statement
	})
	if err != nil {
		return 0, fmt.Errorf("telemetry: %w", err)
	}
	return n, nil
}
