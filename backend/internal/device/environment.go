// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"sync"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage"
)

// productionTTL is how long a replica remembers whether an environment is
// a production one. The answer changes only when an environment is
// created or deleted, and the key class it selects is checked against the
// token's signature, so a stale answer cannot admit a forged token.
const productionTTL = 60 * time.Second

// maxProductionEntries bounds the memory: past it, the cache starts over.
const maxProductionEntries = 4096

// productionCache maps an environment to whether it is production.
type productionCache struct {
	mu      sync.Mutex
	entries map[string]productionEntry
}

// productionEntry is one remembered answer; known is false for an
// environment that does not exist, which is remembered as well so that
// unknown identifiers cost the database one read a minute.
type productionEntry struct {
	production, known bool
	until             time.Time
}

func (c *productionCache) get(environmentID string, now time.Time) (productionEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[environmentID]
	if !ok || !now.Before(e.until) {
		return productionEntry{}, false
	}
	return e, true
}

func (c *productionCache) put(environmentID string, e productionEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) >= maxProductionEntries {
		c.entries = map[string]productionEntry{}
	}
	c.entries[environmentID] = e
}

// EnvironmentIsProduction says whether an environment is a production
// one, which selects the class of key that signs and verifies its tokens
// (SEC-056). An unknown environment is ResourceNotFound. The answer may be
// up to a minute old.
func (s *Service) EnvironmentIsProduction(ctx context.Context, environmentID string) (bool, error) {
	now := s.now()
	if e, ok := s.production.get(environmentID, now); ok {
		return e.production, notKnown(e.known)
	}
	id, err := storage.UUID(environmentID)
	if err != nil {
		return false, plxerr.New(plxerr.ResourceNotFound, "no such environment")
	}
	production, err := s.environmentIsProduction(ctx, id)
	if code, _ := plxerr.CodeOf(err); code == plxerr.ResourceNotFound {
		s.production.put(environmentID, productionEntry{until: now.Add(productionTTL)})
		return false, err
	}
	if err != nil {
		return false, err
	}
	s.production.put(environmentID, productionEntry{production: production, known: true, until: now.Add(productionTTL)})
	return production, nil
}

// notKnown is the error of an environment the cache remembers as absent.
func notKnown(known bool) error {
	if known {
		return nil
	}
	return plxerr.New(plxerr.ResourceNotFound, "no such environment")
}
