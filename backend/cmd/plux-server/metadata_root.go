// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nightCode42/plux3/backend/internal/signing"
	"github.com/nightCode42/plux3/backend/internal/updatemeta"
)

// rootUsage is the usage of the offline root ceremony commands.
const rootUsage = `usage: metadata root-key-export -key <ref> [-pkcs11-socket <path>] [-key-dir <dir>]
       metadata root-new -type production|development -version <n> -expires <time|duration>
                         -threshold <n> -key <public key file|ref> ... -online-keys <file> [-pins <file>] -out <file>
       metadata root-sign -in <file> -out <file> -key <ref> [-previous <file>]
       metadata root-verify -in <file> [-previous <file>]
A <ref> is pkcs11:object=<label> (with -pkcs11-socket) or file:<name> (with -key-dir, development only).
`

// productionRootThreshold is the fewest offline signatures root-new accepts
// for a production root (SEC-051); the server enforces its configured
// updateMetadata.rootThreshold again when the root is uploaded.
const productionRootThreshold = 2

// Prefixes of a key reference.
const (
	pkcs11Prefix = "pkcs11:object="
	filePrefix   = "file:"
)

// rootCommands are the metadata subcommands of the offline ceremony. They
// never read the configuration and never open a database: a root key lives
// on an offline machine that has neither.
var rootCommands = []string{"root-key-export", "root-new", "root-sign", "root-verify"}

// rootCommand runs one ceremony subcommand (SEC-051, SEC-121).
func rootCommand(ctx context.Context, args []string, now func() time.Time, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(name+" metadata "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o rootOptions
	o.register(fs)
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		_, _ = fmt.Fprint(stderr, name+": "+rootUsage)
		return exitUsage
	}
	var err error
	switch args[0] {
	case "root-key-export":
		err = o.keyExport(ctx, stdout, stderr)
	case "root-new":
		err = o.newRoot(ctx, now(), stdout, stderr)
	case "root-sign":
		err = o.signRoot(ctx, now(), stdout, stderr)
	default:
		err = o.verifyRoot(now(), stdout)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s metadata: %s\n", name, err)
		if errors.Is(err, errRootUsage) {
			return exitUsage
		}
		return exitFailed
	}
	return exitOK
}

// errRootUsage marks a missing or contradictory flag.
var errRootUsage = errors.New("missing or invalid flags; see the usage")

// rootOptions are the flags of the ceremony subcommands; each uses some.
type rootOptions struct {
	in, out, previous, onlineKeys, pins string
	typ, expires, socket, keyDir        string
	version                             int64
	threshold                           int
	key                                 string
	keys                                []string
}

// register declares the flags.
func (o *rootOptions) register(fs *flag.FlagSet) {
	fs.StringVar(&o.in, "in", "", "the root file to read")
	fs.StringVar(&o.out, "out", "", "the file to write")
	fs.StringVar(&o.previous, "previous", "", "the root this one replaces")
	fs.StringVar(&o.onlineKeys, "online-keys", "", "the output of 'metadata keys'")
	fs.StringVar(&o.pins, "pins", "", "a JSON object of host name to at least two SPKI SHA-256 pins")
	fs.StringVar(&o.typ, "type", "", "production or development")
	fs.StringVar(&o.expires, "expires", "", "RFC 3339 time, or a duration from now")
	fs.StringVar(&o.socket, "pkcs11-socket", "", "the PKCS#11 helper's socket")
	fs.StringVar(&o.keyDir, "key-dir", "", "the directory of file keys (development only)")
	fs.Int64Var(&o.version, "version", 0, "the root's version")
	fs.IntVar(&o.threshold, "threshold", 0, "the signatures the root role requires")
	fs.Func("key", "a key: a reference, or for root-new a public key file; repeatable there", func(v string) error {
		o.key = v
		o.keys = append(o.keys, v)
		return nil
	})
}

