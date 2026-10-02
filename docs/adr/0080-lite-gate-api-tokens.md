# ADR 0080 — Box API tokens through the lite gate

- **Status**: accepted (2026-10-02)
- **Related**: [ADR 0079 — box-shell single sign-on](./0079-lite-shell-session-sso.md),
  [ADR 0077 — add-on token onboarding](./0077-lite-addon-token-onboarding.md),
  [ADR 0078 — lite webserver ingress](./0078-lite-webserver-ingress.md),
  [ADR 0076 — client pairing](./0076-client-pairing.md),
  [openccu-lite issue #3 (the gate change this builds on)](https://github.com/hobbyquaker/openccu-lite/issues/3),
  [occulited `system-api.md`, *The gate and API tokens*](https://github.com/hobbyquaker/occulited/blob/master/docs/system-api.md)

## Context

Programs that reach the daemon through the openccu-lite ingress (the Home
Assistant integration above all, which uses it so the daemon's own port can
stay closed) had only one way past the box's gate: a box session, opened
with the user name and password of a box account that the program then had
to store.

Since openccu-lite 1.0.0-dev.36 the gate in front of `/addons/<id>/` also
accepts a **box API token** as `Authorization: Bearer <token>`, on every
request, when the token holds the scope `addon:<id>` of the add-on behind the
path, or Full access (`*`). A program obtains such a token through the box's
client pairing (`POST /api/auth/v1/pairing/request` with
`"addons": ["<id>"]`, confirmed by the box administrator) or from the box's
token page. For a request it accepted with a token, the gate hands the
add-on the token itself in `X-Occulite-Session`, plus `X-Occulite-Auth:
token` and the token's name in `X-Occulite-Token`; client-sent copies of
all three are stripped. The box's open `GET /api/auth/v1/state` answers for
a token with `authenticated`, `user: "token:<name>"` and the token's stored
scopes — and no role.

ADR 0079's resolver ignores such a request: it accepts only the 26-character
box session id and requires a role. The program would therefore still need
a daemon credential of its own — but `Authorization`, where the daemon reads
one, is the header the gate now consumes.

## Decision

- **A box token the gate accepted is a daemon identity, by verification,
  never by trust** — ADR 0079's resolver, extended. When
  `X-Occulite-Session` has the shape of a box token (`olt_` and 32
  lower-case hex digits), the resolver asks the local box about it on the
  open state route, exactly as it asks about a session, with the same
  cache, the same refusal of auth-off and public-principal answers, and the
  same 60-second WebSocket re-verification. No second daemon credential is
  needed: one pairing, at the box, yields one secret.
- **The daemon re-checks the scope itself.** The gate already refused a
  token without the add-on's scope, but the daemon's port is reachable
  without the gate, where the header is client-controlled. A token that
  opens a different add-on's pages, or only the box APIs, must not open
  this daemon, so the resolver demands the add-on's own scope (or Full
  access) in the box's answer.
- **The add-on's id is read from the box, not assumed.** occulited writes
  the add-on's own minted token to `/run/occulite/addon-tokens/<id>.api`;
  the daemon already reads that file (ADR 0077) and derives its gate scope
  `addon:<id>` from the file's name. Without that file there is no scope
  and the token branch stays off — tokens are only accepted where a box
  exists to verify them.
- **Fixed role mapping from the scopes.** Full access maps to admin, the
  add-on's own scope to operator; the token's other scopes count for
  nothing, as at the gate. A token carries no role on the box, and a pairing
  confirmed for "this add-on's pages" is not a grant of the daemon's
  administration.
- **A scheme of its own: `occulite-token`.** The subject is
  `occulite-token:<token name>`, distinct from a box user of the same name.
  Like `occulite` it is federated (no local-account controls apply) and
  carries no expiry (the box owns the token's lifetime). Unlike `occulite`
  it is exempt from the double-submit CSRF check: the gate takes a token
  only from the request's own `Authorization` header, never from a URL or
  a cookie, so nothing in a browser attaches it by itself — the reasoning
  that exempts the daemon's own bearer tokens.
- **The answer must match the credential's kind.** A token-shaped answer
  for a session id, or a session-shaped answer for a token, vouches for
  nothing; one predicate (`OcculiteSession.Grant`) decides for both the
  request resolver and the socket re-verification.

## Alternatives considered

- **Two credentials: the box token for the gate, a daemon token in a
  second header.** Two pairings in every client's setup and two secrets to
  rotate, and a daemon-side header that exists only to route around the
  gate. The box can answer who the token is, so it is asked.
- **Trusting `X-Occulite-Auth: token` and `X-Occulite-Token` without asking
  the box.** Trustworthy only behind the gate; on the directly reachable
  port both are client-controlled. Every claim is verified, as in ADR 0079.
- **Mapping every accepted token to admin.** Simpler, but it would make a
  token paired for an add-on's pages the daemon's administrator; Full
  access remains the explicit way to grant that.
- **A configurable role for box tokens.** A knob without a use case yet;
  the fixed mapping can be extended when one appears.

## Consequences

- `GET /auth/me` can report the new scheme — an additive vocabulary change
  (APIVersion 13.3.0).
- If the gate passes the `Authorization` header on, the daemon's own bearer
  resolution misses on the box token and defers, at the cost of one failed
  token lookup per request; no failed-bearer lockout exists that the
  gate's loopback address could trip.
- The contract test drives the resolver against godevccu's litefake, whose
  state route already answers tokens in the box's documented form.
- Clients (the Python client, the Home Assistant integration) pair with the
  box for an `addon:openccu-loom` token instead of storing a box account's
  password.
