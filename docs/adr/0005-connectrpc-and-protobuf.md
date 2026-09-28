# 0005. ConnectRPC and Protocol Buffers for all APIs

- **Status:** Accepted
- **Date:** 2026-09-27
- **Requirements:** `SRV-000`, `SRV-002`, `SRV-003`, `SRV-004`, `SRV-005`, `SRV-006`, `CI-001`, `CI-003`

## Context and problem

Four kinds of client talk to the Plux Server: Studio in a browser, the `plux` CLI, devices running `plux_flutter`, and customers' CI pipelines. They need one contract that is versioned, checked for breaking changes (`SRV-000`), generated into Go, TypeScript and Dart, and usable from a browser without a proxy. Devices are on slow, lossy mobile networks, so the wire format has to be compact and the transport has to work over plain HTTP/1.1 where middleboxes forbid HTTP/2.

## Decision drivers

- One handler serving gRPC, gRPC-Web and plain HTTP/JSON, so no client needs a special gateway (`SRV-002`).
- Browser support without Envoy or a sidecar: Studio is a static SPA plus a BFF, nothing more (L-7).
- Generated clients in Go, TypeScript and Dart from the same `.proto` files.
- Mechanical breaking-change detection against the last tagged release (`SRV-000`).
- An error model that survives all three protocols and carries Plux error codes (`SRV-006`, ADR-0018).
- Small dependency surface and no code generation that needs a Node toolchain in the Go job.

## Considered options

1. **ConnectRPC with Protocol Buffers, managed with `buf`.**
2. gRPC with `grpc-go` plus grpc-gateway and Envoy for gRPC-Web.
3. A hand-written REST API described by OpenAPI, with generated clients.
4. GraphQL.

## Decision

Chosen option: **1**, as the specification requires (`SRV-002`).

### The contract

All services live in `proto/plux/v1/`, one file per service plus `common.proto` for shared
messages. `buf lint` enforces the style guide and `buf breaking` compares each pull request
with the last tagged release; a breaking change requires a `v2` package and the deprecation
period of `SRV-000`. Generation runs inside `make gen`, is committed, and `make gen-check`
fails when it is stale (`CI-003`).

| Output | Tool | Location |
|---|---|---|
| Go messages | `protoc-gen-go` | `backend/internal/api/pluxv1/` |
| Go handlers and clients | `protoc-gen-connect-go` | `backend/internal/api/pluxv1/pluxv1connect/` |
| OpenAPI 3.1 | `protoc-gen-connect-openapi` | `docs/reference/api/openapi.yaml` |
| TypeScript client | `protoc-gen-es`, `protoc-gen-connect-es` | `studio/packages/api-client/` (from P11) |
| Dart client | `protoc-gen-dart` | `packages/plux_flutter/lib/src/api/` (from P3) |

Only the Go and OpenAPI outputs are generated in P2; the others are wired in when their
phase needs them, from the same contract, so no second contract can drift.

### Transport

One `connect.Handler` per service serves the Connect protocol, gRPC and gRPC-Web over both
HTTP/1.1 and HTTP/2. Studio and the CLI use Connect over HTTP/1.1 with binary Protocol
Buffers; the same endpoints accept `application/json` for integrators who prefer plain
HTTP, which is what the generated OpenAPI description documents. Devices use Connect with
binary messages; the manifest and artifact endpoints stay plain HTTP so CDNs, conditional
requests (`REL-031`) and range requests (`REL-024`) work unchanged.

### Cross-cutting concerns as interceptors

Authentication, authorisation, idempotency (`SRV-005`), rate limiting (`SRV-065`), request
size limits (`SEC-104`), tracing and metrics (`OBS-001`, `OBS-002`) and error translation
(`SRV-006`) are Connect interceptors, in that order. Handlers themselves stay thin (L-1):
they read the authenticated principal from the context, call one domain method and return.

### Errors

Domain code returns `plxerr.Error` values carrying a registered reason and a Plux code
(ADR-0018). A single interceptor at the edge translates each into a `connect.Error` with a
Connect code, `google.rpc.ErrorInfo` (`reason`, domain `plux.dev`, metadata) and the Plux
code, and nothing else: no domain module knows about transport (L-2). Untranslated errors
become `internal` with a generated incident ID that is logged but never returned.

### Lists and partial reads

Every list endpoint takes `page_size`, `page_token`, `filter`, `order_by` and a
`google.protobuf.FieldMask` (`SRV-004`). Page tokens are opaque and integrity-protected:
the cursor is serialised, bound to the request's filter and ordering, and authenticated
with HMAC under a server key, so a client cannot forge one or carry it to another query.

## Consequences

- **Positive:** one contract, three protocols, no proxy; browser calls work directly; breaking changes are caught mechanically; the same messages type Studio, the CLI, devices and the OpenAPI description.
- **Negative:** three new dependencies (`connectrpc.com/connect`, `google.golang.org/protobuf` and the `buf` toolchain); protobuf's JSON mapping is not the document model's canonical JSON, so documents travel as bytes, not as protobuf structures.
- **Follow-up:** the TypeScript client is generated in P11 and the Dart client in P3; `buf breaking` has no baseline until the first `backend` tag, where it starts comparing.

## Options in detail

### Option 2 — gRPC with grpc-gateway and Envoy

gRPC is the most widely deployed option and has the richest tooling, but browsers cannot
speak it: Studio would need Envoy or grpc-web-proxy in front of the API, and self-hosters
would have to run and upgrade it. grpc-gateway adds a second, hand-annotated REST surface
that drifts from the gRPC one. Rejected for the operational cost it pushes onto every
self-hosted installation (`DEP-002` promises one command).

### Option 3 — REST described by OpenAPI

Familiar to every integrator and trivially debuggable, but the contract becomes prose:
breaking-change detection is weaker, streaming (needed for publish progress, `SRV-050`) has
no standard shape, and client generation across Go, TypeScript and Dart is uneven. Connect
gives the same plain HTTP/JSON surface as a property of the protobuf contract, so this
option's only real advantage is kept without its costs.

### Option 4 — GraphQL

Good for Studio's read-heavy screens, but it offers no help for device sync, streaming
publish jobs or CLI automation, and its resolver model makes per-call authorisation
(`SEC-102`) and bounded responses (`SRV-004`) harder to prove. Rejected.
