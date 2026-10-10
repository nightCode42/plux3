// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/nightCode42/plux3/backend/internal/jobs"
)

// AuditCheckpoint is the periodic job that signs a checkpoint over each
// organisation's audit chain (SEC-141).
type AuditCheckpoint struct{}

// Kind names the job.
func (AuditCheckpoint) Kind() string { return "audit.checkpoint" }

// InsertOpts puts the job on the maintenance queue.
func (AuditCheckpoint) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance}
}

// checkpointWorker runs the job. It exists only in a process that holds
// a signer, which is the worker role's (ADR-0006).
type checkpointWorker struct {
	river.WorkerDefaults[AuditCheckpoint]
	svc *Services
	log *slog.Logger
}

// Work signs a checkpoint for every organisation with entries the last
// one does not cover. An organisation that fails does not stop the
// others; the job fails if any did, so River retries it.
func (w *checkpointWorker) Work(ctx context.Context, _ *river.Job[AuditCheckpoint]) error {
	var errs []error
	signed := 0
	err := w.svc.Tenancy.ForEachOrganization(ctx, func(org string) error {
		cp, err := w.svc.Checkpointer.Checkpoint(ctx, org)
		errs = append(errs, err)
		if cp != nil {
			signed++
		}
		return nil
	})
	errs = append(errs, err)
	if w.log != nil {
		w.log.InfoContext(ctx, "audit checkpoints", slog.Int("signed", signed))
	}
	return errors.Join(errs...)
}

// CheckpointJobs registers the checkpoint worker and returns its
// schedule; a process with no checkpointer registers and schedules
// nothing.
func CheckpointJobs(workers *jobs.Workers, svc *Services, interval time.Duration, log *slog.Logger) []*river.PeriodicJob {
	if svc.Checkpointer == nil {
		return nil
	}
	jobs.AddWorker(workers, &checkpointWorker{svc: svc, log: log})
	return []*river.PeriodicJob{jobs.Every(interval, AuditCheckpoint{})}
}