// keyExport prints the public half of a key as PEM, for root-new.
func (o *rootOptions) keyExport(ctx context.Context, stdout, stderr io.Writer) error {
	if o.key == "" {
		return errRootUsage
	}
	pub, id, err := o.publicKey(ctx, o.key)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return fmt.Errorf("encode the public key: %w", err)
	}
	if err := pem.Encode(stdout, &pem.Block{Type: "PUBLIC KEY", Bytes: der}); err != nil {
		return fmt.Errorf("write the public key: %w", err)
	}
	_, _ = fmt.Fprintf(stderr, "key id %s\n", id)
	return nil
}

// signer opens the backend a key reference names and returns the label.
// A file key is announced as unfit for production.
func (o *rootOptions) signer(ref string, warn io.Writer) (signing.Signer, string, error) {
	switch {
	case strings.HasPrefix(ref, pkcs11Prefix):
		p, err := signing.NewPKCS11(signing.PKCS11Options{Socket: o.socket})
		if err != nil {
			return nil, "", fmt.Errorf("%w: -pkcs11-socket", errRootUsage)
		}
		return p, strings.TrimPrefix(ref, pkcs11Prefix), nil
	case strings.HasPrefix(ref, filePrefix):
		if o.keyDir == "" {
			return nil, "", fmt.Errorf("%w: -key-dir", errRootUsage)
		}
		f, err := signing.NewFile(o.keyDir)
		if err != nil {
			return nil, "", fmt.Errorf("file keys: %w", err)
		}
		_, _ = fmt.Fprintln(warn, name+" metadata: warning: a file key is for development only; a production root key belongs on a hardware token")
		return f, strings.TrimPrefix(ref, filePrefix), nil
	}
	return nil, "", fmt.Errorf("%w: a key reference starts with %q or %q", errRootUsage, pkcs11Prefix, filePrefix)
}

// publicKey reads the public half of a key reference, or of a PEM file.
func (o *rootOptions) publicKey(ctx context.Context, spec string) (ed25519.PublicKey, string, error) {
	if !strings.HasPrefix(spec, pkcs11Prefix) && !strings.HasPrefix(spec, filePrefix) {
		return readPublicFile(spec)
	}
	s, ref, err := o.signer(spec, io.Discard)
	if err != nil {
		return nil, "", err
	}
	pub, id, err := s.PublicKey(ctx, ref)
	if err != nil {
		return nil, "", fmt.Errorf("public key %s: %w", spec, err)
	}
	return pub, id, nil
}

// readPublicFile reads an Ed25519 public key in PEM (PKIX) form.
func readPublicFile(path string) (ed25519.PublicKey, string, error) {
	raw, err := readLimited(path)
	if err != nil {
		return nil, "", err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, "", fmt.Errorf("%s is not a PEM public key", path)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, "", fmt.Errorf("%s is not an Ed25519 key; root keys are Ed25519", path)
	}
	return pub, updatemeta.KeyID(pub), nil
}

// readLimited reads a small file named by the operator.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the operator names the file
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxRootFile+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxRootFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxRootFile)
	}
	return raw, nil
}

// newRoot writes an unsigned root document.
func (o *rootOptions) newRoot(ctx context.Context, now time.Time, stdout, stderr io.Writer) error {
	if o.out == "" || o.onlineKeys == "" || len(o.keys) == 0 || o.version < 1 {
		return errRootUsage
	}
	expires, err := o.expiry(now)
	if err != nil {
		return err
	}
	if err := o.checkType(now, expires, stderr); err != nil {
		return err
	}
	spec := updatemeta.RootSpec{Version: o.version, Expires: expires, Root: updatemeta.RoleSpec{Threshold: o.threshold}}
	for _, k := range o.keys {
		pub, _, err := o.publicKey(ctx, k)
		if err != nil {
			return err
		}
		spec.Root.Keys = append(spec.Root.Keys, updatemeta.Key{Alg: updatemeta.AlgEd25519, Public: base64.StdEncoding.EncodeToString(pub)})
	}
	if err := o.addOnlineKeys(&spec); err != nil {
		return err
	}
	if spec.Pins, err = o.readPins(); err != nil {
		return err
	}
	root, err := updatemeta.NewRoot(spec)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	doc, err := updatemeta.Marshal(root, nil)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	if err := os.WriteFile(o.out, doc, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", o.out, err)
	}
	_, _ = fmt.Fprintf(stdout, "wrote unsigned root %d to %s: expires %s, %d of %d root keys must sign\n",
		root.Version, o.out, root.Expires, root.Roles.Root.Threshold, len(root.Roles.Root.KeyIDs))
	for _, id := range root.Roles.Root.KeyIDs {
		_, _ = fmt.Fprintf(stdout, "root key %s\n", id)
	}
	return nil
}

