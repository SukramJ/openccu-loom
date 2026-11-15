# ADR 0091 — The remote add-on pairs with an openccu-lite box

- **Status**: accepted (2026-11-15)
- **Related**: [ADR 0054 — remote ingress proxy add-on](./0054-remote-ingress-proxy-addon.md),
  [ADR 0076 — client token pairing](./0076-client-pairing.md),
  [ADR 0078 — the box's web server serves the Config UI](./0078-lite-webserver-ingress.md),
  [ADR 0080 — box API tokens through the lite gate](./0080-lite-gate-api-tokens.md)

## Context

OpenCCU-Loom Remote (ADR 0054) proxies a remote daemon into a Home
Assistant Ingress panel. On an openccu-lite box the daemon runs as an
add-on and is usually reached through the box's own web server at
`https://<box>/addons/loom` (ADR 0078), with no separate daemon port
(8119) open. The box's lighttpd gate stands in front of that path: without
a credential it sends a browser navigation to `/login?return=…` and
answers every other request with 401. It admits an
`Authorization: Bearer olt_…` token whose scopes cover the add-on's path
segment, answers 401 for an unknown or expired token and 403 for one that
does not open the add-on.

Before this decision the remote add-on could only be pointed at such an
instance with a box token minted by hand and copied into the add-on
options. Without one the panel showed the gate's redirect, which the proxy
rebased into the panel, and which led back into the gate: a login loop.

The box already offers programs a pairing protocol (the one the daemon
speaks as a client and mirrors as a server, ADR 0076). An ask that names
only an add-on (`addons: ["openccu-loom"]`) yields a token with the scope
`addon:openccu-loom`, which the gate accepts for the add-on's pages and
which the daemon maps to the operator role (ADR 0080). Full access is
never pairable.

## Decision

- **The panel pairs with the box.** The proxy reserves
  `/-/pair/<instance>` in both modes, answered before anything is
  proxied. `POST` asks the box (`POST /api/auth/v1/pairing/request` at the
  box root, whatever path the instance URL names) for the add-on scope and
  shows the six-digit code; the proxy long-polls the box in the background
  and stores the token once the box's administrator approves. `GET`
  reports the state (JSON) or serves a pairing page (HTML); `DELETE`
  withdraws a pending ask and forgets the paired token. The token, the
  poll secret and the client nonce never reach the browser.
- **The code binds the certificate.** The proxy commits to a secret
  client nonce, checks that the fingerprint the box reports is the
  certificate the request saw, and derives the code from the box's nonce,
  its own nonce and that fingerprint — the formula in
  `internal/pairingcode`, shared with the daemon's client and server. An
  interceptor's certificate yields a code the box refuses, so the first
  request may skip chain verification; the polls are pinned to the agreed
  certificate.
- **The paired certificate is pinned.** While the paired token is the
  credential in use, the upstream transport accepts exactly the
  certificate the pairing was bound to, in place of chain verification and
  of `tls_insecure`. A changed box certificate therefore needs a new
  pairing.
- **The state is the add-on's own.** Paired tokens live in
  `/data/paired.json` (`--state`), mode 0600, written through a temp file
  and a rename, keyed by instance name and bound to the normalised
  instance URL: a renamed instance or a changed URL drops the entry at the
  next start.
- **The options token wins.** A non-empty `token` in the add-on options is
  always the injected credential; pairing is not offered for that
  instance. Pairing yields the operator role; administration needs a
  Full-access box token, minted on the box and put into the options by
  hand.
- **Refusals are told apart.** The status poller, which now runs in both
  modes, reports `unauthorized` for a 401 or 403. Only a 401 drops a
  paired token (the box no longer knows it); a 403 keeps it, because the
  token is valid and a new pairing alone would not change what it opens.
- **The login wall is explained, not looped.** A 401/403 to a browser
  navigation, or a redirect to the box's `/login` outside the instance
  path, is replaced by a page that offers the pairing and names the manual
  Full-access token. When the box refused pairing as `not-local` (Home
  Assistant outside the box's local networks), the page explains the
  manual token only.
- **Cross-site calls are refused.** The mutating calls require a JSON
  content type and an `Origin` whose host equals `X-Forwarded-Host` (as
  Home Assistant's ingress sets it) or else `Host`.

## Is this the best architecture?

**Better than the comparison targets.** Home Assistant's own add-on
panels and the CCU's WebUI know no way to bring a remote, gate-protected
UI into a panel without a hand-copied secret. The aiohomematic family has
no remote panel at all. Pairing with a code the box's administrator
approves, bound to the certificate, is the box's own trust ceremony,
used as designed instead of bypassed.

**Better than before.** An operator no longer mints and copies a box
token to see the daemon in Home Assistant, and the login loop is gone.
The manual token keeps its place for the case pairing deliberately does
not cover (admin rights, a Home Assistant outside the box's networks), and
the panel says which case applies.

**The right cut.** The box owns the protocol, the approval and the
token's scope; the daemon owns the role mapping (ADR 0080) and needs no
change. The remote add-on owns only its side: the ask, the wait, the
stored token and the pin. It speaks the protocol with a small stdlib
client of its own, because the daemon's client pulls the daemon's auth
stack into a binary that must stay light; the one piece both need to
agree on byte for byte, the code formula, moved into a stdlib-only leaf
package both import.

## Consequences

- A box certificate renewal breaks a paired instance until it is paired
  again; the panel then shows the instance as unreachable.
- The status poller probes a single instance too, every 15 s, so a
  revoked paired token is dropped without an overview.
- The add-on's backup carries the paired tokens along with its options.
