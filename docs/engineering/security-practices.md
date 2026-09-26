# Security Practices

Secure-coding rules that apply to every component. The security requirements themselves are in spec §15; the threat model arrives with P6 (`SEC-000`). Report vulnerabilities as described in [SECURITY.md](../../SECURITY.md).

---

## 1. Secrets and sensitive data

- Never commit secrets, keys, certificates, keystores, `google-services.json`, `.env` files or credentials. gitleaks runs in pre-commit and on the full history in CI.
- Never log, trace, label or return keys, tokens, DPoP proofs, signatures, secrets or fields tagged `sensitive` — not even in debug builds (`ACT-031`, `SEC-092`).
- Secrets are read from files or a secret manager, never from command-line arguments.

## 2. Keys and cryptography

- Private keys are used only through the signing abstraction (`SEC-120`), in the `worker` role; device keys never leave secure hardware (`SEC-001`).
- Use standard, reviewed primitives from the standard library or allowlisted dependencies. Never invent cryptography or protocols.
- Algorithms are named explicitly and validated against configuration, never taken from untrusted input.

## 3. Untrusted input

- Every input is untrusted until validated: API requests, documents, bundles, manifests, deltas, device telemetry, AI output, uploaded assets.
- Validate size and structure before parsing deeper (`SEC-104`); bound every loop and allocation by the limits registry (`LIM-001`).
- Server-side fetches of user-supplied URLs go through the SSRF-safe client (`SEC-105`).
- Parsers of untrusted formats are fuzzed (`QA-004`).

## 4. Defaults

- Secure by default. An unsafe setting is never a default, is available only in development environments where the spec allows it, and produces a distinct warning.
- Deny by default: permissions, capabilities, network domains and CORS origins are granted explicitly.

## 5. Supply chain

- Dependencies come only from the allowlist ([dependencies.md](dependencies.md)); updates arrive through Dependabot with a cooldown.
- GitHub Actions are pinned to full commit SHAs; workflows use least-privilege permissions and do not persist credentials; `actionlint` and `zizmor` check every workflow.
- Release artifacts are reproducible (`CI-006`) and, from P2, signed with SLSA provenance (`CI-004`).

## 6. Review

- Changes to authentication, authorisation, signing, key handling, DPoP, attestation, sandboxing or security defaults require the maintainer's review (`AGENTS.md` §7, `CODEOWNERS`).
- A security fix includes a regression test that fails without the fix.