// readPins reads the file of -pins, a JSON object of host name to pins; the
// root checks the hosts and pins as it does for any upload (SEC-041).
func (o *rootOptions) readPins() (map[string][]string, error) {
	if o.pins == "" {
		return nil, nil
	}
	raw, err := readLimited(o.pins)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var pins map[string][]string
	if err := dec.Decode(&pins); err != nil || dec.More() || len(pins) == 0 {
		return nil, fmt.Errorf("%s must be a JSON object of host name to a list of pins", o.pins)
	}
	return pins, nil
}

// expiry reads -expires: an RFC 3339 time, or a duration from now.
func (o *rootOptions) expiry(now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, o.expires); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(o.expires)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("%w: -expires is neither an RFC 3339 time nor a positive duration", errRootUsage)
	}
	return now.Add(d), nil
}

// checkType applies the rules of the environment type. A production root
// asks at least productionRootThreshold signatures and expires within the
// year the specification allows (SEC-050); a development root is allowed
// anything, with a warning that it must never reach production.
func (o *rootOptions) checkType(now, expires time.Time, stderr io.Writer) error {
	switch o.typ {
	case updatemeta.EnvProduction:
		if o.threshold < productionRootThreshold {
			return fmt.Errorf("a production root needs a threshold of at least %d, not %d", productionRootThreshold, o.threshold)
		}
		if !expires.After(now) || expires.After(now.Add(updatemeta.DefaultExpiry().Root)) {
			return fmt.Errorf("a production root must expire within %s from now", updatemeta.DefaultExpiry().Root)
		}
	case updatemeta.EnvDevelopment:
		_, _ = fmt.Fprintln(stderr, name+" metadata: warning: a development root; never upload it to a production environment")
	default:
		return fmt.Errorf("%w: -type is production or development", errRootUsage)
	}
	return nil
}

// addOnlineKeys reads the output of 'metadata keys' into the spec: each of
// targets, snapshot and timestamp is signed by its key alone.
func (o *rootOptions) addOnlineKeys(spec *updatemeta.RootSpec) error {
	raw, err := readLimited(o.onlineKeys)
	if err != nil {
		return err
	}
	roles := map[string]*updatemeta.RoleSpec{
		updatemeta.RoleTargets: &spec.Targets, updatemeta.RoleSnapshot: &spec.Snapshot, updatemeta.RoleTimestamp: &spec.Timestamp,
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		role, ok := roles[f[0]]
		if len(f) != 4 || !ok || f[2] != updatemeta.AlgEd25519 {
			return fmt.Errorf("%s: unexpected line %q; expected '<targets|snapshot|timestamp> <keyid> Ed25519 <base64>'", o.onlineKeys, f[0])
		}
		pub, err := base64.StdEncoding.DecodeString(f[3])
		if err != nil || updatemeta.KeyID(pub) != f[1] {
			return fmt.Errorf("%s: the key %s does not match its identifier", o.onlineKeys, f[1])
		}
		role.Keys = append(role.Keys, updatemeta.Key{Alg: f[2], Public: f[3]})
		role.Threshold = 1
	}
	for role, rs := range roles {
		if len(rs.Keys) == 0 {
			return fmt.Errorf("%s lists no %s key", o.onlineKeys, role)
		}
	}
	return nil
}

