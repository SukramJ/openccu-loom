# Changelog — OpenCCU-Loom HA Add-on

## 0.16.0

- **RF reception matrix.** The CCU's pairwise device ↔ partner RSSI matrix is
  now available for RF diagnostics — as a new on-demand "Signal / RSSI matrix"
  section on the Diagnostics page, plus a REST endpoint and WebSocket command —
  alongside the existing per-device signal-quality view.
- **Regex search in the config UI lists.** The device, system-variable, and
  program lists now accept regular expressions (e.g. `BidCos-RF\.MEQ`,
  `MEQ|HEQ`) and fall back to a plain substring match otherwise.
- **Favorites are now a quick-control surface.** Pinned system variables can be
  changed inline (toggle / number / select) right on the start page, and
  inactive programs are dimmed in the program list for easier scanning.
- Removed the obsolete `north.ui.listen` setting — the bootstrap UI has shared
  the same listener (and HA Ingress port) since 0.14.0, so it had no effect. No
  action needed unless you set it by hand; if so, just delete the key.

## 0.15.0

- **`GET /incidents` now returns the recorded incidents** — the diagnostics
  panel was always showing an empty list even though incidents were being
  persisted; this is now fixed.
- **REST API bumped to 2.1.0** (additive) — seven response schemas corrected
  to match what the server actually sends; generated client types regenerated.
- **Optional Matter TimeSynchronization cluster** — new
  `north.matter.enable_time_sync` flag (default off) for controllers that
  require a time-sync surface; leave it off unless your controller needs it.
- Manual device reload now also refreshes link-peer addresses.

## 0.14.6

- **CLI tools (`hmcli`)** now correctly honour `OPENCCU_LOOM_DATA_DIR` when
  run without `--config`, so a containerised `hmcli backup` or
  `hmcli config` opens the same `/data` store the daemon uses.

## 0.14.5

- **CRITICAL — configuration and database were lost on every restart /
  add-on update.** When the daemon started without a config file (the
  normal add-on case), it ignored `OPENCCU_LOOM_DATA_DIR` and wrote its
  SQLite database to an ephemeral path inside the container — so every
  restart started with an empty database, losing your CCU connections,
  admin user, and all SPA-edited settings. **After updating to 0.14.5,
  re-create your CCU and admin user once; from that point on they survive
  restarts and add-on updates.**

## 0.14.4

- **mDNS no longer advertises container-internal addresses.** The add-on's
  mDNS advertisement previously included Docker bridge IPs (e.g.
  `172.30.232.1`); `homematicip_local` would resolve the daemon to that
  address and fail to connect. Only routable LAN addresses are now published.
- Config fields for `public_url`, `tls_cert_file`, and `tls_key_file` now
  have proper labels and help text in the SPA (EN + DE).

## 0.14.3

- **HA Ingress auth passthrough is now ON by default in the add-on.**
  Opening OpenCCU-Loom through the Home Assistant sidebar now logs you
  straight in as admin — no login page, no setup redirect. This relies on
  `panel_admin: true` restricting Ingress to HA admins. Set
  `north.rest.auth.ha_ingress.enabled: false` to opt out and use the
  CCU / local login instead.

## 0.14.2

- **Onboarding (first-run setup wizard) now works behind HA Ingress.**
  Form POSTs and redirects in the setup wizard were resolving against the HA
  origin instead of the add-on's Ingress path, so the wizard could not be
  submitted. All server-rendered pages now emit Ingress-prefix-aware URLs.

## 0.14.1

- **First-run redirect no longer traps CCU-login users on `/setup`.**
  After updating to 0.14.0, operators using CCU-delegated login (the default
  in the add-on) were redirected to the "Create administrator account" wizard.
  The redirect now fires only when there is genuinely no way to authenticate
  (no local user, no CCU auth, no OIDC).
- Setup wizard step indicator now shows the correct "Step N of M" text.

## 0.14.0

- **Single-port onboarding** — login, first-run setup, and OIDC callback are
  now served on the same port as the REST/SPA listener (`:8080`). The
  separate `:8081` port is gone; the add-on no longer exposes it. If you
  referenced port 8081 anywhere, switch to 8080.
- **CCU-delegated login** (ADR 0043) — operators can now log in with their
  CCU username and password; the CCU is only contacted at login, the
  resulting session carries full access. Configured via
  `north.rest.auth.ccu`; CCU-primary is the default in the add-on (local
  users act as break-glass fallback).
