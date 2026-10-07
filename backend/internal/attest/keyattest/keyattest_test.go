// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"bytes"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/limits"
)

func TestVerifyValid(t *testing.T) {
	// Verifies: SEC-002.
	tests := []struct {
		name      string
		attLevel  int64
		kmLevel   int64
		wantAtt   SecurityLevel
		wantKM    SecurityLevel
		withRoot  bool
		twoCertOK bool
	}{
		{name: "TEE", attLevel: 1, kmLevel: 1, wantAtt: SecurityTrustedEnvironment, wantKM: SecurityTrustedEnvironment},
		{name: "StrongBox", attLevel: 2, kmLevel: 2, wantAtt: SecurityStrongBox, wantKM: SecurityStrongBox},
		{name: "StrongBox key with TEE attestation", attLevel: 1, kmLevel: 2, wantAtt: SecurityTrustedEnvironment, wantKM: SecurityStrongBox},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPKI(t, nil)
			spec := validSpec()
			spec.attLevel, spec.kmLevel = tt.attLevel, tt.kmLevel
			leaf := p.leaf(elliptic.P256(), encodeKD(spec))
			for _, chain := range [][][]byte{p.chain(leaf), {leaf, p.intDER}} {
				res, err := p.verifier(nil).Verify(chain, testChallenge, testPolicy())
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				if res.AttestationLevel != tt.wantAtt || res.KeyMintLevel != tt.wantKM || !res.HardwareBacked() {
					t.Errorf("levels = %d/%d, want %d/%d", res.AttestationLevel, res.KeyMintLevel, tt.wantAtt, tt.wantKM)
				}
				if res.VerifiedBootState != BootVerified || !res.DeviceLocked {
					t.Errorf("boot = %d locked=%v", res.VerifiedBootState, res.DeviceLocked)
				}
				if res.PackageName != testPackage || res.OSPatchLevel != 20260905 {
					t.Errorf("package=%q patch=%d", res.PackageName, res.OSPatchLevel)
				}
				if len(res.SigningCertDigests) != 1 || !bytes.Equal(res.SigningCertDigests[0], testDigest()) {
					t.Errorf("digests = %x", res.SigningCertDigests)
				}
				if res.PublicKey == nil || res.PublicKey.Curve != elliptic.P256() {
					t.Errorf("public key = %v", res.PublicKey)
				}
			}
		})
	}
}

func TestVerifyApplicationIDInHardwareList(t *testing.T) {
	// Verifies: SEC-002.
	p := newPKI(t, nil)
	spec := validSpec()
	spec.appInHW = true
	res, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), encodeKD(spec))), testChallenge, testPolicy())
	if err != nil || res.PackageName != testPackage {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
}

func TestVerifyOptionalPolicy(t *testing.T) {
	// Verifies: SEC-002.
	p := newPKI(t, nil)
	spec := validSpec()
	spec.attLevel, spec.kmLevel = 0, 0
	spec.root = &rotSpec{locked: false, state: 2}
	spec.digests = [][]byte{otherDigest()}
	policy := Policy{PackageNames: []string{"other.app", testPackage}}
	res, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), encodeKD(spec))), testChallenge, policy)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.HardwareBacked() || res.VerifiedBootState != BootUnverified || res.DeviceLocked {
		t.Errorf("result = %+v", res)
	}
}

