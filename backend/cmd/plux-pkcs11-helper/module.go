// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

//go:build cgo && unix

package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/miekg/pkcs11"

	"github.com/nightCode42/plux3/backend/internal/pkcs11helper"
	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
)

// ckmEDDSA is CKM_EDDSA of PKCS#11 3.0, which the binding does not name.
const ckmEDDSA = 0x00001057

// module is a logged-in session with one token. PKCS#11 sessions are not
// safe for concurrent use, so every call holds the mutex.
type module struct {
	mu      sync.Mutex
	ctx     *pkcs11.Ctx
	slot    uint
	pin     string
	session pkcs11.SessionHandle
}

// module is a pkcs11helper.Token.
var _ pkcs11helper.Token = (*module)(nil)

// openModule loads the vendor library, finds the token by label and logs
// in. Errors name the step and the token's status code, never the PIN.
func openModule(path, label, pin string) (*module, error) {
	ctx := pkcs11.New(path)
	if ctx == nil {
		return nil, errors.New("load the PKCS#11 module")
	}
	if err := ctx.Initialize(); err != nil { //nolint:misspell // the binding's name
		ctx.Destroy()
		return nil, fmt.Errorf("initialise the PKCS#11 module: %w", err)
	}
	m := &module{ctx: ctx, pin: pin}
	slot, err := m.findSlot(label)
	if err != nil {
		m.close()
		return nil, err
	}
	m.slot = slot
	if err := m.login(); err != nil {
		m.close()
		return nil, err
	}
	return m, nil
}

// findSlot returns the slot of the token with the label.
func (m *module) findSlot(label string) (uint, error) {
	slots, err := m.ctx.GetSlotList(true)
	if err != nil {
		return 0, fmt.Errorf("list the PKCS#11 slots: %w", err)
	}
	for _, slot := range slots {
		info, err := m.ctx.GetTokenInfo(slot)
		if err == nil && strings.TrimRight(info.Label, " ") == label {
			return slot, nil
		}
	}
	return 0, errors.New("no token with the given label is present")
}

// login opens a session on the slot and logs in as the user.
func (m *module) login() error {
	session, err := m.ctx.OpenSession(m.slot, pkcs11.CKF_SERIAL_SESSION)
	if err != nil {
		return fmt.Errorf("open a PKCS#11 session: %w", err)
	}
	if err := m.ctx.Login(session, pkcs11.CKU_USER, m.pin); err != nil && !isCode(err, pkcs11.CKR_USER_ALREADY_LOGGED_IN) {
		_ = m.ctx.CloseSession(session)
		return fmt.Errorf("log in to the token: %w", err)
	}
	m.session = session
	return nil
}

// close ends the session and unloads the library.
func (m *module) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != 0 {
		_ = m.ctx.Logout(m.session)
		_ = m.ctx.CloseSession(m.session)
	}
	_ = m.ctx.Finalize() //nolint:misspell // the binding's name
	m.ctx.Destroy()
}

// isCode reports whether err is the PKCS#11 return value.
func isCode(err error, code uint) bool {
	var perr pkcs11.Error
	return errors.As(err, &perr) && uint(perr) == code
}

// stale reports whether err means the session is gone, as an HSM ends an
// idle one.
func stale(err error) bool {
	return isCode(err, pkcs11.CKR_SESSION_HANDLE_INVALID) ||
		isCode(err, pkcs11.CKR_SESSION_CLOSED) ||
		isCode(err, pkcs11.CKR_USER_NOT_LOGGED_IN)
}

// retry runs op and, when the session turns out to be gone, logs in again
// and runs it once more. The caller holds the mutex.
func (m *module) retry(op func() error) error {
	err := op()
	if err == nil || !stale(err) {
		return err
	}
	if lerr := m.login(); lerr != nil {
		return lerr
	}
	return op()
}

// PublicKey reads the curve and point of the public key a selector names.
func (m *module) PublicKey(sel pkcs11helper.Selector) (pkcs11helper.PublicAttributes, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var attrs pkcs11helper.PublicAttributes
	err := m.retry(func() error {
		obj, err := m.find(sel, pkcs11.CKO_PUBLIC_KEY)
		if err != nil {
			return err
		}
		values, err := m.ctx.GetAttributeValue(m.session, obj, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, nil),
			pkcs11.NewAttribute(pkcs11.CKA_EC_POINT, nil),
		})
		if err != nil {
			return fmt.Errorf("read a public key: %w", err)
		}
		if len(values) != 2 {
			return errors.New("read a public key: the token returned too few attributes")
		}
		attrs = pkcs11helper.PublicAttributes{ECParams: values[0].Value, ECPoint: values[1].Value}
		return nil
	})
	return attrs, err
}

