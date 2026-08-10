<!--
Home Assistant renders this file as the add-on changelog in its UI.
Keep entries condensed; the full history lives in the repository's
top-level CHANGELOG.md. Newest version first.
-->

# 0.57.2

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.57.1

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.57.0

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.56.0

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.55.3

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.55.2

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.55.1

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.55.0

- Version bump in lockstep with the main add-on. No change to the proxy
  itself. The remote instances it fronts may now hide views through
  their own **Settings → Navigation & views**; the proxy passes whatever
  they serve through untouched.

# 0.54.7

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.6

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.5

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.4

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.3

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.2

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.1

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.54.0

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.53.1

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.53.0

- Version bump in lockstep with the main add-on. No change to the proxy
  itself.

# 0.52.12

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.12 too.

# 0.52.11

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.11 too.

# 0.52.10

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.10 too.

# 0.52.9

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.9 too.

# 0.52.8

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.8 too.

# 0.52.7

Version ride-along. No changes to the proxy itself; the change ships in the
paired instance — update the instance to 0.52.7 too.

# 0.52.6

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.6 too.

# 0.52.5

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.5 too.

# 0.52.4

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.4 too.

# 0.52.3

Version ride-along. No changes to the proxy itself; the fix ships in the
paired instance — update the instance to 0.52.3 too.

# 0.52.2

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance — update the instance to 0.52.2 too.

# 0.52.1

Version ride-along. No changes to the proxy itself; the fixes ship in the
paired instance — update the instance to 0.52.1 too.

# 0.52.0

Version ride-along. No changes to the proxy itself; the features ship in the
paired instance — update the instance to 0.52.0 too.

# 0.51.0

Version ride-along. No changes to the proxy itself; the feature ships in the
paired instance (WS verbs for the add-on self-updater) — update the instance
to 0.51.0 too.

# 0.50.1

Version ride-along. No changes to the proxy itself; the fix ships in the paired
instance (CCU add-on runtime detection for the self-update mechanism) — update
the instance to 0.50.1 too.

# 0.50.0

Version ride-along. No changes to the proxy itself; the feature ships in the
paired instance (CCU add-on self-update on OpenCCU/RaspberryMatic) — update
the instance to 0.50.0 too.

# 0.49.3

Version ride-along. No changes to the proxy itself; the feature ships in the
paired instance (areas — room groupings with filters across the UI) — update
the instance to 0.49.3 too.

# 0.49.2

Version ride-along. No changes to the proxy itself; the fixes ship in the paired
instance (alarm setup wizard selects sensors/outputs inline; sirens shareable
across alarm zones without save errors) — update the instance to 0.49.2 too.

# 0.49.0

Version ride-along. No changes to the proxy itself; the change ships in the
paired instance (sysvar/program markers now steer HA's enabled-by-default for
hub entities) — update the instance to 0.49.0 too.

# 0.48.9

Version ride-along. No changes to the proxy itself; the fix ships in the paired
instance (channel-level custom-DP unique_ids for the HA drop-in) — update the
instance to 0.48.9 too.

# 0.48.8

Version ride-along. No changes to the proxy itself; the fix ships in the paired
instance (group editor shows its members by name again) — update the instance to
0.48.8 too.

# 0.48.7

Version ride-along. No changes to the proxy itself; the fix ships in the paired
instance (heating groups no longer stick in the device inbox) — update the
instance to 0.48.7 too.

# 0.48.6

**Security.** Completes the redirect-rewriting hardening against open redirects
(CodeQL). 0.48.5 gated the final rewrite; this closes the remaining leading-slash
checks in the rewrite helpers so a `//host` / `/\host` target can never be
emitted. Update the paired instance to 0.48.6 too. No user-visible change.

# 0.48.5

**Security & maintenance.** The proxy's redirect rewriting is hardened against
open redirects (CodeQL), and dependencies are refreshed. Update the paired
instance to 0.48.5 too. No user-visible change.

# 0.48.4

**Really fixes the live-connection flicker through this proxy.** 0.48.3's
`X-Forwarded-Host` approach did not survive every proxy chain; the daemon now
accepts the token-authenticated live WebSocket this proxy forwards regardless of
the forwarded host. Update the paired instance to 0.48.4 too.

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