func TestVerifyPolicyFailures(t *testing.T) {
	// Verifies: SEC-002.
	tests := []struct {
		name   string
		mutate func(*kdSpec)
		policy func(*Policy)
		clock  []byte
		want   plxerr.Code
	}{
		{name: "software attestation", mutate: func(s *kdSpec) { s.attLevel = 0 }, want: plxerr.KeyNotHardwareBacked},
		{name: "software key", mutate: func(s *kdSpec) { s.kmLevel = 0 }, want: plxerr.KeyNotHardwareBacked},
		{name: "wrong challenge", mutate: func(s *kdSpec) { s.challenge = []byte("another challenge") }, want: plxerr.AttestationFailed},
		{name: "challenge prefix", mutate: func(s *kdSpec) { s.challenge = testChallenge[:8] }, want: plxerr.AttestationFailed},
		{name: "wrong package", mutate: func(s *kdSpec) { s.packages = []string{"com.evil.app"} }, want: plxerr.AttestationFailed},
		{name: "no allowed package", policy: func(p *Policy) { p.PackageNames = nil }, want: plxerr.AttestationFailed},
		{name: "wrong digest", mutate: func(s *kdSpec) { s.digests = [][]byte{otherDigest()} }, want: plxerr.AttestationFailed},
		{name: "additional unlisted digest", mutate: func(s *kdSpec) { s.digests = [][]byte{testDigest(), otherDigest()} }, want: plxerr.AttestationFailed},
		{name: "no digest", mutate: func(s *kdSpec) { s.digests = nil }, want: plxerr.AttestationFailed},
		{name: "no application identity", mutate: func(s *kdSpec) { s.noAppID = true }, want: plxerr.AttestationFailed},
		{name: "unverified boot", mutate: func(s *kdSpec) { s.root.state = 2 }, want: plxerr.AttestationFailed},
		{name: "self-signed boot", mutate: func(s *kdSpec) { s.root.state = 1 }, want: plxerr.AttestationFailed},
		{name: "failed boot", mutate: func(s *kdSpec) { s.root.state = 3 }, want: plxerr.AttestationFailed},
		{name: "unlocked bootloader", mutate: func(s *kdSpec) { s.root.locked = false }, want: plxerr.AttestationFailed},
		{name: "no root of trust", mutate: func(s *kdSpec) { s.root = nil }, want: plxerr.AttestationFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPKI(t, nil)
			spec := validSpec()
			root := *spec.root
			spec.root = &root
			if tt.mutate != nil {
				tt.mutate(&spec)
			}
			policy := testPolicy()
			if tt.policy != nil {
				tt.policy(&policy)
			}
			res, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), encodeKD(spec))), testChallenge, policy)
			wantCode(t, err, tt.want)
			if res.PublicKey != nil || res.PackageName != "" {
				t.Errorf("result on failure = %+v", res)
			}
		})
	}
}

func TestVerifyEmptyChallengeRejected(t *testing.T) {
	// Verifies: SEC-002.
	p := newPKI(t, nil)
	spec := validSpec()
	spec.challenge = nil
	_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), encodeKD(spec))), nil, testPolicy())
	wantCode(t, err, plxerr.AttestationFailed)
}

