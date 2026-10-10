// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/device/devicetest"
	"github.com/nightCode42/plux3/backend/internal/pluxv1"
	"github.com/nightCode42/plux3/backend/internal/pluxv1/pluxv1connect"
)

// Verifies: SEC-007.
// The refresh response carries the device's assurance level, the value of
// the token's al claim, and the manifest is refused with PLX-6002 to a
// device below the environment's minAssuranceForSync.
func TestManifestNeedsTheMinimumAssuranceForSync(t *testing.T) {
	t.Parallel()
	f := newNegativeFixture(t)
	manifests := pluxv1connect.NewManifestServiceClient(&http.Client{Transport: f.rec}, f.w.base)
	getManifest := func() error {
		r := connect.NewRequest(&pluxv1.GetManifestRequest{})
		r.Header().Set("Authorization", "DPoP "+f.dev.token)
		r.Header().Set("DPoP", devicetest.SignProof(t, f.dev.key, devicetest.Proof{
			Method: "POST", URL: f.w.base + pluxv1connect.ManifestServiceGetManifestProcedure,
			AccessToken: f.dev.token, Nonce: f.nonce(), IssuedAt: f.clock.Now(),
		}))
		_, err := manifests.GetManifest(t.Context(), r)
		return err
	}

	refresh := connect.NewRequest(&pluxv1.RefreshDeviceTokenRequest{DeviceId: f.dev.id})
	refresh.Header().Set("DPoP", devicetest.SignProof(t, f.dev.key, devicetest.Proof{
		Method: "POST", URL: f.w.base + pluxv1connect.TokenServiceRefreshDeviceTokenProcedure,
		Nonce: f.nonce(), IssuedAt: f.clock.Now(),
	}))
	res := must(f.w.token.RefreshDeviceToken(t.Context(), refresh))(t)
	if res.GetAssuranceLevel() != "AL0" {
		t.Errorf("a development device's assurance_level = %q, want AL0", res.GetAssuranceLevel())
	}

	// Standard asks for AL0: the call goes on (to find no release).
	if err := getManifest(); reasonOf(t, err) == "ASSURANCE_INSUFFICIENT" {
		t.Errorf("under standard: %v", err)
	}
	// Strict asks for AL1.
	f.strict.Store(f.envs["development"], true)
	err := getManifest()
	if codeOf(err) != connect.CodePermissionDenied || reasonOf(t, err) != "ASSURANCE_INSUFFICIENT" {
		t.Errorf("under strict: %v", err)
	}
}
