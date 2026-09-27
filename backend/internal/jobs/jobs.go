// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package jobs runs the server's background work as durable rows in
// PostgreSQL (SRV-024, ADR-0007).
//
// A job is enqueued in the same transaction as the change that causes
// it, so state and work never disagree: a publish is either recorded and
// queued, or neither. Retries, backoff, uniqueness keys and job state
// are rows an operator can read with psql.
//
// Jobs run only in the worker role. The api role builds a client that
// can enqueue but does not start, so a handler can queue work without
// ever executing it (ADR-0006).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// Queue names the queues this phase uses. Each is a separate lane, so a
// long asset job never starves a publish.
const (
	// QueuePublish runs the publish pipeline (SRV-050).
	QueuePublish = "publish"
	// QueueDelta generates deltas (REL-022).
	QueueDelta = "delta"
	// QueueAsset processes uploaded assets (SRV-060).
	QueueAsset = "asset"
	// QueueMaintenance runs retention and other periodic work (REL-007).
	QueueMaintenance = "maintenance"
)

// queues are the queues a worker serves, with how many jobs each runs at
// once.
var queues = map[string]river.QueueConfig{
	QueuePublish:     {MaxWorkers: 4},
	QueueDelta:       {MaxWorkers: 8},
	QueueAsset:       {MaxWorkers: 4},
	QueueMaintenance: {MaxWorkers: 1},
}

// Workers is the set of job types a worker process runs. It wraps
// River's own set so that the client can tell an empty set from a
// missing one: a worker process that registers no job type starts no
// queues rather than failing.
type Workers struct {
	river *river.Workers
	n     int
}

// NewWorkers returns an empty set.
func NewWorkers() *Workers { return &Workers{river: river.NewWorkers()} }

// AddWorker registers the worker of one job type.
func AddWorker[T river.JobArgs](w *Workers, worker river.Worker[T]) {
	river.AddWorker(w.river, worker)
	w.n++
}

// Len returns how many job types are registered.
func (w *Workers) Len() int {
	if w == nil {
		return 0
	}
	return w.n
}

// Client enqueues and, in the worker role, runs jobs.
type Client struct {
	river *river.Client[pgx.Tx]
	pool  *pgxpool.Pool
	// running is true when this client also works jobs.
	running bool
}

// Options configures New.
type Options struct {
	// Pool is the database.
	Pool *pgxpool.Pool
	// Workers are the job types to run. An api-role client registers
	// none and only enqueues.
	Workers *Workers
	// Run starts the queues; only the worker role sets it.
	Run bool
	// Log receives job events.
	Log *slog.Logger
}

// New builds the client. It does not start it; call Start.
func New(opts Options) (*Client, error) {
	if opts.Pool == nil {
		return nil, errors.New("jobs: a database pool is required")
	}
	// Queues are configured only when there is something to run: River
	// refuses a client that serves queues with no registered job type,
	// and a worker process with none is a valid, idle process.
	run := opts.Run && opts.Workers.Len() > 0
	cfg := &river.Config{Logger: opts.Log}
	if run {
		cfg.Queues = queues
		cfg.Workers = opts.Workers.river
		cfg.FetchCooldown = 100 * time.Millisecond
		cfg.JobTimeout = 30 * time.Minute
	}
	c, err := river.NewClient(riverpgxv5.New(opts.Pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("jobs: %w", err)
	}
	return &Client{river: c, pool: opts.Pool, running: run}, nil
}

// Migrate applies River's own schema. It runs with the server's
// migrations, under the same start-up path (SRV-021).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("jobs: %w", err)
	}
	if _, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("jobs: migrate: %w", err)
	}
	return nil
}

// Start begins working jobs. On a client that only enqueues it does
// nothing, so a caller need not know the role.
func (c *Client) Start(ctx context.Context) error {
	if !c.running {
		return nil
	}
	if err := c.river.Start(ctx); err != nil {
		return fmt.Errorf("jobs: start: %w", err)
	}
	return nil
}

// Stop drains the queues, letting running jobs finish until the context
// is done (SRV-007).
func (c *Client) Stop(ctx context.Context) error {
	if !c.running {
		return nil
	}
	if err := c.river.Stop(ctx); err != nil {
		return fmt.Errorf("jobs: stop: %w", err)
	}
	return nil
}

// InsertTx enqueues a job inside an existing transaction, so the job and
// the change that causes it commit together (SRV-024).
func (c *Client) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobRow, error) {
	res, err := c.river.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, fmt.Errorf("jobs: enqueue %s: %w", args.Kind(), err)
	}
	return res.Job, nil
}

// QueueDepths returns how many jobs wait in each queue, for the
// plux_jobs_queue_depth metric of Appendix G.1.
func (c *Client) QueueDepths(ctx context.Context) (map[string]int, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT queue, count(*) FROM river_job WHERE state IN ('available', 'retryable') GROUP BY queue`)
	if err != nil {
		return nil, fmt.Errorf("jobs: queue depths: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var queue string
		var n int
		if err := rows.Scan(&queue, &n); err != nil {
			return nil, fmt.Errorf("jobs: queue depths: %w", err)
		}
		out[queue] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs: queue depths: %w", err)
	}
	return out, nil
}

// Queues returns the queue names a worker serves, in a stable order.
func Queues() []string {
	return []string{QueueAsset, QueueDelta, QueueMaintenance, QueuePublish}
}
