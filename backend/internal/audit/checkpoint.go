// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package audit

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// checkpointType domain-separates the signed bytes of a checkpoint from
// every other message the audit key could be asked to sign.
const checkpointType = "plux.audit.checkpoint.v1"

// pageSize is how many entries or checkpoints one read fetches.
const pageSize = 1000

// Checkpoint is a signed point in one organisation's chain (SEC-141). It
// commits to the hash of the entry at Sequence, and so to every entry
// before it: a chain rewritten from any earlier entry onwards no longer
// has that hash, and the signature cannot be remade without the key.
type Checkpoint struct {
	ID             string
	OrganizationID string
	// Sequence is the sequence number of the last entry covered.
	Sequence int64
	// EntryHash is that entry's hash, in hexadecimal.
	EntryHash string
	CreatedAt time.Time
	// KeyID identifies the audit key that signed it.
	KeyID string
	// Algorithm is the signature algorithm; signing.Algorithm.
	Algorithm string
	Signature []byte
}

// Payload is the canonical encoding (RFC 8785) of everything the
// signature covers: every field but the signature and the row's own
// identifier. The sequence is a decimal string so that no reader's
// number type can round it.
func (c Checkpoint) Payload() ([]byte, error) {
	b, err := jcs.Marshal(map[string]any{
		"type":            checkpointType,
		"organization_id": c.OrganizationID,
		"sequence":        strconv.FormatInt(c.Sequence, 10),
		"entry_hash":      c.EntryHash,
		"created_at":      c.CreatedAt.UTC().Format(time.RFC3339Nano),
		"key_id":          c.KeyID,
		"algorithm":       c.Algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("audit: encode a checkpoint: %w", err)
	}
	return b, nil
}

// PublicKeys are the audit keys a checkpoint may have been signed with,
// by key identifier.
type PublicKeys map[string]ed25519.PublicKey

// Verify checks the checkpoint's signature against the trusted keys. A
// key or algorithm it does not know is refused rather than guessed at
// (SEC-122).
func (c Checkpoint) Verify(keys PublicKeys) error {
	if c.Algorithm != signing.Algorithm {
		return fmt.Errorf("checkpoint %d uses the unknown algorithm %q", c.Sequence, c.Algorithm)
	}
	key, ok := keys[c.KeyID]
	if !ok {
		return fmt.Errorf("checkpoint %d was signed by the unknown key %q", c.Sequence, c.KeyID)
	}
	payload, err := c.Payload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, payload, c.Signature) {
		return fmt.Errorf("checkpoint %d has an invalid signature", c.Sequence)
	}
	return nil
}

// Checkpointer signs checkpoints. Only the worker role builds one: it
// holds a Signer, which the api role never does (L-3, ADR-0006).
type Checkpointer struct {
	db     *storage.DB
	log    *Log
	signer signing.Signer
	key    string
	ids    IDs
	now    func() time.Time
}

// CheckpointerOptions configures NewCheckpointer.
type CheckpointerOptions struct {
	DB     *storage.DB
	Log    *Log
	Signer signing.Signer
	// Key is the reference of the audit checkpoint key.
	Key string
	IDs IDs
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// NewCheckpointer returns a checkpointer.
func NewCheckpointer(o CheckpointerOptions) (*Checkpointer, error) {
	if o.DB == nil || o.Log == nil || o.Signer == nil || o.IDs == nil || o.Key == "" {
		return nil, errors.New("audit: a checkpointer needs a database, a log, a signer, a key and identifiers")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Checkpointer{db: o.DB, log: o.Log, signer: o.Signer, key: o.Key, ids: o.IDs, now: now}, nil
}

// Checkpoint signs a checkpoint over an organisation's chain when the
// chain has entries the last checkpoint does not cover, and returns it;
// it returns nil when there is nothing new, so an idle organisation
// costs no signature (SEC-141).
//
// The entries since the last checkpoint are verified first, starting
// from that checkpoint's signed hash: a checkpoint is never signed over
// a chain that does not hold together.
func (c *Checkpointer) Checkpoint(ctx context.Context, organizationID string) (*Checkpoint, error) {
	var head Entry
	var found bool
	if err := c.db.InTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		head, found, err = c.uncovered(ctx, tx, organizationID)
		return err
	}); err != nil {
		return nil, fmt.Errorf("audit: checkpoint %s: %w", organizationID, err)
	}
	if !found {
		return nil, nil //nolint:nilnil // nothing new is not an error
	}
	cp, err := c.sign(ctx, head)
	if err != nil {
		return nil, err
	}
	var inserted int64
	if err := c.db.InTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		inserted, err = c.store(ctx, tx, cp)
		return err
	}); err != nil {
		return nil, fmt.Errorf("audit: store the checkpoint of %s: %w", organizationID, err)
	}
	if inserted == 0 {
		// Another worker signed the same point first.
		return nil, nil //nolint:nilnil // nothing new is not an error
	}
	return &cp, nil
}

