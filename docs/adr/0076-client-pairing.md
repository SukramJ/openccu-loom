# ADR 0076 — Client token pairing mirrors the occulited protocol

- **Status**: accepted (2026-09-29)
- **Related**: [ADR 0075 — Token credentials](./0075-token-credentials.md),
  `internal/pairing`, `internal/client/transport/occulited/pairing.go`

## Context

Since 0.79.0 this daemon *asks* an openccu-lite box for its API token by
code pairing: no secret is copied by hand, both sides derive a six-digit
code, and the box's administrator approves by typing it. External
clients of this daemon — openccu-loom-client above all, and through it
the Home Assistant integration — still start from a manually created
bearer token: create it in the tokens panel, copy it, paste it into the
client.

## Decision

The daemon grows the *answering* side of the same protocol, so both
pairings feel identical:

1. **Wire contract** (`/api/v1/pairing`, unauthenticated, ADR-numbered
   here): the client sends `commit = SHA-256(client_nonce)` and gets
   `{id, poll, nonce, expires_in, interval, fingerprint}` back. Both
   sides derive `code = six digits of SHA-256(nonce ‖ client_nonce ‖
   fingerprint)`; the client reveals its nonce with the first poll
   (commit-reveal, so an interceptor cannot search for a certificate
   whose code collides). Polls authenticate with
   `Authorization: Pairing <poll>`; the approved answer carries the
   token exactly once. The derivation function is shared with the
   asking side (`internal/pairing.Code`).
2. **The typed code is the approval.** The admin card lists revealed
   requests; the administrator types the six digits read off the asking
   client. A wrong code rejects the request and mutes its
   (address, app) pair — a mismatch means the code compared belongs to
   a different request than the one asking.
3. **Roles instead of scope areas.** loom's token model is role-based,
   so the ask names `viewer` or `operator`. `admin` is never pairable —
   the same line occulited draws around `auth:admin`: a credential with
   administrative reach is created deliberately in the tokens panel,
   not approved from a network request.
4. **Limits are part of the protocol**: five-minute lifetime, five
   pending at most, ten per hour per address, one per (address, app),
   ten-minute mute after a rejection, local networks only (by
   RemoteAddr, deliberately not X-Forwarded-For — the same policy as
   the login rate limiter; behind a reverse proxy, the proxy gates
   pairing exactly as it gates login).
5. **Switchable**: `north.rest.auth.pairing.enabled` (default on,
   restart-required). Off removes the unauthenticated routes' function
   entirely; nothing else about tokens changes.

## Consequences

- A paired token is an ordinary token: same store, same audit trail
  (`token_create` with a `paired` note plus a `pairing_approve` /
  `pairing_reject` entry), same revocation.
- Requests live in memory only. A restart voids unapproved requests,
  which is what an operator expects of an unapproved credential ask.
- The pending list reaches operators through the admin card (live via
  the `pairing.requests_changed` broadcast) and as a `pairing:pending`
  operator warning.
- openccu-loom-client implements the asking side against this contract;
  the Home Assistant config flow can then offer "pair with the daemon"
  instead of a token paste.

## Alternatives considered

- **Reusing occulited's scope areas** would have invented a scope model
  loom does not have; roles are what its tokens carry.
- **Approval by click without typing the code** drops the
  man-in-the-middle binding — the typed code is what proves the
  administrator saw the *asking client's* screen, not just a row in a
  list.
- **Persisting pending requests** buys nothing: a request outliving a
  daemon restart is a credential ask nobody is looking at.
