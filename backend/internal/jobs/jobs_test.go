// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package jobs_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/nightCode42/plux3/backend/internal/jobs"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/storagetest"
)

// echoArgs is a job used only by these tests.
type echoArgs struct {
	Text string `json:"text" river:"unique"`
}

// Kind names the job type in the database.
func (echoArgs) Kind() string { return "test_echo" }

// InsertOpts puts the job on the maintenance queue.
func (echoArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance}
}

// echoWorker records what it ran.
type echoWorker struct {
	river.WorkerDefaults[echoArgs]

	mu   sync.Mutex
	seen []string
	done chan struct{}
}

// Work records the job's text.
func (w *echoWorker) Work(_ context.Context, job *river.Job[echoArgs]) error {
	w.mu.Lock()
	w.seen = append(w.seen, job.Args.Text)
	w.mu.Unlock()
	select {
	case w.done <- struct{}{}:
	default:
	}
	return nil
}

// texts returns what the worker has run.
func (w *echoWorker) texts() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.seen)
}

// client returns a migrated database and a running worker client.
func client(t *testing.T) (*storage.DB, *jobs.Client, *echoWorker) {
	t.Helper()
	db := storagetest.Open(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, db.Pool()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	w := &echoWorker{done: make(chan struct{}, 4)}
	workers := jobs.NewWorkers()
	jobs.AddWorker(workers, w)
	c, err := jobs.New(jobs.Options{Pool: db.Pool(), Workers: workers, Run: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.Stop(stop); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return db, c, w
}

// Verifies: SRV-024.
// A job is enqueued in the transaction that causes it, so rolling that
// transaction back leaves no work behind.
func TestJobsCommitWithTheirTransaction(t *testing.T) {
	t.Parallel()
	db, c, w := client(t)
	ctx := context.Background()

	rollback := storage.Tenant{}
	err := db.InTx(ctx, rollback, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := c.InsertTx(ctx, tx, echoArgs{Text: "discarded"}, nil); err != nil {
			return err
		}
		return context.Canceled // roll back
	})
	if err == nil {
		t.Fatal("the transaction was expected to fail")
	}

	if err := db.InTx(ctx, rollback, func(ctx context.Context, tx pgx.Tx) error {
		_, err := c.InsertTx(ctx, tx, echoArgs{Text: "kept"}, nil)
		return err
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case <-w.done:
	case <-time.After(20 * time.Second):
		t.Fatal("the job never ran")
	}
	got := w.texts()
	if len(got) != 1 || got[0] != "kept" {
		t.Errorf("the worker ran %v; want only the committed job", got)
	}
}

// Verifies: SRV-024.
func TestQueueDepthsAreReadable(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, db.Pool()); err != nil {
		t.Fatal(err)
	}
	// A client that does not run leaves the jobs waiting, which is what
	// the depth metric reports.
	c, err := jobs.New(jobs.Options{Pool: db.Pool()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start on an enqueue-only client must do nothing: %v", err)
	}
	if err := db.InTx(ctx, storage.Tenant{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := c.InsertTx(ctx, tx, echoArgs{Text: "waiting"}, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	depths, err := c.QueueDepths(ctx)
	if err != nil {
		t.Fatalf("QueueDepths: %v", err)
	}
	if depths[jobs.QueueMaintenance] != 1 {
		t.Errorf("depths = %v; want one job on %s", depths, jobs.QueueMaintenance)
	}
	if err := c.Stop(ctx); err != nil {
		t.Errorf("Stop on an enqueue-only client must do nothing: %v", err)
	}
}

func TestNewNeedsAPool(t *testing.T) {
	t.Parallel()
	if _, err := jobs.New(jobs.Options{}); err == nil {
		t.Error("a client without a pool was accepted")
	}
	if got := jobs.Queues(); len(got) != 4 {
		t.Errorf("Queues() = %v", got)
	}
}

// Verifies: SRV-024.
// A periodic job is enqueued when a worker starts, without anyone
// inserting it.
func TestPeriodicJobsRunOnStart(t *testing.T) {
	t.Parallel()
	db := storagetest.Open(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, db.Pool()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	w := &echoWorker{done: make(chan struct{}, 4)}
	workers := jobs.NewWorkers()
	jobs.AddWorker(workers, w)
	c, err := jobs.New(jobs.Options{
		Pool: db.Pool(), Workers: workers, Run: true,
		Periodic: []*river.PeriodicJob{jobs.Every(time.Hour, echoArgs{Text: "tick"})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.Stop(stop)
	})
	select {
	case <-w.done:
	case <-time.After(20 * time.Second):
		t.Fatal("the periodic job did not run on start")
	}
	if got := w.texts(); len(got) == 0 || got[0] != "tick" {
		t.Errorf("ran %v", got)
	}
}
