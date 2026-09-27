# ADR 0075 — Credentials for token-based systems

- Status: accepted
- Date: 2026-09-27

## Context

A CCU authenticates the daemon with a username and a password (or none),
stored per central, sealed at rest when a master key exists, masked as
`***` on every read and never round-tripped (CLAUDE.md, "never round-trip
the secret mask").

openccu-lite authenticates every API call with a **bearer token** carrying
**scopes** (`rpc:read` … `rpc:admin`, `meta:write`, `system:write`, `backup`,
`power`, …). A token is created on the box, or through **client pairing**: an
app asks, the box's administrator approves by entering a six-digit code the
app shows, and the app receives the token exactly once. The box serves HTTPS
with a certificate no public authority signed. Pairing never grants the
`backup`, `power`, `radio:keys`, `addons:write`, `led` or `auth:admin` scopes.

## Decision

- **Configuration.** A central carries `api_token` — a `cfg:"secret"` field,
  sealed at rest in the same column cipher as the CCU password, masked on
  every read, restored from the stored value when a write sends the mask;
  a central stored through the centrals API may name an environment
  variable instead (`api_token_env`) — and
  `tls_fingerprint`, the lower-case hex SHA-256 of the box's leaf certificate.
  A box's central carries no username, password, port overrides or CUxD; a
  CCU's carries no token or fingerprint. Every write path enforces the rules.
- **TLS pinning.** With a fingerprint set, a TLS handshake succeeds exactly
  when the leaf certificate's SHA-256 equals it; the chain is not checked.
  Without one, an unknown authority is refused, and the refusal carries the
  presented fingerprint so a first contact can show it.
- **Probe.** `POST /centrals/probe` identifies what answers at an address.
  Over HTTPS behind an unknown authority it answers with the presented
  fingerprint, for the operator to compare with the box before pinning it.
- **Pairing, with the token kept on the server.** `POST /centrals/pairing`
  starts a pairing (full, control-only or read-only access) and returns only
  the pairing id and the code; `GET /centrals/pairing/{id}` long-polls the
  box. The approved token stays in the daemon's pairing session. A central
  created or updated with `pairing_id` takes it on the server and the session
  forgets it; **no answer a client receives carries the token**
  (`TestPairingTokenNeverLeavesTheDaemon`). The code binds the certificate
  fingerprint both sides saw, and the client aborts when the box reports a
  different certificate than the one it connected to.
- **First run.** The same three operations exist session-less under
  `/setup/*`, open only while first-run setup is required and allowed.
- **Manual tokens** remain for everything pairing cannot grant: a token
  created on the box with the `backup` or `power` scope is pasted into the
  form. The feature set (ADR 0074) names which features a missing scope
  would unlock.

## Alternatives considered

- **Deliver the token to the browser and let it save the central.** The
  token would pass through the SPA, its logs and its storage, and every XSS
  would reach it. Rejected: the server keeps it.
- **Accept any certificate (`tls_insecure_skip_verify`).** Leaves the token
  open to interception on the first request; pinning costs one comparison
  by the operator.
- **Trust on first use without showing the fingerprint.** Silent, and
  exactly the moment an interception would be invisible.
- **Store the token unsealed like a plain config field.** The token is a
  standing credential to the whole box; it is sealed like the CCU password.

## Consequences

- An operator pairs from the setup wizard or the CCU form in a few steps and
  never sees or copies a token; one pairing yields one token per daemon, so a
  daemon does not share the box's two-streams-per-token budget with another
  client.
- Backup and power features need a manually created token; the UI says which
  scope is missing.
- A pairing session lives in memory for ten minutes; a daemon restart during
  pairing loses it, and the operator pairs again.
