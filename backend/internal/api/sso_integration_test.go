// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/nightCode42/plux3/backend/internal/pluxv1"
)

// Verifies: SEC-100.
// The single sign-on and security-key procedures are reachable without
// a credential where they must be, need a session where they must, and
// report an installation without a provider or relying party as a
// failed precondition rather than an error.
func TestSingleSignOnAndSecurityKeysOverTheAPI(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.identity.StartOidcLogin(ctx, connect.NewRequest(&pluxv1.StartOidcLoginRequest{})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("StartOidcLogin without a provider: %v", err)
	}
	if _, err := w.identity.CompleteOidcLogin(ctx, connect.NewRequest(&pluxv1.CompleteOidcLoginRequest{State: "x", Code: "y"})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("CompleteOidcLogin without a provider: %v", err)
	}
	if _, err := w.identity.BeginWebAuthnLogin(ctx, connect.NewRequest(&pluxv1.BeginWebAuthnLoginRequest{ChallengeId: "plux_mfa_x"})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("BeginWebAuthnLogin without a relying party: %v", err)
	}
	if _, err := w.identity.CompleteWebAuthnLogin(ctx, connect.NewRequest(&pluxv1.CompleteWebAuthnLoginRequest{ChallengeId: "plux_mfa_x"})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("CompleteWebAuthnLogin without a relying party: %v", err)
	}
	if _, err := w.identity.BeginWebAuthnRegistration(ctx, connect.NewRequest(&pluxv1.BeginWebAuthnRegistrationRequest{Label: "key"})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous registration: %v", err)
	}
	if _, err := w.identity.FinishWebAuthnRegistration(ctx, connect.NewRequest(&pluxv1.FinishWebAuthnRegistrationRequest{})); codeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("an anonymous registration: %v", err)
	}
	_, invitation, err := w.auth.Bootstrap(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	must(w.identity.AcceptInvitation(ctx, connect.NewRequest(&pluxv1.AcceptInvitationRequest{
		Invitation: invitation, DisplayName: "Admin", Password: "correct horse battery",
	})))(t)
	admin := w.signIn(t, "admin@example.com")
	if _, err := w.identity.BeginWebAuthnRegistration(ctx, req(admin, &pluxv1.BeginWebAuthnRegistrationRequest{Label: "key"})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a registration without a relying party: %v", err)
	}
	if _, err := w.identity.FinishWebAuthnRegistration(ctx, req(admin, &pluxv1.FinishWebAuthnRegistrationRequest{FactorId: "x"})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("finishing without a relying party: %v", err)
	}
}
