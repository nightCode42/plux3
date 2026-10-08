// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package pkcs11helper

import (
	"context"
	"crypto/rand"
	"log/slog"

	"github.com/nightCode42/plux3/backend/internal/pkcs11pb"
)

// Sizes of the AES-GCM wrapping format: the wrapped key is the IV, the
// ciphertext, then the tag.
const (
	// ivSize is the 96-bit IV.
	ivSize = 12
	// tagSize is the 128-bit tag.
	tagSize = 16
)

// wrap encrypts a data key. The IV is drawn here from the system's
// CSPRNG and handed to the token, so the helper never depends on how a
// module generates one; if the module insists on its own, the one it
// used is what is returned.
func (s *Server) wrap(ctx context.Context, req *pkcs11pb.WrapRequest) *pkcs11pb.Response {
	plaintext := req.GetPlaintext()
	if len(plaintext) == 0 {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "there is no data key to wrap")
	}
	sel, err := ParseKeyRef(req.GetKeyRef())
	if err != nil {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, err.Error())
	}
	iv := make([]byte, ivSize)
	if _, err := rand.Read(iv); err != nil {
		s.log.ErrorContext(ctx, "pkcs11 helper: no random IV", slog.Any("error", err))
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "no random IV")
	}
	used, sealed, err := s.token.Encrypt(sel, iv, plaintext)
	if err != nil {
		return s.tokenFailure(ctx, "wrap", err)
	}
	if len(used) != ivSize || len(sealed) != len(plaintext)+tagSize {
		s.log.ErrorContext(ctx, "pkcs11 helper: the token returned a malformed ciphertext", slog.Int("iv", len(used)), slog.Int("ciphertext", len(sealed)))
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "the token returned a malformed ciphertext")
	}
	wrapped := append(append(make([]byte, 0, ivSize+len(sealed)), used...), sealed...)
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Wrap{Wrap: &pkcs11pb.WrapResponse{Wrapped: wrapped}}}
}

// unwrap decrypts a data key that wrap produced.
func (s *Server) unwrap(ctx context.Context, req *pkcs11pb.UnwrapRequest) *pkcs11pb.Response {
	wrapped := req.GetWrapped()
	if len(wrapped) <= ivSize+tagSize {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, "the wrapped key is too short")
	}
	sel, err := ParseKeyRef(req.GetKeyRef())
	if err != nil {
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INVALID_REQUEST, err.Error())
	}
	plaintext, err := s.token.Decrypt(sel, wrapped[:ivSize], wrapped[ivSize:])
	if err != nil {
		return s.tokenFailure(ctx, "unwrap", err)
	}
	if len(plaintext) != len(wrapped)-ivSize-tagSize {
		s.log.ErrorContext(ctx, "pkcs11 helper: the token returned a data key of the wrong length", slog.Int("length", len(plaintext)))
		return failure(pkcs11pb.ErrorCode_ERROR_CODE_INTERNAL, "the token returned a malformed data key")
	}
	return &pkcs11pb.Response{Response: &pkcs11pb.Response_Unwrap{Unwrap: &pkcs11pb.UnwrapResponse{Plaintext: plaintext}}}
}