// signRoot appends this holder's signature to a root file.
func (o *rootOptions) signRoot(ctx context.Context, now time.Time, stdout, stderr io.Writer) error {
	if o.in == "" || o.out == "" || o.key == "" {
		return errRootUsage
	}
	raw, err := readLimited(o.in)
	if err != nil {
		return err
	}
	d, err := updatemeta.ParseDocument(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", o.in, err)
	}
	root, err := updatemeta.ParseRoot(d.Signed)
	if err != nil {
		return fmt.Errorf("%s: %w", o.in, err)
	}
	if exp, _ := time.Parse(time.RFC3339, root.Expires); !now.Before(exp) {
		return fmt.Errorf("root %d expired at %s", root.Version, root.Expires)
	}
	allowed := slices.Clone(root.Roles.Root.KeyIDs)
	if o.previous != "" {
		prev, err := o.previousRoot()
		if err != nil {
			return err
		}
		allowed = append(allowed, prev.Roles.Root.KeyIDs...)
	}
	signer, ref, err := o.signer(o.key, stderr)
	if err != nil {
		return err
	}
	if strings.HasPrefix(o.key, filePrefix) {
		// A file backend would make a key that does not exist; a
		// misspelt name must not become a new signer.
		if _, err := os.Stat(filepath.Join(o.keyDir, ref+".ed25519")); err != nil {
			return fmt.Errorf("file key %s: %w", ref, err)
		}
	}
	_, id, err := signer.PublicKey(ctx, ref)
	if err != nil {
		return fmt.Errorf("key %s: %w", o.key, err)
	}
	if !slices.Contains(allowed, id) {
		return fmt.Errorf("key %s is not a root key of this document or of the previous root", id)
	}
	_, sig, err := updatemeta.Sign(ctx, signer, ref, d.Signed)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	doc, err := updatemeta.AddSignature(raw, sig)
	if err != nil {
		return fmt.Errorf("add the signature: %w", err)
	}
	if err := os.WriteFile(o.out, doc, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", o.out, err)
	}
	sum := sha256.Sum256(d.Signed)
	_, _ = fmt.Fprintf(stdout, "signed root %d (expires %s, signed part sha256 %s) with key %s\n", root.Version, root.Expires, hex.EncodeToString(sum[:]), id)
	return nil
}

// previousRoot reads the root named by -previous.
func (o *rootOptions) previousRoot() (updatemeta.Root, error) {
	raw, err := readLimited(o.previous)
	if err != nil {
		return updatemeta.Root{}, err
	}
	d, err := updatemeta.ParseDocument(raw)
	if err != nil {
		return updatemeta.Root{}, fmt.Errorf("%s: %w", o.previous, err)
	}
	r, err := updatemeta.ParseRoot(d.Signed)
	if err != nil {
		return updatemeta.Root{}, fmt.Errorf("%s: %w", o.previous, err)
	}
	return r, nil
}

// verifyRoot checks a root file, and a rotation when -previous is given.
func (o *rootOptions) verifyRoot(now time.Time, stdout io.Writer) error {
	if o.in == "" {
		return errRootUsage
	}
	raw, err := readLimited(o.in)
	if err != nil {
		return err
	}
	var prev []byte
	if o.previous != "" {
		if prev, err = readLimited(o.previous); err != nil {
			return err
		}
	}
	rep, err := updatemeta.CheckRoot(raw, prev, now)
	if err != nil {
		return fmt.Errorf("%s: %w", o.in, err)
	}
	_, _ = fmt.Fprintf(stdout, "✓ root %d valid until %s: %d of %d required root signatures (%s)\n",
		rep.Root.Version, rep.Root.Expires, len(rep.Signers), rep.Root.Roles.Root.Threshold, strings.Join(rep.Signers, ","))
	if prev != nil {
		_, _ = fmt.Fprintf(stdout, "✓ rotation: %d signatures under the previous root's keys (%s)\n", len(rep.Previous), strings.Join(rep.Previous, ","))
	}
	return nil
}