- **HA Ingress auth passthrough** (opt-in, default off in this release —
  enabled by default from 0.14.3) — a request proxied through the HA
  Supervisor is accepted as an authenticated admin without a local login.

## 0.13.3

- **HA add-on image now publishes correctly.** The 0.13.0–0.13.2 releases
  failed to publish the HA add-on image due to CI tooling issues. 0.13.3
  switches to a reliable `docker/build-push-action` build. If you were on an
  earlier 0.13.x version and the add-on image was missing, update to 0.13.3.

## 0.13.2

- Attempted fix for HA add-on image publish (superseded by 0.13.3). Daemon
  binaries and the CCU/RaspberryMatic add-on were unaffected in all 0.13.x
  releases.

## 0.13.1

- Attempted fix for HA add-on image publish (superseded by 0.13.2).

## 0.13.0

- **"CCU login" tab in Settings now saves correctly.** The CCU-auth section
  was wired in the SPA but not registered in the backend, so saves were
  rejected with a 400 error.
- **Tri-state config toggles** (e.g. `ccu.primary`) now preserve the "unset /
  use default" state instead of silently writing `false`.
- **Device teach-in (install mode) now works.** Starting pairing from the
  SPA inbox no longer returns a 502. Per-interface install-mode is now wired
  for HmIP-RF, BidCos-RF, and BidCos-Wired; the inbox lists the available
  radios.
- The deprecated CCU-wide `GET`/`POST /install-mode` endpoints are removed;
  use `GET`/`POST /install-mode/interfaces`. API version bumped to 2.0.0.

## 0.12.0

- **CCU as an authentication provider** (ADR 0043, editable in SPA → Settings
  → "CCU login"). Log in with your CCU username and password; CCU-primary is
  the default in the add-on.
- **Rooms & functions management** in Settings → Groups (create / rename /
  delete), Favorites / start page, self-service password change, log-level
  override, audit export with CSV download, and targeted teach-in (serial
  pairing + interface selection).
- **HTTPS** for the daemon listener — set `north.rest.tls_cert_file` +
  `tls_key_file`; certificate hot-reloads without a restart.
- Six additional CCU WebUI parity gaps closed in the SPA (direct-link sender
  side, runtime log-level override, audit date-range filter + pagination).

## 0.11.3

- **HmIP-FWI fingerprint reader fix** — `CODE_ID=31` (idle/standby) was
  being dropped, keeping the last recognized code instead of clearing it.
  The paramset cache is rebuilt on first boot after this update.

## 0.11.2

- **Optimistic values now roll back immediately** when a CCU write is
  rejected (previously they lingered for the full 30 s timeout, making
  switches appear stuck).

## 0.11.1

- **Virtual heating-group temperatures no longer report a spurious `0`**
  after a CCU restart.

## 0.11.0

- Firmware-refresh WebSocket command is now wired.
- `unique_id` is now present on week-profile and schedule-channel-switch
  entities in the REST API.

## 0.10.1

- **ReGa bulk seeder fix** — the seeder no longer drops legitimate `0`
  readings (e.g. `ACTUAL_TEMPERATURE=0 °C`) while still suppressing
  post-restart placeholders. Fixes spurious `0` values reported after a CCU
  restart for all device types.

## 0.10.0

- `unique_id` is now guaranteed non-empty on all REST/WS entity summaries.
- New `update_status` field on device summaries (`up_to_date |
  update_available | installing`).
- Hub pseudo-addresses and climate vocabulary exported as named constants in
  the API schema.

## 0.9.1

- Corrects the published OpenAPI spec for the 0.9.0 wire additions (D1–D3);
  generated client type packages were missing these schemas.

## 0.9.0

- **`hub.<central>.system_update` WebSocket push** — no more polling for
  firmware update state.
- `value_translations` on data-point summaries for ENUM parameters (localized
  labels in the request locale).
- `functions` (Gewerke) now on channel summaries as well as device summaries.
- **Breaking change for `set_color` callers:** light saturation is now
  `0..100` throughout (was `0..1` on some device types); update any client
  that sent saturation as a fraction.

## 0.8.0

- Hub singletons now accessible at `GET /api/v1/hub/data-points` (alarm,
  service messages, inbox, firmware, metrics, connectivity, install-mode).
- Per-device event groups at `GET …/channels/{no}/event-groups`.
- New WebSocket broadcasts for hub singletons eliminate the 30 s hub-refresh
  poll loop.
