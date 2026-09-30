// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package release

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
	"github.com/nightCode42/plux3/backend/internal/schema/jcs"
	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/storage"
	"github.com/nightCode42/plux3/backend/internal/storage/dbgen"
)

// updateManifestVectors rewrites the device verification vectors.
var updateManifestVectors = flag.Bool("update", false, "rewrite schema/testdata/manifest/vectors.json")

// manifestVectorsPath is read by the runtime's manifest verifier tests
// (ADR-0029).
var manifestVectorsPath = filepath.Join("..", "..", "..", "schema", "testdata", "manifest", "vectors.json")

type vectorKey struct {
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Role      string `json:"role"`
	PublicKey string `json:"publicKey"`
}

// manifestCase is one manifest as a device receives it, the context it
// is verified in, and the outcome ADR-0029 requires.
type manifestCase struct {
	Name            string              `json:"name"`
	Signed          string              `json:"signed"`
	Signatures      []ManifestSignature `json:"signatures"`
	App             string              `json:"app"`
	Environment     string              `json:"environment"`
	Channel         string              `json:"channel"`
	Now             string              `json:"now"`
	HighestAccepted int64               `json:"highestAccepted"`
	Runtime         string              `json:"runtime"`
	Error           string              `json:"error,omitempty"`
}

// bundleSignatureCase is a bundle hash signed as publish signs it, for
// the baseline check.
type bundleSignatureCase struct {
	Name      string `json:"name"`
	Hash      string `json:"hash"`
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
	Valid     bool   `json:"valid"`
}

type manifestVectors struct {
	Comment     string                `json:"$comment"`
	Keys        []vectorKey           `json:"keys"`
	Unsupported []string              `json:"unsupportedFeatures"`
	Manifests   []manifestCase        `json:"manifests"`
	Bundles     []bundleSignatureCase `json:"bundles"`
}

