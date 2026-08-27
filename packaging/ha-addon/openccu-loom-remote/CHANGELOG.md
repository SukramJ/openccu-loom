<!--
Home Assistant renders this file as the add-on changelog in its UI.
Keep entries condensed; the full history lives in the repository's
top-level CHANGELOG.md. Newest version first.
-->

# 0.65.3

- Version alignment with OpenCCU-Loom 0.65.3. The proxy itself is unchanged;
  the release corrects the published API description of the device-created
  event.

# 0.65.2

- Version alignment with OpenCCU-Loom 0.65.2. The proxy itself is unchanged;
  the release stops a CCU reconnect from reporting every paired device as
  newly created, and lets clients read when their login expires so they can
  renew before being disconnected.

# 0.65.1

- Version alignment with OpenCCU-Loom 0.65.1. The proxy itself is unchanged;
  the release makes Matter accessories conform to the device types the
  specification defines for them — thermostats stop serving measurement
  clusters they are meant to consume, metering plugs move their electrical
  readings onto their own accessory, and battery levels move onto the device's
  own accessory instead of a nameless extra one.

# 0.65.0

- Version alignment with OpenCCU-Loom 0.65.0. The proxy itself is unchanged;
  the release makes the daemon's calculated sensors reachable over Matter,
  surfaces the Security & Safety domain and the mDNS advertiser on the health
  page, and adds four capability tokens for surfaces a client could not
  discover before.

# 0.64.2

- Version alignment with OpenCCU-Loom 0.64.2. The proxy itself is unchanged;
  the release fixes a backup restore that left the daemon serving the
  configuration it had just replaced, and an MQTT settings save that reported
  success without reaching the running bridge.

# 0.64.1

- Version alignment with OpenCCU-Loom 0.64.1. The proxy itself is unchanged;
  the release documents a live-event field the daemon had been sending
  without describing it, so external clients can consume it.

# 0.64.0

- Version alignment with OpenCCU-Loom 0.64.0. The proxy itself is unchanged; the
  release adds the backup storage location and a delete button to the Backups
  page, an MQTT sensor for the daemon's own connection, and a ventilation
  select for garage doors. It also stops the daemon from reconnecting an
  interface it is still bringing up — which announced the whole fleet going
  offline and back on every start — restores the start-up events a Matter
  controller reads after pairing, and fixes the classic BidCos thermostats:
  HM-CC-TC showing no temperature at all, and the RF wall thermostats showing
  no humidity.

# 0.63.0

- Version alignment with OpenCCU-Loom 0.63.0. The proxy itself is unchanged; the
  release carries the medium and low findings of the full-codebase audit —
  saving channel configuration, configuration entities keeping their value,
  colour reaching Matter controllers, the raw-topic switch taking effect, and a
  number of device commands that sent values real hardware rejects.

# 0.62.0

- Version alignment with OpenCCU-Loom 0.62.0. The proxy itself is unchanged; the
  release carries the critical and high findings of a full-codebase audit —
  availability reporting during a CCU outage, MQTT reconnect after a reload,
  runtime-adopted CCUs keeping their periodic jobs, session and token revocation,
  the alarm domain's outage and shutdown behaviour, and several CCU-side reads
  that only ever worked against the simulator.

# 0.61.4

- Version alignment with OpenCCU-Loom 0.61.4. The proxy itself is unchanged;
  the release re-lands the classic-RF thermostat weekly-profile fix and corrects
  the install-mode sensor id so pairing-mode sensors seed on start.

# 0.61.3

- Version alignment with OpenCCU-Loom 0.61.3. The proxy itself is unchanged;
  the release is a full-codebase audit fix wave for the daemon — security
  hardening (Basic-auth rate limiting, logout closing open connections, a
  closed script-injection path), MQTT/Home-Assistant entity and backup fixes,
  alarm keypad-lockout fixes, and honest "unknown" status during a CCU outage.

# 0.61.2

- Version alignment with OpenCCU-Loom 0.61.2. The proxy itself is
  unchanged; the release corrects the 0.61.1 "Systemzustand" fix, which
  reached the wrong internal health tracker and reported a constant 0 % —
  the sensor now shows the CCU's real health score.

# 0.61.1

- Version alignment with OpenCCU-Loom 0.61.1. The proxy itself is
  unchanged; the release fixes three "Homematic(IP) Local for OpenCCU"
  integration bugs on the daemon side — an already-integrated OpenCCU being
  re-discovered on every Home Assistant restart, the per-interface
  connectivity sensors stuck on "disconnected", and the "Systemzustand"
  diagnostic sensor stuck on "unknown".

# 0.61.0

- Version alignment with OpenCCU-Loom 0.61.0. The proxy itself is
  unchanged; the release is the follow-up to the 0.60.0 audit — a fresh
  full-codebase pass and three defect waves headlined by security fixes
  (token revocation now severs live WebSocket sessions, the channel lock is
  enforced on every write path, HTTP Basic no longer bypasses CSRF), data
  integrity (a redacted config export no longer wipes stored secrets on
  import; backup restore clears the stale write-ahead log), and Home Assistant
  discovery no longer creating permanently-unavailable entities. The MQTT
  transport moves to go-mqtt v1.3.0. API clients: the north-bound contract
  version moves to 6.0.0 to match what the daemon already did.

# 0.60.0

- Version alignment with OpenCCU-Loom 0.60.0. The proxy itself is
  unchanged; the release is a maintenance release built from a full audit
  of the code base, fixing device-configuration writes, the interface
  connectivity display, a silent refused-arming path and several
  credential-handling defects in the daemon.

# 0.59.1

- Version alignment with OpenCCU-Loom 0.59.1. The proxy itself is
  unchanged; the release is a maintenance release fixing 136 defects
  across the daemon, including an alarm-engine lock-up and a crash.

# 0.59.0

- Version alignment with OpenCCU-Loom 0.59.0. The proxy itself is
  unchanged; the release adds Matter session diagnostics to the API.

# 0.58.6

- Version alignment with OpenCCU-Loom 0.58.6. The proxy itself is
  unchanged; the release gives the RF tunable-white dimmers a colour
  temperature, which they never had.

# 0.58.5

- Version alignment with OpenCCU-Loom 0.58.5. The proxy itself is
  unchanged; the release makes a siren silenceable again and restores
  several device values that were missing from every device.

# 0.58.4

- Version alignment with OpenCCU-Loom 0.58.4. The proxy itself is
  unchanged; the release stops a parameter from reaching Home Assistant
  under two different names, and shows enum values in your language over
  MQTT.

# 0.58.3

- Version alignment with OpenCCU-Loom 0.58.3. The proxy itself is
  unchanged; the release fixes device week profiles — Sunday, entries
  past the 24th, condition names, and durations that shortened on save.

# 0.58.2

- Version alignment with OpenCCU-Loom 0.58.2. The proxy itself is
  unchanged; the release fixes the hidden-parameters screen's
  week-profile categorisation and makes its rule badges concrete.

# 0.58.1

- Version alignment with OpenCCU-Loom 0.58.1. The proxy itself is
  unchanged; the release repairs the motion-detector reset added in
  0.58.0, which never reached the devices, and extends it to presence
  detectors.

# 0.58.0

- Version alignment with OpenCCU-Loom 0.58.0. The proxy itself is
  unchanged; the release adds the motion-detector reset to the daemon's
  alarm system, reachable through the surfaces this add-on forwards.

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