// uncovered returns the chain's last entry when the latest checkpoint
// does not cover it, after verifying the entries in between.
func (c *Checkpointer) uncovered(ctx context.Context, tx pgx.Tx, organizationID string) (Entry, bool, error) {
	org, err := storage.UUID(organizationID)
	if err != nil {
		return Entry{}, false, fmt.Errorf("audit: %w", err)
	}
	var after int64
	var previous string
	latest, err := dbgen.New(tx).LatestAuditCheckpoint(ctx, org)
	switch {
	case err == nil:
		after, previous = latest.Sequence, latest.EntryHash
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return Entry{}, false, fmt.Errorf("audit: read the latest checkpoint: %w", err)
	}
	var head Entry
	for {
		page, err := c.log.List(ctx, tx, organizationID, after, pageSize)
		if err != nil {
			return Entry{}, false, err
		}
		if len(page) == 0 {
			return head, head.Sequence > 0, nil
		}
		if page[0].Sequence != after+1 {
			return Entry{}, false, fmt.Errorf("audit: sequence jumps from %d to %d: an entry is missing", after, page[0].Sequence)
		}
		if err := Verify(page, previous); err != nil {
			return Entry{}, false, err
		}
		head = page[len(page)-1]
		after, previous = head.Sequence, head.EntryHash
	}
}

// sign builds and signs the checkpoint of an entry.
func (c *Checkpointer) sign(ctx context.Context, head Entry) (Checkpoint, error) {
	_, keyID, err := c.signer.PublicKey(ctx, c.key)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("audit: audit checkpoint key: %w", err)
	}
	id, err := c.ids.New()
	if err != nil {
		return Checkpoint{}, fmt.Errorf("audit: %w", err)
	}
	cp := Checkpoint{
		ID: id, OrganizationID: head.OrganizationID, Sequence: head.Sequence, EntryHash: head.EntryHash,
		CreatedAt: c.now().UTC().Truncate(time.Microsecond), KeyID: keyID, Algorithm: signing.Algorithm,
	}
	payload, err := cp.Payload()
	if err != nil {
		return Checkpoint{}, err
	}
	sig, signedBy, err := c.signer.Sign(ctx, c.key, payload)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("audit: sign a checkpoint: %w", err)
	}
	if signedBy != keyID {
		return Checkpoint{}, errors.New("audit: the audit checkpoint key changed while signing; the job will retry")
	}
	cp.Signature = sig
	return cp, nil
}

// store inserts a checkpoint and reports how many rows it wrote.
func (*Checkpointer) store(ctx context.Context, tx pgx.Tx, cp Checkpoint) (int64, error) {
	id, err := storage.UUID(cp.ID)
	if err != nil {
		return 0, fmt.Errorf("audit: %w", err)
	}
	org, err := storage.UUID(cp.OrganizationID)
	if err != nil {
		return 0, fmt.Errorf("audit: %w", err)
	}
	n, err := dbgen.New(tx).InsertAuditCheckpoint(ctx, dbgen.InsertAuditCheckpointParams{
		ID: id, OrganizationID: org, Sequence: cp.Sequence, EntryHash: cp.EntryHash,
		KeyID: cp.KeyID, Algorithm: cp.Algorithm, Signature: cp.Signature, CreatedAt: storage.Timestamp(cp.CreatedAt),
	})
	if err != nil {
		return 0, fmt.Errorf("audit: insert a checkpoint: %w", err)
	}
	return n, nil
}

