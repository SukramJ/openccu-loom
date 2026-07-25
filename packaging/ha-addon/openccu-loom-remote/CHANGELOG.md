<!--
Home Assistant renders this file as the add-on changelog in its UI.
Keep entries condensed; the full history lives in the repository's
top-level CHANGELOG.md. Newest version first.
-->

# 0.48.3

**Fixes the live-connection flicker behind this proxy.** The proxy now preserves
the browser-facing `X-Forwarded-Host` a trusted upstream (e.g. Traefik) set, so
the daemon's live WebSocket accepts the handshake through the chained proxy.
Update the paired instance to 0.48.3 too (it carries the matching WebSocket
heartbeat fix and the group-picker change).

# 0.48.2

Version ride-along. No changes to the proxy itself; the fix ships in the paired
instance (security bump of a bundled OpenAPI-validation library,
GHSA-r277-6w6q-xmqw) — update the instance to 0.48.2 too.

# 0.48.1

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance (live WebSocket reconnect behind a reverse proxy; redesigned
heating-group member picker) — update the instance to 0.48.1 too.

# 0.48.0

Version ride-along. No changes to the proxy itself; the features ship in the
paired instance (heating-group administration — create/edit/delete; central
links show their live active state) — update the instance to 0.48.0 too.

# 0.47.4

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance (room/function combobox with inline create, iPad diagram
channel picker) — update the instance to 0.47.4 too.

# 0.47.3

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance (program-summary umlauts, diagram picker UX, energy/links/signal
UI) — update the instance to 0.47.3 too.

# 0.47.2

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance (heating-group listing, per-channel visibility/lock, auth
toggle restart hint) — update the instance to 0.47.2 too.

# 0.47.1

Version ride-along. No changes to the proxy itself; the paired instance's
SPA fix makes the brand logo render again when accessed through this
remote proxy (update the instance to 0.47.1 too).

# 0.47.0 (unreleased)

Version ride-along with the OpenCCU-Loom device-workflows wave; no
changes to this add-on.

# 0.46.0 (2026-07-22)

Version ride-along with the OpenCCU-Loom device-admin wave; no
changes to this add-on.

# 0.45.0 (2026-07-20)

Version ride-along with the OpenCCU-Loom REST naming extension; no
changes to this add-on.

# 0.44.3 (2026-07-20)

Version ride-along with the OpenCCU-Loom data-point naming fix; no
changes to this add-on.

# 0.44.2 (2026-07-19)

Fix: instance names may contain capital letters (e.g. `OttoLoom`),
and stray surrounding whitespace (a classic paste mistake) is trimmed
instead of rejected — both previously broke saving the configuration.

# 0.44.1 (2026-07-19)

Initial release of **OpenCCU-Loom Remote** — an ingress proxy that
brings the Config UI of one or more remote OpenCCU-Loom instances into
the Home Assistant sidebar (no local daemon required).

- Multiple instances behind one panel: overview page with live status
  tiles (health, version), each instance under its own path.
- Optional per-instance API token: HA admins land in the remote UI
  without a second login; without a token the remote login page is
  proxied through.
- `http://` and `https://` upstreams; `tls_insecure` flag per instance
  for self-signed certificates.