// Sign signs data with the private key a selector names.
func (m *module) Sign(sel pkcs11helper.Selector, alg pkcs11pb.Algorithm, data []byte) ([]byte, error) {
	var mechanism uint
	switch alg {
	case pkcs11pb.Algorithm_ALGORITHM_ED25519:
		mechanism = ckmEDDSA
	case pkcs11pb.Algorithm_ALGORITHM_ECDSA_P256_SHA256:
		mechanism = pkcs11.CKM_ECDSA
	case pkcs11pb.Algorithm_ALGORITHM_UNSPECIFIED:
		fallthrough
	default:
		return nil, errors.New("sign: unsupported algorithm")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var sig []byte
	err := m.retry(func() error {
		obj, err := m.find(sel, pkcs11.CKO_PRIVATE_KEY)
		if err != nil {
			return err
		}
		if err := m.ctx.SignInit(m.session, []*pkcs11.Mechanism{pkcs11.NewMechanism(mechanism, nil)}, obj); err != nil {
			return fmt.Errorf("start a signature: %w", err)
		}
		sig, err = m.ctx.Sign(m.session, data)
		if err != nil {
			return fmt.Errorf("sign: %w", err)
		}
		return nil
	})
	return sig, err
}

// find returns the one object of a class that the selector matches.
func (m *module) find(sel pkcs11helper.Selector, class uint) (pkcs11.ObjectHandle, error) {
	template := []*pkcs11.Attribute{pkcs11.NewAttribute(pkcs11.CKA_CLASS, class)}
	if sel.Label != "" {
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_LABEL, sel.Label))
	}
	if sel.ID != nil {
		template = append(template, pkcs11.NewAttribute(pkcs11.CKA_ID, sel.ID))
	}
	if err := m.ctx.FindObjectsInit(m.session, template); err != nil {
		return 0, fmt.Errorf("search for a key: %w", err)
	}
	objects, _, findErr := m.ctx.FindObjects(m.session, 2)
	if err := m.ctx.FindObjectsFinal(m.session); err != nil && findErr == nil {
		findErr = err
	}
	if findErr != nil {
		return 0, fmt.Errorf("search for a key: %w", findErr)
	}
	switch len(objects) {
	case 0:
		return 0, pkcs11helper.ErrKeyNotFound
	case 1:
		return objects[0], nil
	default:
		return 0, pkcs11helper.ErrAmbiguousKey
	}
}

// gcmTagBits is the 128-bit tag of the wrapping format.
const gcmTagBits = 128

// Encrypt encrypts with CKM_AES_GCM under the secret key a selector
// names. It returns the IV the module used, read back after the
// operation, since some modules write their own.
func (m *module) Encrypt(sel pkcs11helper.Selector, iv, plaintext []byte) ([]byte, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var used, sealed []byte
	err := m.retry(func() error {
		obj, err := m.find(sel, pkcs11.CKO_SECRET_KEY)
		if err != nil {
			return err
		}
		params := pkcs11.NewGCMParams(iv, nil, gcmTagBits)
		defer params.Free()
		mechanism := []*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_AES_GCM, params)}
		if err := m.ctx.EncryptInit(m.session, mechanism, obj); err != nil {
			return fmt.Errorf("start an encryption: %w", err)
		}
		sealed, err = m.ctx.Encrypt(m.session, plaintext)
		if err != nil {
			return fmt.Errorf("encrypt: %w", err)
		}
		used = params.IV()
		return nil
	})
	return used, sealed, err
}

// Decrypt decrypts with CKM_AES_GCM. A ciphertext the token refuses as
// invalid is ErrDecryptFailed.
func (m *module) Decrypt(sel pkcs11helper.Selector, iv, sealed []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var plaintext []byte
	err := m.retry(func() error {
		obj, err := m.find(sel, pkcs11.CKO_SECRET_KEY)
		if err != nil {
			return err
		}
		params := pkcs11.NewGCMParams(iv, nil, gcmTagBits)
		defer params.Free()
		mechanism := []*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_AES_GCM, params)}
		if err := m.ctx.DecryptInit(m.session, mechanism, obj); err != nil {
			return fmt.Errorf("start a decryption: %w", err)
		}
		plaintext, err = m.ctx.Decrypt(m.session, sealed)
		if invalidCiphertext(err) {
			return pkcs11helper.ErrDecryptFailed
		}
		if err != nil {
			return fmt.Errorf("decrypt: %w", err)
		}
		return nil
	})
	return plaintext, err
}

// invalidCiphertext reports whether err is the token saying the
// ciphertext or its tag is wrong. SoftHSM reports a failed GCM tag check
// as CKR_GENERAL_ERROR, so that is read as a failed decryption too: only
// after a successful DecryptInit does it reach here, and the answer is
// the same either way, a data key that cannot be had.
func invalidCiphertext(err error) bool {
	return isCode(err, pkcs11.CKR_GENERAL_ERROR) ||
		isCode(err, pkcs11.CKR_ENCRYPTED_DATA_INVALID) ||
		isCode(err, pkcs11.CKR_ENCRYPTED_DATA_LEN_RANGE) ||
		isCode(err, pkcs11.CKR_DATA_INVALID)
}
