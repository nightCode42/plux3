// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// maxControlMessage bounds the message shown when a switch is on.
const maxControlMessage = 500

// Control is the set of switches of one channel (REL-030).
type Control struct {
	AppID, EnvironmentID, ChannelKey string
	KillSwitchPlugins                []string
	AppKillSwitch, MandatoryUpdate   bool
	Message                          string
	UpdatedBy                        audit.Actor
	UpdatedAt                        time.Time
}

// GetControl returns a channel's switches; a channel never changed has
// every switch off.
func (s *Service) GetControl(ctx context.Context, p auth.Principal, appID, envID, channelKey string) (Control, error) {
	if err := authorize(p, auth.AppRead, appID); err != nil {
		return Control{}, err
	}
	var out Control
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		_, env, err := s.environment(ctx, q, appID, envID)
		if err != nil {
			return err
		}
		ch, err := q.GetChannel(ctx, dbgen.GetChannelParams{EnvironmentID: env.ID, Key: channelOrDefault(channelKey)})
		if err != nil {
			return failure(err, "channel")
		}
		row, err := s.controlRow(ctx, q, ch)
		if err != nil {
			return err
		}
		out = controlOf(appID, envID, ch.Key, row)
		return nil
	})
	return out, err
}

// SetControl replaces a channel's switches and has a manifest carrying
// them signed; devices obey it at their next manifest check (REL-006).
func (s *Service) SetControl(ctx context.Context, p auth.Principal, c Control) (Control, error) {
	if err := authorize(p, auth.ReleasePromote, c.AppID); err != nil {
		return Control{}, err
	}
	if len(c.Message) > maxControlMessage {
		return Control{}, plxerr.New(plxerr.InvalidFormat, "the message is longer than %d characters", maxControlMessage)
	}
	kills := slices.Clone(c.KillSwitchPlugins)
	for _, k := range kills {
		if !schema.ValidKey(k) {
			return Control{}, plxerr.New(plxerr.InvalidFormat, "%q is not a plugin key", k)
		}
	}
	slices.Sort(kills)
	kills = slices.Compact(kills)
	if kills == nil {
		kills = []string{}
	}
	var out Control
	err := s.inOrg(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := dbgen.New(tx)
		_, env, err := s.environment(ctx, q, c.AppID, c.EnvironmentID)
		if err != nil {
			return err
		}
		ch, err := q.GetChannelForUpdate(ctx, dbgen.GetChannelForUpdateParams{EnvironmentID: env.ID, Key: channelOrDefault(c.ChannelKey)})
		if err != nil {
			return failure(err, "channel")
		}
		actor := p.Actor()
		row, err := q.UpsertChannelControl(ctx, dbgen.UpsertChannelControlParams{
			ChannelID: ch.ID, OrganizationID: ch.OrganizationID, KillSwitchPlugins: kills,
			AppKillSwitch: c.AppKillSwitch, MandatoryUpdate: c.MandatoryUpdate, Message: c.Message,
			UpdatedByKind: actor.Kind, UpdatedByID: actor.ID, UpdatedBy: actor.Display,
		})
		if err != nil {
			return failure(err, "control")
		}
		if err := s.enqueueManifest(ctx, tx, ch); err != nil {
			return err
		}
		if err := s.record(ctx, tx, p, audit.Entry{
			Action: audit.ControlChanged, TargetKind: "channel", TargetID: storage.ID(ch.ID),
			Detail: "kill switches [" + strings.Join(kills, ", ") + "]" + onOff(" app kill switch", c.AppKillSwitch) + onOff(" mandatory update", c.MandatoryUpdate),
		}); err != nil {
			return err
		}
		out = controlOf(c.AppID, c.EnvironmentID, ch.Key, row)
		return nil
	})
	return out, err
}

// controlRow reads a channel's switches, all off when never set.
func (*Service) controlRow(ctx context.Context, q *dbgen.Queries, ch dbgen.Channel) (dbgen.ChannelControl, error) {
	row, err := q.GetChannelControl(ctx, ch.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.ChannelControl{ChannelID: ch.ID, KillSwitchPlugins: []string{}}, nil
	}
	if err != nil {
		return dbgen.ChannelControl{}, failure(err, "control")
	}
	return row, nil
}

func controlOf(appID, envID, channel string, r dbgen.ChannelControl) Control {
	return Control{
		AppID: appID, EnvironmentID: envID, ChannelKey: channel, KillSwitchPlugins: r.KillSwitchPlugins,
		AppKillSwitch: r.AppKillSwitch, MandatoryUpdate: r.MandatoryUpdate, Message: r.Message,
		UpdatedBy: audit.Actor{Kind: r.UpdatedByKind, ID: r.UpdatedByID, Display: r.UpdatedBy},
		UpdatedAt: storage.Time(r.UpdatedAt),
	}
}

func channelOrDefault(key string) string {
	if key == "" {
		return "production"
	}
	return key
}

func onOff(label string, on bool) string {
	if on {
		return label + " on"
	}
	return ""
}
