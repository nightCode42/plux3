// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package playintegrity

import (
	"crypto/sha256"
	"encoding/base64"
)

// RequestHash returns the value the device must send as the standard
// request's requestHash: base64url without padding of
// SHA-256(challenge || jkt), binding the server challenge to the thumbprint
// of the DPoP key. The same binding is used for App Attest.
func RequestHash(challenge []byte, jkt string) string {
	h := sha256.New()
	h.Write(challenge)
	h.Write([]byte(jkt))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
