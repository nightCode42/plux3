// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

// Package api is the ConnectRPC edge: handlers that authenticate,
// authorise, translate and delegate, and the interceptors that do the
// cross-cutting work (L-1, ADR-0005).
//
// Handlers hold no business logic. Domain packages return plxerr errors
// with registered reasons; one interceptor here turns them into Connect
// errors with google.rpc.ErrorInfo and the Plux code (SRV-006). No
// domain package imports connectrpc (L-2).
package api

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/nightCode42/plux3/backend/internal/plxerr"
)

// Domain is the error domain of every ErrorInfo this server returns.
const Domain = "plux.dev"

// codes maps each registered reason to the Connect code that carries it.
// It is a table rather than a chain of type switches, so that adding a
// failure mode is one line and an unmapped one is visible.
var codes = map[plxerr.Code]connect.Code{
	// The caller sent something the server cannot accept.
	plxerr.UnknownProperty:          connect.CodeInvalidArgument,
	plxerr.MissingProperty:          connect.CodeInvalidArgument,
	plxerr.WrongJSONType:            connect.CodeInvalidArgument,
	plxerr.InvalidEnumValue:         connect.CodeInvalidArgument,
	plxerr.InvalidFormat:            connect.CodeInvalidArgument,
	plxerr.OutOfRange:               connect.CodeInvalidArgument,
	plxerr.InvalidStructure:         connect.CodeInvalidArgument,
	plxerr.InvalidJSON:              connect.CodeInvalidArgument,
	plxerr.UnsupportedSchemaVersion: connect.CodeInvalidArgument,
	plxerr.InvalidProjectLayout:     connect.CodeInvalidArgument,
	plxerr.InvalidPageToken:         connect.CodeInvalidArgument,
	plxerr.RequestTooLarge:          connect.CodeInvalidArgument,

	// The caller's view of the world is out of date.
	plxerr.RevisionConflict:    connect.CodeAborted,
	plxerr.IdempotencyConflict: connect.CodeAlreadyExists,

	// The caller may not do this, or may not know that it exists.
	plxerr.AuthenticationRequired: connect.CodeUnauthenticated,
	plxerr.MultiFactorRequired:    connect.CodePermissionDenied,
	plxerr.PermissionDenied:       connect.CodePermissionDenied,
	plxerr.ResourceNotFound:       connect.CodeNotFound,
	plxerr.ResourceExists:         connect.CodeAlreadyExists,
	plxerr.PreconditionFailed:     connect.CodeFailedPrecondition,
	plxerr.EditingLockHeld:        connect.CodeFailedPrecondition,

	// The caller must slow down or ask for less.
	plxerr.RateLimited:      connect.CodeResourceExhausted,
	plxerr.LimitExceeded:    connect.CodeResourceExhausted,
	plxerr.LimitApproaching: connect.CodeResourceExhausted,

	// The server refused to reach somewhere.
	plxerr.OutboundRequestBlocked: connect.CodePermissionDenied,
	plxerr.AssetRejected:          connect.CodeInvalidArgument,

	// The server failed.
	plxerr.InternalServerError: connect.CodeInternal,
	plxerr.UpstreamUnavailable: connect.CodeUnavailable,
}

// ConnectCode returns the Connect code a Plux code is reported with.
// An unmapped code is internal: a failure the edge does not know how to
// describe is not described at all.
func ConnectCode(c plxerr.Code) connect.Code {
	if code, ok := codes[c]; ok {
		return code
	}
	return connect.CodeInternal
}

// Error translates a domain error into a Connect error carrying the
// Connect code, google.rpc.ErrorInfo and the Plux code (SRV-006).
//
// incident identifies an unrecognised failure in the server's logs; the
// response carries the identifier and nothing else about it, so an
// internal message can never leak.
func Error(err error, incident string) error {
	if err == nil {
		return nil
	}
	var connErr *connect.Error
	if errors.As(err, &connErr) {
		return connErr
	}
	var plx *plxerr.Error
	if !errors.As(err, &plx) {
		return internal(incident)
	}
	code := ConnectCode(plx.Code)
	if code == connect.CodeInternal {
		return internal(incident)
	}
	out := connect.NewError(code, fmt.Errorf("%s: %s", plx.Code, plx.Message))
	if detail := info(plx.Code, plx.Reason(), plx.Details); detail != nil {
		out.AddDetail(detail)
	}
	return out
}

// internal returns the one error an unrecognised failure becomes.
func internal(incident string) error {
	out := connect.NewError(connect.CodeInternal,
		fmt.Errorf("%s: internal error, incident %s", plxerr.InternalServerError, incident))
	if detail := info(plxerr.InternalServerError, "INTERNAL_SERVER_ERROR", map[string]string{"incident": incident}); detail != nil {
		out.AddDetail(detail)
	}
	return out
}

// info builds the ErrorInfo detail. A detail that cannot be encoded is
// dropped rather than replacing the error with one about encoding.
func info(code plxerr.Code, reason plxerr.Reason, details map[string]string) *connect.ErrorDetail {
	metadata := map[string]string{"code": code.String(), "docURL": code.DocURL()}
	for k, v := range details {
		metadata[k] = v
	}
	detail, err := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason:   string(reason),
		Domain:   Domain,
		Metadata: metadata,
	})
	if err != nil {
		return nil
	}
	return detail
}
