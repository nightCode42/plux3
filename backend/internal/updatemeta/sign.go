// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package updatemeta

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/nightCode42/plux3/backend/internal/signing"
)

// Sign signs a signed part with the named key and returns the finished
// file. The signature names its algorithm (SEC-122). Only the worker role
// holds a Signer (ADR-0006).
func Sign(ctx context.Context, signer signing.Signer, keyRef string, signed any) ([]byte, Signature, error) {
	canonical, err := Canonical(signed)
	if err != nil {
		return nil, Signature{}, err
	}
	sig, keyID, err := signer.Sign(ctx, keyRef, canonical)
	if err != nil {
		return nil, Signature{}, fmt.Errorf("updatemeta: sign: %w", err)
	}
	s := Signature{KeyID: keyID, Alg: AlgEd25519, Sig: base64.StdEncoding.EncodeToString(sig)}
	doc, err := Marshal(signed, []Signature{s})
	if err != nil {
		return nil, Signature{}, err
	}
	return doc, s, nil
}
