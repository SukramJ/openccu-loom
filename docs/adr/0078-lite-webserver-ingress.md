# ADR 0078 — The box's web server serves the Config UI (lite ingress)

- **Status**: accepted (2026-09-30)
- **Related**: [ADR 0044 — single port + HA-Ingress](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0054 — remote ingress proxy](./0054-remote-ingress-proxy-addon.md),
  [ADR 0077 — add-on token onboarding](./0077-lite-addon-token-onboarding.md),
  `packaging/ccu-addon/ccu/addon/etc/lighttpd.conf`

## Context

An openccu-lite box terminates TLS at its own web server with the
certificate the operator configured there (manual or ACME), and offers
add-ons a validated way in: an add-on ships an `etc/lighttpd.conf`
fragment in its own directory, occulited checks it against an allowlist
(`proxy.server` to this host only, `proxy.header`, `proxy.forwarded`,
`url.rewrite*`/`url.redirect`; never `include`, `ssl.*`, sockets outside
the add-on's tree) and installs a root-owned copy before every web-server
start. A proxied frontend appears in the box shell's navigation by
itself, is framed as a kept page, and receives the gate's
`X-Occulite-Session` header — with a client-sent header of that name
stripped, so the value is trustworthy.

Until now the add-on was reachable only on its own port 8119: plain HTTP
unless the operator wires a certificate, a firewall switch to open, and
a second origin next to the box UI.

## Decision

- **Ship the fragment.** The package carries
  `addon/etc/lighttpd.conf`; `update_script` copies it into the add-on's
  directory, occulited does the rest. It proxies `/addons/loom/` to
  `127.0.0.1:8119`, strips the prefix (`map-urlpath` — the daemon mounts
  at the root and its SPA derives the base path from the page URL, the
  same mechanism HA-Ingress uses), keeps the WebSocket upgrade enabled
  for the event stream, and redirects the bare `/addons/loom` to
  `/addons/loom/app/` so the shell's navigation entry lands in the UI
  without the daemon ever needing to know the prefix. The path segment
  is `loom`, deliberately distinct from occulited's own
  `/addons/openccu-loom/` CGI alias that serves the settings card.
- **TLS and the firewall come for free on that path**: the box
  terminates HTTPS with the operator's certificate, and port 8119 can
  stay closed in the firewall for everyone who only uses the ingress.
- **Certificate reuse on the direct port stays opt-in.** Both systems
  keep their web-server certificate in one combined file,
  `/etc/config/server.pem`; a confined add-on on openccu-lite may read
  it (the `certs` group), and on a CCU the add-on runs as root. An
  operator who wants HTTPS on 8119 sets `north.rest.tls_cert_file` and
  `tls_key_file` **both** to that file — the pair loader accepts a
  combined PEM, the reloader picks up rotations on the next handshake,
  and on openccu-lite an ACME renewal restarts the certs-group add-ons
  anyway. No automatic switch: existing `http://…:8119` clients (a
  Home Assistant backend above all) must not break on an add-on update.
- **Authentication stays the daemon's own** inside the shell frame (the
  established pattern for proxied frontends). Mapping the gate's
  `X-Occulite-Session` onto a daemon account — single sign-on from the
  box shell — is deliberately left to a follow-up ADR: it touches the
  auth core and deserves its own decision.

## Alternatives considered

- **Ingress on CCU3/OpenCCU too.** The firmware ships no include point
  for add-on web-server fragments (`/etc/lighttpd` is read-only
  firmware, no `/usr/local` include exists in either tree). Rejected as
  a non-goal; the direct port with optional certificate reuse is the
  CCU story.
- **Serving under the prefix without stripping** (the Node-RED pattern,
  which mounts at a sub-path). The daemon and SPA already master the
  stripped-prefix model for HA-Ingress; a second mounting mode would
  double the surface for no gain.
- **Automatic TLS on 8119 when server.pem is readable.** Breaks every
  existing plain-HTTP client at the next update. Rejected for opt-in.

## Consequences

- On openccu-lite the Config UI is reachable at
  `https://<box>/addons/loom/` with the box's certificate, listed in the
  box shell's navigation, WebSocket included, with 8119 closed.
- `TestCCUAddonLighttpdDropinProxiesTheDaemon` pins the fragment to the
  four allowlisted directives it uses, the loopback target, the rc.d
  port and the prefix-strip — a directive outside the allowlist would
  make occulited reject the whole fragment on the box.
- The combined-PEM path is pinned by
  `TestNewCertReloader_CombinedPEM_OneFileForBoth`.
- Live verification on a real box (shell navigation entry, SPA under the
  prefix, WS through the proxy) is recorded in the lite live-verification
  notes once performed.
