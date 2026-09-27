// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/auth"
	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Verifies: SEC-100.
// Passwords are Argon2id PHC strings with a per-password salt; a wrong,
// damaged or unbounded one never verifies.
func TestPasswordHashing(t *testing.T) {
	t.Parallel()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q; want an Argon2id PHC string", hash)
	}
	again, _ := auth.HashPassword(password)
	if again == hash {
		t.Error("two hashes of one password are equal: the salt is missing")
	}
	if err := auth.VerifyPassword(hash, password); err != nil {
		t.Errorf("VerifyPassword: %v", err)
	}
	for _, bad := range []string{"wrong password!", "", strings.Repeat("x", 2000)} {
		if auth.VerifyPassword(hash, bad) == nil {
			t.Errorf("VerifyPassword accepted %q", bad[:min(len(bad), 20)])
		}
	}
	for _, damaged := range []string{
		"", "$bcrypt$x", "$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=19456,t=2,p=1$!!$" + strings.Repeat("A", 43),
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$!!",
		"$argon2id$v=19$x$c2FsdA$aGFzaA",
	} {
		if auth.VerifyPassword(damaged, password) == nil {
			t.Errorf("VerifyPassword accepted the damaged hash %q", damaged)
		}
	}
	for _, weak := range []string{"short", strings.Repeat("é", 11), strings.Repeat("x", 1025)} {
		if _, err := auth.HashPassword(weak); err == nil {
			t.Errorf("HashPassword accepted a password of %d bytes", len(weak))
		}
	}
}

// Verifies: SEC-100.
// TOTP follows RFC 6238: the reference vector, one step of skew, and the
// step a code belongs to, which is what makes a code single-use.
func TestTOTP(t *testing.T) {
	t.Parallel()
	// RFC 6238 Appendix B, SHA-1: the secret is "12345678901234567890".
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" //nolint:gosec // the RFC's published test secret gitleaks:allow
	at := time.Unix(59, 0)
	got, err := auth.TOTPCode(secret, at)
	if err != nil || got != "287082" {
		t.Fatalf("TOTPCode = %q, %v; want the RFC's 287082", got, err)
	}
	step, err := auth.MatchTOTP(secret, got, at.Add(30*time.Second))
	if err != nil || step != 1 {
		t.Errorf("a code one step old: step %d, %v", step, err)
	}
	if _, err := auth.MatchTOTP(secret, got, at.Add(90*time.Second)); err == nil {
		t.Error("a code three steps old was accepted")
	}
	for _, bad := range []string{"", "12345", "abcdef", "2870821"} {
		if _, err := auth.MatchTOTP(secret, bad, at); err == nil {
			t.Errorf("MatchTOTP accepted %q", bad)
		}
	}
	if _, err := auth.TOTPCode("not base32!", at); err == nil {
		t.Error("a malformed secret was accepted")
	}
	fresh, err := auth.NewTOTPSecret()
	if err != nil || len(fresh) != 32 {
		t.Errorf("NewTOTPSecret = %q, %v", fresh, err)
	}
	url := auth.TOTPURL("Plux", "ada@example.com", fresh)
	if !strings.HasPrefix(url, "otpauth://totp/Plux:ada@example.com?") || !strings.Contains(url, "secret="+fresh) {
		t.Errorf("TOTPURL = %q", url)
	}
}

// Verifies: SEC-101, CLI-002.
// Secrets carry their kind, are stored only as hashes, and user codes
// avoid ambiguous characters and are normalised when typed.
func TestSecretsAndUserCodes(t *testing.T) {
	t.Parallel()
	s, err := auth.NewSecret(auth.PrefixToken, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Value, "plux_pat_") || !strings.HasPrefix(s.Value, s.Prefix) || len(s.Hash) != 32 {
		t.Errorf("secret = %+v", s)
	}
	if string(auth.HashSecret(s.Value)) != string(s.Hash) {
		t.Error("the stored hash is not the secret's hash")
	}
	if _, err := auth.NewSecret(auth.PrefixToken, 8); err == nil {
		t.Error("an 8-byte secret was accepted")
	}
	code, err := auth.NewUserCode()
	if err != nil || len(code) != 9 || code[4] != '-' || strings.ContainsAny(code, "AEIOUY01") {
		t.Errorf("NewUserCode = %q, %v", code, err)
	}
	if got := auth.NormaliseUserCode(" " + strings.ToLower(strings.ReplaceAll(code, "-", "")) + " "); got != code {
		t.Errorf("NormaliseUserCode = %q; want %q", got, code)
	}
	if got := auth.NormaliseUserCode("abc"); got != "ABC" {
		t.Errorf("NormaliseUserCode(abc) = %q", got)
	}
}

// Verifies: SEC-102.
// Roles hold exactly what the table lists; the capabilities SEC-100
// names need a second factor; a principal with no organisation is
// refused before anything else.
func TestRolesAndAuthorisation(t *testing.T) {
	t.Parallel()
	if got := auth.Roles(); len(got) != 4 || got[0] != auth.RoleAdmin {
		t.Errorf("Roles = %v", got)
	}
	for _, p := range []auth.Permission{auth.ReleasePublish, auth.ReleasePromote, auth.KeysManage, auth.MembersManage} {
		if !auth.NeedsSecondFactor(p) {
			t.Errorf("%s does not need a second factor (SEC-100)", p)
		}
	}
	if auth.NeedsSecondFactor(auth.AppRead) {
		t.Error("reading needs a second factor")
	}
	if len(auth.RolePermissions(auth.RoleOwner)) != len(auth.Permissions()) {
		t.Error("the owner does not hold the whole catalogue")
	}
	if _, err := auth.ParseRole("root"); err == nil {
		t.Error("an unknown role was accepted")
	}
	if _, err := auth.ParseScopes([]string{"app.read", "everything"}); err == nil {
		t.Error("an unknown scope was accepted")
	}
	scopes, err := auth.ParseScopes([]string{" app.read", "app.read", "plugin.read"})
	if err != nil || len(scopes) != 2 {
		t.Errorf("ParseScopes = %v, %v", scopes, err)
	}
	sys := auth.System("org")
	if sys.Authorize(auth.KeysManage) != nil || sys.Actor().Kind != "system" {
		t.Errorf("the system principal = %+v", sys)
	}
	var nobody auth.Principal
	if err := nobody.Authorize(auth.AppRead); code(err) != plxerr.AuthenticationRequired {
		t.Errorf("a principal with no organisation: %v", err)
	}
}
