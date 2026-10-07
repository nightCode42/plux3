// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package keyattest

import (
	"encoding/json"
	"math/big"
	"strings"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Revocations is Google's attestation certificate status list, indexed by
// certificate serial number. It is immutable after ParseRevocations and safe
// for concurrent use. A nil *Revocations lists nothing.
type Revocations struct {
	entries map[string]revocation
}

type revocation struct {
	status string
}

type revocationFile struct {
	Entries map[string]struct {
		Status string `json:"status"`
	} `json:"entries"`
}

// ParseRevocations parses the JSON status list published at
// https://android.googleapis.com/attestation/status. Serial numbers are
// normalised, so letter case and leading zeros do not matter. Every listed
// serial fails a chain, whatever its status (REVOKED, SUSPENDED or any value
// added later).
func ParseRevocations(data []byte) (*Revocations, error) {
	var f revocationFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, plxerr.Wrap(plxerr.AttestationFailed, err, "revocation list is not valid JSON")
	}
	if f.Entries == nil {
		return nil, plxerr.New(plxerr.AttestationFailed, "revocation list has no entries object")
	}
	r := &Revocations{entries: make(map[string]revocation, len(f.Entries))}
	for serial, e := range f.Entries {
		n, ok := new(big.Int).SetString(serial, 16)
		if !ok || n.Sign() < 0 {
			return nil, plxerr.New(plxerr.AttestationFailed, "revocation list holds a serial that is not hexadecimal")
		}
		r.entries[n.Text(16)] = revocation{status: strings.ToUpper(e.Status)}
	}
	return r, nil
}

// Len returns the number of listed serial numbers.
func (r *Revocations) Len() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// status returns the listed status of a serial number.
func (r *Revocations) status(serial *big.Int) (string, bool) {
	if r == nil || serial == nil {
		return "", false
	}
	e, ok := r.entries[serial.Text(16)]
	return e.status, ok
}
