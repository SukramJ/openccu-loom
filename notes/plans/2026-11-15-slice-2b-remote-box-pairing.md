# Slice 2b — the remote add-on reaches loom through the box ingress (box pairing)

Owner requirement 2026-11-15: OpenCCU-Loom Remote must reach a loom on an
openccu-lite box when no separate loom port (8119) is open, i.e. through
`https://<box>/addons/loom` (ADR 0078). Fable reads of 2026-11-15, spot-checked.

## Facts (read)

- The proxy already handles a base-path target: `internal/remoteproxy/options.go:112-129`,
  `proxy.go:132,144-148,193-245,315-336`; poller joins the base `poller.go:158`.
- The box gate blocks every `/addons/` request without a credential: 302 `/login?return=`
  for HTML, else 401 (`../occulited/deploy/lighttpd/occulite-gate.lua:430-446`). It admits
  `Authorization: Bearer olt_…` when the token's segments cover the path segment
  (`:296-325`); strips only `X-Occulite-*`, `X-Forwarded-*`, `Forwarded` (`:85-91`).
- A token with scope `addon:openccu-loom` opens segment `loom`: segments come from the
  add-on's lighttpd drop-in (`../occulited/internal/system/addoningress.go:17-41`,
  `internal/auth/auth.go:1755-1768`).
- Pairing (`../occulited/internal/httpapi/pairing.go:28-30`): `POST /api/auth/v1/pairing/request`
  (202: id, poll secret, nonce, fingerprint), `GET …/request/{id}?client_nonce=&wait=≤30`
  with `Authorization: Pairing <poll>` (token delivered once), `DELETE` withdraws. An
  addons-only ask is valid (`internal/pairing/pairing.go:424-460`); Full access is never
  pairable (`:83-101`). Refusals: `pairing-off`/`not-local` 403, limits 429
  (`httpapi/pairing.go:60-70`); locality = the box's own networks.
- Loom maps a box token to operator (scoped) / admin (Full) (ADR 0080 `:39-63`);
  X-Ingress-Path honoured without peer restriction (`internal/north/rest/router.go:1566-1580`).
- Loom's pairing client (`internal/client/transport/occulited/pairing.go`) pulls
  `internal/auth` + bcrypt — not importable by the remote binary.

## Owner decisions (2026-11-15)

- Automatic pairing from the remote add-on; ships in 0.105.0 as its own PR after Slice 2.
- Pairing yields operator (`addon:openccu-loom`); admin stays a manually minted Full
  token in the options. A non-empty options `token` wins over a paired one.
- `not-local` (HA in another subnet / VPN): explain, point to a manual token.
- Reserved path `/-/pair/<name>` in both modes, checked before proxying.
- Docs name both roles.

## Shape (Fable plan)

- `internal/remoteproxy/pairing.go`: small stdlib client (request, long-poll, withdraw;
  bind nonce + client nonce + fingerprint, abort on mismatch); the pairing code derivation
  moved to a leaf package both sides import (today `internal/pairing/code.go:27`, whose
  package imports auth).
- `internal/remoteproxy/pairstate.go`: `/data/paired.json` 0600, atomic rename, keyed by
  instance name + normalised URL (URL change invalidates); `--state` flag (`main.go`).
  `inst.Token` (`proxy.go:149`, `poller.go:163`) becomes a mutex-guarded accessor; paired
  fingerprint pinned in the transport (`proxy.go:98-104`).
- `server.go`: `/-/pair/{name}` POST (code + expiry) / GET (state) / DELETE; server
  long-polls in a goroutine; token never reaches the browser; POST only, JSON content
  type, `Origin` vs `X-Forwarded-Host`.
- `overview.go`: sibling "Pair with box" button (card is an `<a>`), de/en.
- `poller.go`: status `unauthorized` for 401/403; re-pair offer when paired; drop a stored
  token only on 401, never 403.
- `proxy.go` `modifyResponse`: 401/403 on HTML navigation, or 302 to `/login?return=`
  outside the instance path → explanatory page (pair / manual token) instead of the loop.
- Tests through `remoteproxy.New` with an httptest TLS gate-fake implementing pairing +
  Bearer gate (godevccu litefake pairing lacks `addons`).
- ADR 0091 (better-than / right-split section) + amendment note in ADR 0054; DOCS.md,
  config.yaml comment, CHANGELOG.

Possible godevccu issue: `addons` in litefake pairing.