- Text-display option lists (colors, alignments, etc.) now included in CDP
  state.

## 0.7.1

- Device and channel config reload now available via REST (`POST
  /api/v1/devices/{addr}/reload`, `POST …/channels/{channel}/reload`) in
  addition to the existing WebSocket commands.

## 0.7.0

- **Device-action services** (parity with the HA integration's service
  surface): climate away-mode, on-time for lights/switches/valves, cover
  combined position+tilt, siren turn-on/stop, text-display send/clear.
- Session recording, schedule copy, and force sysvar-refresh commands added.

## 0.6.0

- **Login sessions now survive a daemon restart** — a restart no longer logs
  everyone out.
- **Reverse-proxy support** — set `north.rest.public_url` so the CCU add-on
  "Open Config UI" button links to the correct public address behind a TLS
  proxy.
- **Device-definition export** — `GET /devices/{addr}/export-definition` and
  `hmcli export-def` produce a pydevccu / godevccu-compatible fixture zip.
- **Devices now always appear with their names** — the daemon waits for the
  CCU's readiness endpoint before loading devices, so a co-booting CCU never
  yields nameless devices.
- Config-save behaviour made homogeneous: saving any section now persists to
  the DB and shows the restart-pending banner rather than firing an immediate
  restart. Secrets in section editors are now masked.
- Dark-mode, accessibility, and UX consistency improvements across the SPA.

## 0.5.1

- **Two daemons against the same CCU no longer drag each other's `/health`
  to 503** (ping/pong caller-id scoping).
- **Sensors no longer report spurious `0` after a CCU restart** — the ReGa
  bulk seeder now skips empty/not-yet-measured values.
- `available` for REST/WS/MQTT now reflects full data-point validity
  (overflow/underflow/unobserved = unavailable).

## 0.5.0

- **Opt-in measurement history** for SPA charts (no external stack needed);
  optional InfluxDB line-protocol push exporter.
- Nine new MCP read tools (programs, sysvars, service/alarm messages, inbox,
  system info, rooms, functions, channels).
- Device-type icons in the device list (proxied from the CCU).
- Persistent "restart required" banner with per-field revert and a
  "Changed settings" overview.
- Grid/list toggle for the device list; search and filters now survive
  navigation.
- Full mobile / responsive pass across the config UI.
- Full EN + DE localisation of the SPA (~190 new keys).

## 0.4.0

- Optional pagination (`page` / `per_page`) on hub list endpoints (programs,
  sysvars, alarm/service messages); existing clients are unaffected.
- MQTT MASTER-paramset writes now work (previously silently dropped).
- CCU system-update panel in Settings → System (firmware state + Install
  button).
- **Device list no longer truncates at 200 devices.**
- In-memory user passwords are now bcrypt-hashed; brute-force rate limiting
  on the login form.
- Configurable MQTT retain-cleanup window (`north.mqtt.retain_cleanup_window_ms`).

## 0.3.0

- **Per-central behaviour toggles** (`centrals[].behavior`, runtime-editable
  in SPA and REST): light last-brightness restore, cover group channel,
  sysvar/program scan gates and markers, sysvar scan interval, firmware check,
  delayed device creation.
- mDNS advertisement enriched with `instance` and `centrals` TXT records for
  client auto-discovery.
- Sidebar now works on phones (off-canvas drawer on narrow screens).

## 0.2.0

- **Home Assistant add-on introduced** — install from the HA add-on store by
  adding `https://github.com/SukramJ/openccu-loom` as a repository. The
  add-on runs with `host_network` (so CCU callbacks reach the daemon),
  persists state in `/data`, and exposes the Config UI via Ingress and port
  8080.
- **CCU / RaspberryMatic add-on** packaging added (separate channel).
- Persistent login sessions survive a daemon restart.
- Contract schema digest on `GET /api/v1/info` for client type-parity
  verification.
- Per-interface install-mode sensor and button on MQTT discovery.
- Virtual-remote press buttons on MQTT discovery.
- Many MQTT discovery correctness fixes (sysvar typing, ENUM lower-casing,
  channel-group switch state, duplicate entity suppression, multi-CCU
  unique_id collisions).

## 0.1.0

Initial Home Assistant add-on release. Packages the OpenCCU-Loom 0.1.0
daemon as a native HA add-on with Ingress support, s6-overlay supervision,
and host-network mode for reliable CCU callback delivery.