// ListCheckpoints returns an organisation's checkpoints after a sequence
// number, in order.
func (*Log) ListCheckpoints(ctx context.Context, tx pgx.Tx, organizationID string, afterSequence int64, limit int32) ([]Checkpoint, error) {
	org, err := storage.UUID(organizationID)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	rows, err := dbgen.New(tx).ListAuditCheckpoints(ctx, dbgen.ListAuditCheckpointsParams{
		OrganizationID: org, AfterSequence: afterSequence, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("audit: list checkpoints: %w", err)
	}
	out := make([]Checkpoint, len(rows))
	for i, row := range rows {
		out[i] = Checkpoint{
			ID: storage.ID(row.ID), OrganizationID: storage.ID(row.OrganizationID),
			Sequence: row.Sequence, EntryHash: row.EntryHash, CreatedAt: storage.Time(row.CreatedAt),
			KeyID: row.KeyID, Algorithm: row.Algorithm, Signature: row.Signature,
		}
	}
	return out, nil
}

// Break is the first place a chain or its checkpoints stop holding
// together.
type Break struct {
	// Kind is "entry" or "checkpoint".
	Kind string `json:"kind"`
	// Sequence is the entry or checkpoint concerned.
	Sequence int64 `json:"sequence"`
	// Reason says what is wrong.
	Reason string `json:"reason"`
}

// Error describes the break.
func (b *Break) Error() string {
	return fmt.Sprintf("audit: %s %d: %s", b.Kind, b.Sequence, b.Reason)
}

// Summary is what a successful verification covered.
type Summary struct {
	Entries     int64
	Checkpoints int
}

// VerifyCheckpointed verifies an organisation's whole chain and every
// checkpoint over it (SEC-140, SEC-141): each entry follows from the one
// before; each checkpoint carries a valid signature by a trusted key and
// names the hash of the entry at its sequence; and none points past the
// end of the chain, which is what removing the latest entries would
// leave behind. The error is a *Break naming the first thing wrong, in
// sequence order, or a plain error when the database could not be read.
func (l *Log) VerifyCheckpointed(ctx context.Context, tx pgx.Tx, organizationID string, keys PublicKeys) (Summary, error) {
	cps, err := l.allCheckpoints(ctx, tx, organizationID)
	if err != nil {
		return Summary{}, err
	}
	var (
		sum      Summary
		previous string
		last     int64
	)
	for {
		page, err := l.List(ctx, tx, organizationID, last, pageSize)
		if err != nil {
			return sum, err
		}
		if len(page) == 0 {
			break
		}
		for _, e := range page {
			if err := checkEntry(e, last, previous); err != nil {
				return sum, err
			}
			for len(cps) > 0 && cps[0].Sequence == e.Sequence {
				if err := checkCheckpoint(cps[0], e, keys); err != nil {
					return sum, err
				}
				cps = cps[1:]
				sum.Checkpoints++
			}
			sum.Entries++
			last, previous = e.Sequence, e.EntryHash
		}
	}
	if len(cps) > 0 {
		return sum, &Break{
			Kind: "checkpoint", Sequence: cps[0].Sequence,
			Reason: fmt.Sprintf("it points past the end of the chain, which stops at entry %d: later entries were removed", last),
		}
	}
	return sum, nil
}

// allCheckpoints reads every checkpoint of an organisation, in order.
func (l *Log) allCheckpoints(ctx context.Context, tx pgx.Tx, organizationID string) ([]Checkpoint, error) {
	var all []Checkpoint
	var after int64
	for {
		page, err := l.ListCheckpoints(ctx, tx, organizationID, after, pageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		after = page[len(page)-1].Sequence
	}
}

// checkEntry checks that an entry follows the one before it.
func checkEntry(e Entry, last int64, previous string) error {
	if e.Sequence != last+1 {
		return &Break{
			Kind: "entry", Sequence: e.Sequence,
			Reason: fmt.Sprintf("entries %d to %d are missing", last+1, e.Sequence-1),
		}
	}
	if err := e.follows(previous); err != nil {
		return &Break{Kind: "entry", Sequence: e.Sequence, Reason: strings.TrimPrefix(err.Error(), "audit: ")}
	}
	return nil
}

// checkCheckpoint checks a checkpoint's signature and that it names the
// entry it claims to cover.
func checkCheckpoint(cp Checkpoint, e Entry, keys PublicKeys) error {
	if err := cp.Verify(keys); err != nil {
		return &Break{Kind: "checkpoint", Sequence: cp.Sequence, Reason: err.Error()}
	}
	if cp.EntryHash != e.EntryHash {
		return &Break{
			Kind: "checkpoint", Sequence: cp.Sequence,
			Reason: "its signed hash is not the hash of the entry in the chain: the chain was rewritten",
		}
	}
	return nil
}

// CheckpointReader lists an organisation's checkpoints for the api role,
// which reads them and never signs (SEC-141).
type CheckpointReader struct {
	db  *storage.DB
	log *Log
}

// NewCheckpointReader returns a reader.
func NewCheckpointReader(db *storage.DB, log *Log) *CheckpointReader {
	return &CheckpointReader{db: db, log: log}
}

// List returns up to limit of an organisation's checkpoints after a
// sequence number, in order.
func (r *CheckpointReader) List(ctx context.Context, organizationID string, afterSequence int64, limit int32) ([]Checkpoint, error) {
	var out []Checkpoint
	err := r.db.InTx(ctx, storage.Tenant{OrganizationID: organizationID}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = r.log.ListCheckpoints(ctx, tx, organizationID, afterSequence, limit)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("audit: list checkpoints: %w", err)
	}
	return out, nil
}
