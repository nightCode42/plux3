// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package device

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/nightCode42/plux3/backend/internal/attest/playintegrity"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// packageChecker accepts a Play Integrity token for the packages it lists
// only, and records what it was asked.
type packageChecker struct {
	accepts []string
	asked   []playintegrity.Expect
}

func (c *packageChecker) Verify(_ playintegrity.Keys, _ string, e playintegrity.Expect) (playintegrity.Verdict, error) {
	c.asked = append(c.asked, e)
	for _, p := range c.accepts {
		if p == e.PackageName {
			return playintegrity.Verdict{AppRecognised: true}, nil
		}
	}
	return playintegrity.Verdict{}, plxerr.New(plxerr.AttestationFailed, "play integrity: another package")
}

func refreshService(checker IntegrityChecker, trust TrustConfig) *Service {
	return &Service{
		attestors: Attestors{PlayIntegrity: checker},
		appTrust:  func(context.Context, string) (TrustConfig, error) { return trust, nil },
	}
}

// Verifies: SEC-025.
// An Android refresh carries a Play Integrity token bound to its DPoP
// proof exactly where androidRefreshRequiresIntegrity is set, and is
// refused without one.
func TestAndroidRefreshIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	trust := TrustConfig{AndroidPackages: []string{"com.example.one", "com.example.two"}, PlayIntegrity: &playintegrity.Keys{}, AndroidCertDigests: [][]byte{{1}}}
	phone := dbgen.Device{Platform: "android"}
	proof := "the-proof"
	sum := sha256.Sum256([]byte(proof))
	hash := base64.RawURLEncoding.EncodeToString(sum[:])

	// Where it is not required, the token is not looked at.
	checker := &packageChecker{accepts: []string{"com.example.two"}}
	s := refreshService(checker, trust)
	if counter, err := s.proveRefresh(ctx, phone, Settings{}, RefreshRequest{Proof: proof}); err != nil || counter != nil || len(checker.asked) != 0 {
		t.Errorf("a refresh that needs no token: %v %v %d", counter, err, len(checker.asked))
	}

	required := Settings{AndroidRefreshRequiresIntegrity: true}
	if _, err := s.proveRefresh(ctx, phone, required, RefreshRequest{Proof: proof}); err == nil || errCode(err) != plxerr.AttestationFailed {
		t.Errorf("no token where one is required: %v", err)
	}
	if _, err := s.proveRefresh(ctx, phone, required, RefreshRequest{Proof: proof, PlayIntegrityToken: "token"}); err != nil {
		t.Fatalf("a token for the second package: %v", err)
	}
	if len(checker.asked) != 2 {
		t.Fatalf("the packages asked = %d, want 2", len(checker.asked))
	}
	for _, e := range checker.asked {
		if e.RequestHash != hash || e.MaxAge != integrityMaxAge || len(e.CertDigests) != 1 {
			t.Errorf("Play Integrity was asked %+v", e)
		}
	}
	other := &packageChecker{}
	if _, err := refreshService(other, trust).proveRefresh(ctx, phone, required, RefreshRequest{Proof: proof, PlayIntegrityToken: "token"}); errCode(err) != plxerr.AttestationFailed {
		t.Errorf("a token for no package: %v", err)
	}

	// An app without Play Integrity configured cannot prove a refresh.
	for name, svc := range map[string]*Service{
		"no keys":     refreshService(checker, TrustConfig{AndroidPackages: []string{"com.example.one"}}),
		"no packages": refreshService(checker, TrustConfig{PlayIntegrity: &playintegrity.Keys{}}),
		"no verifier": refreshService(nil, trust),
	} {
		if _, err := svc.proveRefresh(ctx, phone, required, RefreshRequest{Proof: proof, PlayIntegrityToken: "token"}); errCode(err) != plxerr.AttestationUnavailable {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A device that is not Android never needs one.
	if _, err := s.proveRefresh(ctx, dbgen.Device{Platform: "linux"}, required, RefreshRequest{Proof: proof}); err != nil {
		t.Errorf("a Linux refresh: %v", err)
	}
}

func errCode(err error) plxerr.Code {
	c, _ := plxerr.CodeOf(err)
	return c
}
