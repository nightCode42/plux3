// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/hex"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/audit"
	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// CheckpointSource lists an organisation's audit checkpoints.
type CheckpointSource interface {
	List(ctx context.Context, organizationID string, afterSequence int64, limit int32) ([]audit.Checkpoint, error)
}

// ListAuditCheckpoints lists the signed checkpoints over the calling
// organisation's audit chain, oldest first (SEC-141).
func (s SecurityAdmin) ListAuditCheckpoints(ctx context.Context, req *connect.Request[pluxv1.ListAuditCheckpointsRequest]) (*connect.Response[pluxv1.ListAuditCheckpointsResponse], error) {
	if s.checkpoints == nil {
		return s.UnimplementedSecurityAdminServiceHandler.ListAuditCheckpoints(ctx, req) //nolint:wrapcheck // the generated refusal
	}
	return read(ctx, func(ctx context.Context) (*pluxv1.ListAuditCheckpointsResponse, error) {
		p, err := s.h.principal(ctx, req.Header(), "")
		if err != nil {
			return nil, err
		}
		if err := p.Authorize(auth.AuditRead); err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		page := req.Msg.GetPage()
		after, size, err := s.h.Pages.Request(req.Spec().Procedure, p.OrganizationID, page)
		if err != nil {
			return nil, err
		}
		cps, err := s.checkpoints.List(ctx, p.OrganizationID, after.Sequence, size)
		if err != nil {
			return nil, err //nolint:wrapcheck // a domain error
		}
		out := &pluxv1.ListAuditCheckpointsResponse{Page: &pluxv1.PageResult{}}
		var last storage.Cursor
		for _, cp := range cps {
			item, err := checkpointProto(cp)
			if err != nil {
				return nil, err
			}
			last = storage.Cursor{Sequence: cp.Sequence, ID: cp.ID}
			out.Checkpoints = append(out.Checkpoints, item)
		}
		out.Page.NextPageToken = s.h.Pages.Next(req.Spec().Procedure, p.OrganizationID, page, len(cps), size, last)
		return out, Mask(page.GetReadMask(), out.Checkpoints)
	})
}

// checkpointProto converts a checkpoint for the wire.
func checkpointProto(cp audit.Checkpoint) (*pluxv1.AuditCheckpoint, error) {
	hash, err := hex.DecodeString(cp.EntryHash)
	if err != nil {
		return nil, plxerr.New(plxerr.InternalServerError, "a stored checkpoint has a malformed entry hash")
	}
	return &pluxv1.AuditCheckpoint{
		Sequence: cp.Sequence, EntryHash: hash, Signature: cp.Signature, KeyId: cp.KeyID, CreatedAt: ts(cp.CreatedAt),
	}, nil
}