func TestVerifyChainFailures(t *testing.T) {
	// Verifies: SEC-002.
	revoked, err := ParseRevocations([]byte(`{"entries":{"2":{"status":"REVOKED","reason":"KEY_COMPROMISE"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	suspendedLeaf, err := ParseRevocations([]byte(`{"entries":{"03":{"status":"SUSPENDED"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	validKD := encodeKD(validSpec())
	tests := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"revoked intermediate", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(revoked).Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"suspended leaf", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(suspendedLeaf).Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"expired chain", func(t *testing.T) error {
			p := newPKI(t, nil)
			v := p.verifier(nil)
			v.Now = func() time.Time { return testNow.Add(48 * time.Hour) }
			_, err := v.Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"not yet valid chain", func(t *testing.T) error {
			p := newPKI(t, nil)
			v := p.verifier(nil)
			v.Now = func() time.Time { return testNow.Add(-48 * time.Hour) }
			_, err := v.Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"unknown root", func(t *testing.T) error {
			p, other := newPKI(t, nil), newPKI(t, nil)
			_, err := other.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"extension missing", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), nil)), testChallenge, testPolicy())
			return err
		}},
		{"extension on intermediate", func(t *testing.T) error {
			p := newPKI(t, []pkix.Extension{{Id: attestationOID, Value: validKD}})
			_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"extension only on intermediate", func(t *testing.T) error {
			p := newPKI(t, []pkix.Extension{{Id: attestationOID, Value: validKD}})
			_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), nil)), testChallenge, testPolicy())
			return err
		}},
		{"P-384 leaf", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P384(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"single certificate", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(nil).Verify([][]byte{p.leaf(elliptic.P256(), validKD)}, testChallenge, testPolicy())
			return err
		}},
		{"empty chain", func(t *testing.T) error {
			_, err := newPKI(t, nil).verifier(nil).Verify(nil, testChallenge, testPolicy())
			return err
		}},
		{"chain too long", func(t *testing.T) error {
			p := newPKI(t, nil)
			chain := [][]byte{p.leaf(elliptic.P256(), validKD)}
			for range 10 {
				chain = append(chain, p.intDER)
			}
			_, err := p.verifier(nil).Verify(chain, testChallenge, testPolicy())
			return err
		}},
		{"garbage certificate", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := p.verifier(nil).Verify([][]byte{p.leaf(elliptic.P256(), validKD), []byte("not a certificate")}, testChallenge, testPolicy())
			return err
		}},
		{"unconfigured verifier", func(t *testing.T) error {
			p := newPKI(t, nil)
			_, err := (&Verifier{}).Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
		{"nil verifier", func(t *testing.T) error {
			p := newPKI(t, nil)
			var v *Verifier
			_, err := v.Verify(p.chain(p.leaf(elliptic.P256(), validKD)), testChallenge, testPolicy())
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantCode(t, tt.run(t), plxerr.AttestationFailed)
		})
	}
}

func TestVerifyMalformedKeyDescription(t *testing.T) {
	// Verifies: SEC-002.
	valid := encodeKD(validSpec())
	cases := map[string][]byte{
		"empty":     {},
		"truncated": valid[:len(valid)/2],
		"trailing":  append(append([]byte(nil), valid...), 0x00),
		"integer":   {0x02, 0x01, 0x01},
	}
	for name, kd := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPKI(t, nil)
			_, err := p.verifier(nil).Verify(p.chain(p.leaf(elliptic.P256(), kd)), testChallenge, testPolicy())
			wantCode(t, err, plxerr.AttestationFailed)
		})
	}
}

func TestNewVerifier(t *testing.T) {
	// Verifies: SEC-002.
	pool := x509.NewCertPool()
	clock := func() time.Time { return testNow }
	if _, err := NewVerifier(nil, nil, clock); err == nil {
		t.Error("nil roots accepted")
	}
	if _, err := NewVerifier(pool, nil, nil); err == nil {
		t.Error("nil clock accepted")
	}
	v, err := NewVerifier(pool, nil, clock)
	if err != nil || v.Roots != pool || !v.Now().Equal(testNow) {
		t.Errorf("NewVerifier = %+v, %v", v, err)
	}
}

// Verifies: SEC-002, LIM-001.
func TestVerifyChainLengthBound(t *testing.T) {
	p := newPKI(t, nil)
	chain := p.chain(p.leaf(elliptic.P256(), encodeKD(validSpec())))

	def, _ := limits.Lookup(limits.AttestKeyAttestationChainCerts)
	tooLong := make([][]byte, def.Default+1)
	if _, err := p.verifier(nil).Verify(tooLong, testChallenge, testPolicy()); err == nil {
		t.Error("a chain beyond the registry default was accepted")
	}

	v := p.verifier(nil)
	v.MaxChain = int64(len(chain)) - 1
	_, err := v.Verify(chain, testChallenge, testPolicy())
	wantCode(t, err, plxerr.AttestationFailed)

	v.MaxChain = int64(len(chain))
	if _, err := v.Verify(chain, testChallenge, testPolicy()); err != nil {
		t.Errorf("a chain at the configured bound: %v", err)
	}
}