// Verifies: SEC-052, SEC-055, BND-008.
// The vectors are signed by the P2 signer, so the device's verifier is
// checked against what the worker produces, and they pin the verification
// order and error codes of ADR-0029.
func TestDeviceVerificationVectors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	for ref, seed := range map[string]byte{"targets": 1, "root": 2, "other": 3} {
		key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
		if err := os.WriteFile(filepath.Join(dir, ref+".ed25519"), key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	signer, err := signing.NewFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := func(ref, role string) vectorKey {
		pub, id, err := signer.PublicKey(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return vectorKey{KeyID: id, Algorithm: signing.Algorithm, Role: role, PublicKey: hex.EncodeToString(pub)}
	}
	sign := func(ref string, msg []byte) ManifestSignature {
		sig, id, err := signer.Sign(ctx, ref, msg)
		if err != nil {
			t.Fatal(err)
		}
		return ManifestSignature{KeyID: id, Algorithm: signing.Algorithm, Signature: base64.StdEncoding.EncodeToString(sig)}
	}
	canonical := func(m SignedManifest) []byte {
		plain, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		out, err := jcs.Canonicalize(plain, manifestDepth)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	hash := sha256.Sum256([]byte("app bundle"))
	// Built as the worker builds it, from stored rows: a channel without
	// controls, whose lists must be empty rather than null for the device
	// to read them.
	appID := storage.MustUUID("01a0c450-6c00-7010-8000-00000001eef0")
	appVersion := dbgen.PluginVersion{BundleSha256: hash[:], BundleSize: 48213, RequiredFeatures: []string{"pxl.v1"}, MinRuntime: "0.1.0"}
	base := newManifest(
		dbgen.Environment{AppID: appID, Key: "production"}, dbgen.Channel{Key: "production"}, 231, appVersion,
		[]dbgen.PluginVersion{appVersion, {
			PluginKey: "loans", Version: 14, BundleSha256: bytes.Repeat([]byte{7}, 32), BundleSize: 182334,
			RequiredFeatures: []string{"widget.Text.v1", "pxl.v1"}, MinRuntime: "0.1.0",
		}},
		dbgen.ChannelControl{}, time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
	)
	ok := func(name string, m SignedManifest, mutate func(*manifestCase)) manifestCase {
		signed := canonical(m)
		c := manifestCase{
			Name: name, Signed: base64.StdEncoding.EncodeToString(signed), Signatures: []ManifestSignature{sign("targets", signed)},
			App: storage.ID(appID), Environment: "production", Channel: "production",
			Now: "2026-09-28T12:00:00Z", HighestAccepted: 230, Runtime: "0.1.0",
		}
		if mutate != nil {
			mutate(&c)
		}
		return c
	}
	with := func(f func(*SignedManifest)) SignedManifest {
		m := base
		m.Plugins = append([]SignedPlugin(nil), base.Plugins...)
		f(&m)
		return m
	}
	code := func(c plxerr.Code) string { return c.String() }
	pretty, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	cases := []manifestCase{
		ok("valid", base, nil),
		ok("same-sequence-accepted", base, func(c *manifestCase) { c.HighestAccepted = 231 }),
		ok("first-release", base, func(c *manifestCase) { c.HighestAccepted = 0 }),
		ok("one-good-signature-of-two", base, func(c *manifestCase) {
			c.Signatures = append([]ManifestSignature{sign("other", []byte("x"))}, c.Signatures...)
		}),
		ok("rollback", base, func(c *manifestCase) { c.HighestAccepted = 232; c.Error = code(plxerr.RollbackRejected) }),
		ok("expired", base, func(c *manifestCase) { c.Now = "2026-10-02T10:00:00Z"; c.Error = code(plxerr.ManifestExpired) }),
		ok("bad-signature", base, func(c *manifestCase) {
			sig, _ := base64.StdEncoding.DecodeString(c.Signatures[0].Signature)
			sig[10] ^= 1
			c.Signatures[0].Signature = base64.StdEncoding.EncodeToString(sig)
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("tampered", base, func(c *manifestCase) {
			signed, _ := base64.StdEncoding.DecodeString(c.Signed)
			c.Signed = base64.StdEncoding.EncodeToString(bytes.Replace(signed, []byte(`"releaseSequence":231`), []byte(`"releaseSequence":239`), 1))
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("unknown-key", base, func(c *manifestCase) {
			signed, _ := base64.StdEncoding.DecodeString(c.Signed)
			c.Signatures = []ManifestSignature{sign("other", signed)}
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("key-of-another-role", base, func(c *manifestCase) {
			signed, _ := base64.StdEncoding.DecodeString(c.Signed)
			c.Signatures = []ManifestSignature{sign("root", signed)}
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("unknown-algorithm", base, func(c *manifestCase) {
			c.Signatures[0].Algorithm = "ecdsa-p256-sha256"
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("no-signature", base, func(c *manifestCase) {
			c.Signatures = []ManifestSignature{}
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("not-canonical", base, func(c *manifestCase) {
			c.Signed = base64.StdEncoding.EncodeToString(pretty)
			c.Signatures = []ManifestSignature{sign("targets", pretty)}
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("another-app", base, func(c *manifestCase) { c.App = "app_other"; c.Error = code(plxerr.ManifestSignatureInvalid) }),
		ok("another-channel", base, func(c *manifestCase) { c.Channel = "beta"; c.Error = code(plxerr.ManifestSignatureInvalid) }),
		ok("another-environment", base, func(c *manifestCase) { c.Environment = "staging"; c.Error = code(plxerr.ManifestSignatureInvalid) }),
		ok("another-role", with(func(m *SignedManifest) { m.Role = "snapshot" }), func(c *manifestCase) {
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("another-type", with(func(m *SignedManifest) { m.Type = "control" }), func(c *manifestCase) {
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("newer-spec-version", with(func(m *SignedManifest) { m.SpecVersion = 2 }), func(c *manifestCase) {
			c.Error = code(plxerr.ManifestSignatureInvalid)
		}),
		ok("unsupported-feature", with(func(m *SignedManifest) {
			m.Plugins[0].RequiredFeatures = []string{"pxl.v1", "widget.Hologram.v1"}
		}), func(c *manifestCase) { c.Error = code(plxerr.UnsupportedRequiredFeature) }),
		ok("runtime-too-old", with(func(m *SignedManifest) { m.AppBundle.MinRuntime = "9.0.0" }), func(c *manifestCase) {
			c.Error = code(plxerr.UnsupportedRequiredFeature)
		}),
		ok("kill-switch", with(func(m *SignedManifest) { m.Control.KillSwitches = []string{"loans"}; m.Control.Message = "Maintenance" }), nil),
	}
	signedHash := func(name string, h []byte, ref string, valid bool, damage bool) bundleSignatureCase {
		s := sign(ref, h)
		if damage {
			sig, _ := base64.StdEncoding.DecodeString(s.Signature)
			sig[0] ^= 1
			s.Signature = base64.StdEncoding.EncodeToString(sig)
		}
		return bundleSignatureCase{Name: name, Hash: hex.EncodeToString(h), KeyID: s.KeyID, Algorithm: s.Algorithm, Signature: s.Signature, Valid: valid}
	}
	vs := manifestVectors{
		Comment: "Device verification vectors (SEC-052, SEC-055, ADR-0029), signed by the P2 file signer and written by backend/internal/release (go test -update). " +
			"Bytes are base64, keys hex. Each manifest is verified with the listed keys, in its app, environment and channel, at `now`, after `highestAccepted`, by a runtime of version `runtime` that lacks unsupportedFeatures; error is the outcome ADR-0029 requires.",
		Keys:        []vectorKey{key("targets", "targets"), key("root", "root")},
		Unsupported: []string{"widget.Hologram.v1"},
		Manifests:   cases,
		Bundles: []bundleSignatureCase{
			signedHash("signed-by-targets", hash[:], "targets", true, false),
			signedHash("damaged", hash[:], "targets", false, true),
			signedHash("unknown-key", hash[:], "other", false, false),
		},
	}
	out, err := json.MarshalIndent(vs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	if *updateManifestVectors {
		if err := os.MkdirAll(filepath.Dir(manifestVectorsPath), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestVectorsPath, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(manifestVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, golden) {
		t.Errorf("%s differs; run the test with -update and review", manifestVectorsPath)
	}
}
