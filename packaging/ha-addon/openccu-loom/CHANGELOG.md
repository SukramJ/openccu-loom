# Changelog — OpenCCU-Loom HA Add-on

## 0.35.0

Device-icon fixes and device-linked system variables.

- **Device icons now appear as an HA add-on**: the real CCU device pictures
  showed only a generic placeholder when the add-on ran under Home Assistant
  (they already worked on the CCU add-on) — they now load correctly through
  the Ingress proxy.
- **Device icons in dark mode**: the icons no longer sit in a glaring white
  box; the line-art is inverted to sit cleanly on the dark card.
- **System variables and programs on the right device**: a sysvar or program
  whose name references a device or channel now shows on that physical
  device's card instead of the central hub card.

## 0.34.0

Design & usability overhaul of the web UI.

- **Fresh look**: the UI now uses the Loom teal brand colour throughout,
  with a complete light/dark palette and a subtle woven signature on the
  login and loading screens.
- **Dark mode fixes**: no more white top bar, unreadable active menu
  entries or disabled buttons that look clickable.
- **Correct radio quality readings**: devices no longer show a bogus
  "RSSI 128 dBm" before their first radio event.
- **Clearer Matter page**: parameters are grouped per device, status icons
  come with a legend, and greyed-out checkboxes explain why.
- **Friendlier details**: destructive "Remove" is toned down, product
  photos render cleanly in dark mode, empty lists explain what will appear
  there, the connection badge explains itself, and the theme (light/dark/
  system) can now be set in Settings → Interface.

## 0.33.0

Matter interop hardening release.

- **Matter device numbering now survives restarts.** Endpoint numbers were
  silently re-allocated on every daemon start; fleet changes across a restart
  could break controller-side groups and automations.
- **Buttons behave like real Matter switches.** All press variants of a button
  now share one endpoint with correct press/long-press/release event sequences;
  held buttons no longer spam long-press events. Apple/Google re-learn button
  accessories once after this update.
- **Covers/blinds:** Apple Home now shows the correct direction arrow when a
  cover is moved from a wall button or CCU program; slider drags no longer send
  a burst of radio writes to duty-cycle-limited actuators; cover product types
  report correct values (visible after a controller re-sync).
- **Thermostats:** conformant SystemMode reporting — controllers syncing state
  back no longer get errors while a TRV runs its week program.
- **Faster reconnects:** controllers no longer show the bridge as unresponsive
  for minutes after a restart (graceful Matter session close on shutdown).
- **Dimmer transitions:** Matter transition times are now passed to the device
  as ramp time.
- New documentation on controller-ecosystem limits (Alexa needs UDP port 5540
  for pairing, ~80–100 devices per bridge, and more) in `docs/user/matter.md`.

## 0.32.0

- **Power/energy meters now update live in Matter.** A switch with a power meter
  (HmIP-BSM) now pushes its live power and energy readings to Apple/Google Home
  instead of only updating when the app reads them.
- **Matter button presses now work.** Subscribing to a button/action (via a
  Matter controller) previously failed; button-press events now reach the
  controller.

## 0.31.0

- **Irrigation valves now work in Matter.** An HmIP irrigation valve is now
  bridged as an on/off device, so you can switch it from Apple Home, Google
  Home or Alexa like any other switch.
- **Cleaner Matter device list.** The Matter exposure screen no longer lists
  internal service, status and overflow parameters (things you can never
  usefully expose) — only real, exposable data points appear.
- **New expert option `expose_secondary_channels`** (off by default). Turn it
  on if you want a multi-channel device's extra actor channels and its status
  channel to each appear as their own Matter endpoint.
- **Matter pairing no longer hangs after "Commissioning complete".** Adding the
  bridge to Apple/Google Home could spin a CPU core and abort with "could not
  add accessory"; pairing now completes cleanly.
- **Thermostat changes from Apple/Google now take effect.** Setting the target
  temperature or mode from a Matter controller reached the daemon but was
  rejected before it hit the CCU — it now applies.
- **Changes made elsewhere now reach Matter controllers.** Turning a light,
  thermostat, cover, lock or siren on/off at the wall or via a CCU program now
  updates the accessory in Apple/Google Home instead of only reflecting
  commands the app itself sent.

## 0.30.0

- **Thermostat away mode now takes effect.** Setting an away/holiday period on an
  HmIP thermostat previously sent the wrong values, so the away temperature and
  end time were ignored by the CCU. It now sends the correct parameters and the
  away setpoint and end time work as expected.
- **Clearer errors when a value is rejected.** Writing an invalid value to a data
  point (e.g. a read-only parameter or a value outside the allowed range) now
  returns a "bad request" instead of misreporting it as a temporary CCU outage.
- **Timed durations shown in the editor match what was set.** The value picked
  for on-time / ramp durations now matches the reference implementation, so the
  editor shows the same value the device stores.
- Under the hood: a large batch of test and CI hardening across the device model
  and the MQTT, REST, WebSocket, SPA, and Matter surfaces — no functional change
  for you, but future regressions in those areas are now caught automatically.

## 0.29.0

- **HmIP-LSC colour lamps now offer a warm/cold white (colour temperature)
  control.** The HmIP-LSC supports both full colour and colour temperature at
  the same time; it was previously shown as a plain colour light, so the white
  slider never appeared. It now exposes both, and Home Assistant shows whichever
  is currently active.
- **Doorbell buttons are recognised as doorbells.** The ring of an HmIP-DBB or
  HmIP-DSD-PCB is now published as a doorbell event instead of a generic button
  press, so it appears with the right icon and can trigger doorbell automations.

## 0.28.2

- **Fixed the device control tiles in the Config UI.** Several buttons and
  sliders were sending the wrong command under the hood, so they either did
  nothing (with an error) or did the wrong thing: the switch "on for" button,
  the valve's timed-open preset (which opened for a fraction of a second
  instead of minutes), the light brightness slider, the fixed-colour and
  effect pickers, colour saturation, and the thermostat's "away for a while"
  action. All of these now work as expected. The switch and valve timed
  actions also gained a proper duration input with quick presets instead of a
  single fixed button.

## 0.28.1

- **Fixed a start-up hiccup with the CCU connection.** When the daemon
  started (or a CCU session expired), several requests could each open
  their own CCU login at the same moment and trip the CCU's session
  limit, leaving devices briefly unavailable. The daemon now shares a
  single login instead of racing, so start-up is reliable.
- **Hardened the CCU callback listeners against LAN abuse.** The two
  ports the daemon opens for the CCU to push events to (XML-RPC and
  BIN-RPC) now limit how many connections a single host can hold open at
  once, so another device on your network can no longer exhaust the
  daemon's memory by flooding them. A new expert option lets you restrict
  those ports to accept callbacks only from your configured CCUs. No
  action is needed — the defaults keep working as before.

## 0.28.0

- **CCU disable/edit in the Config UI now actually takes effect
  immediately.** Disabling a connected CCU now really disconnects it
  right away, and an edit that can't be applied live now tells you a
  restart is required instead of showing a plain "saved" message.
- **MQTT reliability fixes.** Command and Home Assistant birth-sync
  handling no longer risk stalling the MQTT connection under load, and
  removing a device now also clears its retained MQTT state so it
  doesn't linger as "available" forever.
- **Backup and restore are now correct with multiple CCUs.** Backups
  and restores are matched to the right CCU instead of always using
  the first one; the Backups page lets you pick which CCU to back up
  when more than one is configured.
- **Config UI polish.** Program and system-variable lists no longer
  silently stop after the first page of results; Settings and the
  confirmation dialog used for delete/disable actions now behave
  consistently with the rest of the app (toast messages, keyboard
  focus handling).
- **New Diagnostics panel** showing per-CCU connection reliability
  status and values-cache statistics, with a reset action.
- **Backup documentation corrected**: the exported archive does not
  include your encryption key — the guidance now says so clearly, and
  `backup create` prints a reminder after every run.
- Various smaller reliability, metrics-accuracy, and internal
  test-coverage improvements; see the full changelog for details.

## 0.27.2

- **New "About" page in the web interface.** The sidebar now shows the
  daemon version at the bottom; clicking it (or the new About entry)
  opens a page with everything a support question needs: version,
  build details, uptime, your CCUs with their firmware versions and
  pending CCU updates, and the license and project links.

## 0.27.1

- **Maintenance release for the CCU/RaspberryMatic add-on variant.**
  Installing or updating that add-on no longer restarts the CCU on
  RaspberryMatic / OpenCCU. No functional changes for the Home
  Assistant add-on — this release only keeps its version in step.

## 0.27.0

- **Security and reliability hardening.** This release rolls up the
  fixes collected since 0.26.6: idempotent REST requests can no longer
  leak one user's response to another, request bodies are size-capped
  to prevent memory exhaustion, the OIDC login flow is hardened against
  token replay, and server errors no longer echo internal details.
- **Maintenance.** Refreshed the underlying software libraries and the
  web-interface toolchain to their latest compatible versions. No
  changes to how you use the add-on.

## 0.26.6

- **Matter pairing: fixes "device not found" in networks with an mDNS
  repeater (multiple subnets/VLANs).** The bridge's answer to the
  phone's device search used a packet format that strict network
  equipment silently discards, so the search reply never reached the
  phone. The answer now follows the mDNS standard exactly and passes
  through repeaters. Together with the 0.26.5 fix, both discovery
  paths (proactive announcement and live query) now work.

## 0.26.5

- **Matter pairing: phones now actually find the bridge.** Apple/Google
  phones search for a Matter device using a filter derived from the QR
  code. The bridge answered general discovery but never proactively
  announced the filter entry the phone looks for, so pairing kept
  failing with "device not found" even after the 0.26.4 fixes. The
  bridge now announces the filter records when the pairing window
  opens and retracts them when it closes.

## 0.26.4

- **Matter pairing works out of the box now.** The bridge previously
  did NOT announce itself on the network unless the expert setting
  `north.matter.mdns_advertise: zeroconf` was set manually — phones and
  hubs reported "device not found" after scanning the QR code. The
  bridge now advertises by default when Matter is enabled.
- **Correct pairing code with default settings.** With no discriminator
  configured, the QR / manual pairing code and the network announcement
  used a wrong placeholder value; they now use the documented default
  consistently.
- **More reliable pairing on the add-on.** The Matter network
  announcement no longer includes internal container addresses
  (Docker / Supervisor networks), which could stall pairing while the
  phone tried unreachable addresses.

## 0.26.3

- **CCU address is validated on save.** The configured CCU host must be
  a plain hostname or IP address; values containing a scheme, path, or
  an embedded port are rejected with a clear message instead of causing
  confusing connection errors later. Existing valid configurations are
  unaffected.

## 0.26.2

- **Setting system variables works again for all types.** Writes to
  list/enum (e.g. `Aus;Niedrig;Normal;Hoch`), number, integer, and
  boolean system variables were silently dropped — the UI/MQTT reported
  success but the CCU value never changed; only text variables worked.
  All types now write correctly, values are converted to the variable's
  declared type (labels, `on/off`, numeric strings all accepted), and
  invalid values are rejected with a clear error instead of vanishing.

## 0.26.1

- **CCU writes no longer break after a CCU reboot or idle session.**
  When the CCU dropped the login session (reboot, ReGa restart,
  inactivity), setting system variables or running programs failed
  permanently with `access denied ("ADMIN" needed 0)` until the add-on
  was restarted. The bridge now keeps the CCU session alive and
  automatically logs in again when the CCU rejects a stale session.
- **Matter: new pairings work again (broken since 0.23.0).** A
  deadlock in the commissioning handshake made every NEW pairing time
  out (Apple Home / Google Home / Home Assistant alike);
  already-paired setups were unaffected. If pairing failed for you,
  retry after this update.
- Internal: chip-tool commissioning suite now runs in CI; from-source
  Docker build repaired.

## 0.26.0

- **MQTT 5.0 by default + circuit breaker.** The MQTT transport was
  upgraded (shared go-mqtt v1.1.0): the bridge speaks MQTT 5.0 on the
  wire — if your broker only supports 3.1.1 (e.g. very old Mosquitto),
  set the new expert option `north.mqtt.protocol_version: "3.1.1"`;
  the connect error names this fix explicitly. Publishes are now
  protected by a circuit breaker: a degraded broker (link up, acks
  missing) no longer stalls the publish pipeline on ack timeouts, and
  breaker trips are visible in the diagnostics counters.
- **Faster, more resilient restarts.** Device and paramset
  descriptions are now persisted across restarts (fleet-wide after one
  boot against a reachable CCU) and the "clear caches + re-pull" admin
  action also clears these persisted rows.
- **Config options now tell the truth.** `basic_enabled` /
  `bearer_enabled` actually gate their auth schemes (default on; an
  explicit `false` rejects the scheme even with credentials
  configured); the never-evaluated `session_enabled` option was
  removed — session login is always available. The
  `ccu_data.easymode_path` override is honored.
- **Diagnostics that were silently empty now work**: the metrics
  snapshot's model section (device/channel/data-point/program counts),
  circuit-breaker/ping-pong/retry incidents, and the reliability event
  stream.
- Internal: large dead-code cleanup (~7,500 lines of unwired
  duplicates removed) guarded by a new CI ratchet; repaired
  integration test suites and their CI gate.

## 0.25.0

- **Security & robustness hardening across the whole northbound surface.** A
  deep code-vs-code audit closed a wave of issues: WebSocket write commands now
  enforce the same roles as REST, the audit trail and CCU passwords are no
  longer exposed to non-admin users, API tokens can be given an expiry, OIDC
  login-CSRF is closed, and session cookies are marked Secure behind TLS.
- **Matter pairing now works reliably.** The bridge reassembles its topology
  automatically once the CCU devices finish loading, so commissioning succeeds
  after a restart without having to toggle a device exposure first. MASTER
  configuration writes are enforced against the edit lock server-side.
- **Backups are encrypted at rest.** Archives are sealed with the data-dir key
  (mode 0600), so a stolen backup no longer leaks live session tokens or
  credentials; restore is atomic and rejects path-traversal entries. Note:
  restoring onto a fresh host needs the `OPENCCU_LOOM_SECRET_KEY` (or `secret.key`
  copied out of band).
- **Correct energy & history figures.** The measurement rollup was rebuilt so
  “energy today” is populated and totals no longer under-count, and retention no
  longer corrupts finalized buckets.
- **Steadier CCU communication.** A duty-cycle stall no longer blocks unrelated
  reads, reconnects are staggered, and a slow MQTT broker no longer freezes
  event delivery.
- Plus `hmcli` and config-pipeline hardening and many smaller fixes — see the
  project CHANGELOG for the full list.

## 0.24.0

- **More Matter parity and hardening.** A second, independent code-vs-code
  re-audit against the reference stack closed a further wave of gaps:
  - Access control is now enforced on Matter *event* reads and subscriptions
    too, so one ecosystem can no longer see another's access-control activity;
    writes to read-only attributes (like a light's on/off state field) are
    rejected instead of quietly reaching the device.
  - Commissioning is sturdier — repeated wrong pass-codes now count against the
    pairing-attempt limit, and controllers that present a longer certificate
    serial (seen on some TVs) can pair.
  - Editing which devices are exposed no longer makes every controller
    re-download the whole bridge.
  - Door locks require a fresh "timed" request to lock/unlock; heating-only
    thermostats stop showing a cooling control; blinds, colour lights and
    on/off switches report a few status fields more faithfully.
- The Matter bridge remains **opt-in and off by default** (`north.matter.enabled`).

## 0.23.1

- **More stable MQTT connection.** The MQTT client no longer tears down and
  reconnects on a brief network hiccup or a busy moment — it now tolerates a
  single missed keep-alive before declaring the link dead, so you'll see far
  fewer spurious disconnect/reconnect log lines against a healthy broker.
- Internally, the MQTT transport moved to the shared `go-mqtt` library; the
  add-on's MQTT behaviour is otherwise unchanged.

## 0.23.0

- **Matter bridge hardening.** A wave of correctness and security fixes brings
  the Matter side closer to the reference stack:
  - Commissioning is more robust — re-pairing after an interrupted attempt, and
    "add to another ecosystem" flows, recover cleanly; only one pairing runs at
    a time; and a re-connecting controller's fast-resume no longer lands on a
    dead session.
  - Certificate updates (UpdateNOC) are now validated before they are stored,
    duplicate fabrics and mismatched keys are rejected, and a removed fabric's
    stale discovery record is withdrawn instead of lingering.
  - Access-control is enforced on every subscription report and per attribute /
    command, closing a privilege-escalation and a cross-ecosystem data-leak
    path; device-type-scoped access rules are honoured.
  - Bridged devices report a stable data-version, so controllers stop
    re-downloading the whole bridge on every reconnect; the human-set bridge
    name/location and event history survive a restart; and the periodic mDNS
    re-announce no longer churns Apple's device cache.
- The Matter bridge remains **opt-in and off by default** (`north.matter.enabled`).

## 0.22.0

- **New Energy view** (`#/energy`) — per-device power/energy consumption and
  feed-in, backed by new persistent hourly/daily history rollup tiers with
  configurable retention.
- **Add or remove a CCU without restarting the add-on.** Adopting or dropping
  a CCU from the Config UI now takes effect immediately.
- **New cross-CCU Fleet view** (`#/fleet`) — see every configured CCU, its
  online status, interfaces and device count at a glance.
- **New whole-home Overview** (`#/overview`) — every device's tile in one
  grouped, filterable dashboard.
- **New Access Control view** (`#/access`) — manage Basic-auth users and API
  tokens from the browser instead of editing the config file.
- **`hmcli` admin CLI** gained `devices`, `sysvar`, `program`, `paramset` and
  `events tail` command groups for scripting against a running daemon.
- **Scheduled, automatic CCU backups** with per-CCU retention (`backup:`
  config section, off by default).
- **Inbound and outbound webhooks** — trigger a value/program change via a
  webhook call, and/or have the add-on POST signed events to your own
  endpoint (both opt-in, off by default).
- **Matter bridge**: commissioning-window commands now correctly require a
  timed interaction window, matching Matter's conformance rules.

## 0.21.3

- **All ports are now configurable in the add-on options.** Set `rest_port`
  (default 8119, the Config UI / Ingress port), `xmlrpc_callback_port` (8120)
  and `binrpc_callback_port` (8129) directly — handy to avoid clashes with other
  things on your Home Assistant host. The previous fixed port list (which did
  nothing under host networking) was removed.
- **The default UI port changed from 8080 to 8119.** Direct access is now at
  `http://<ha-host>:8119/app/`. The sidebar panel keeps working unchanged; only
  change `rest_port` if you access the UI directly (then it stays at 8119 for
  the panel).

## 0.21.2

- **A CCU you run on the same machine (configured as `localhost`) is no longer
  shown again as a new discovery.** Once such a CCU connects, OpenCCU-Loom learns
  its serial automatically and recognises it as already configured — no matter
  which address you used. Also fixes CCUs that were added before this recognition
  existed; they are picked up after they next connect.

## 0.21.1

- **More reliable recognition of already-added CCUs.** A found CCU is now matched
  by its short 10-character serial — the same form the CCU reports internally — so
  it is recognised as "already configured" consistently, even across address
  changes. CCUs added in 0.21.0 keep being recognised.

## 0.21.0

- **Found CCUs are recognised even after their address changes.** Discovery now
  remembers a CCU by its hardware serial, so a CCU whose IP changed isn't shown
  again as "new". CCUs added before this update keep working and are matched by
  address until you re-add them.
- **Adding a found CCU pre-fills a stable address.** If the CCU runs on the same
  machine as OpenCCU-Loom it is filled in as `localhost`; if it runs as another
  Home Assistant add-on, its add-on hostname is used instead of the changing
  docker IP — so the connection keeps working across restarts.

## 0.20.1

- **Opening the add-on no longer gets stuck on the setup wizard.** When you open
  OpenCCU-Loom through the Home Assistant sidebar you are already signed in as
  admin, so it now takes you straight into the app instead of showing the
  first-time setup screen (which couldn't be finished and ended on a login page
  you have no password for). Add or change your CCU and MQTT settings any time
  under Settings → CCUs.
- **Brief "session expired" hiccups now recover on their own** instead of
  bouncing you to a login screen.

## 0.20.0

- **CCUs on your network are now found automatically.** OpenCCU-Loom scans the
  local network for Homematic / OpenCCU central units and shows them in
  Settings → CCUs and during first-time setup, so you can add a CCU with its
  address already filled in instead of typing it. CCUs you don't want can be
  ignored (they stay hidden until you un-ignore them).
- This only listens on the local network; nothing about the add-on is sent
  out, and it simply finds nothing if your network blocks discovery. You can
  turn it off under `north.discovery.ssdp`.

## 0.19.0

- **First-run setup and login now run entirely in the web UI.** The initial
  setup wizard (admin account → language → CCU → MQTT) is now part of the
  single-page app instead of a separate no-JS page, so onboarding looks and
  behaves like the rest of the panel. Nothing changes for existing
  installations — you sign in exactly as before.
- The `/health` and `/about` pages stay available as a plain fallback for the
  rare case where the web UI itself fails to load.
- Login attempts keep their per-address brute-force speed-bump.

## 0.18.5

- **Fixed: setting colour or colour-temperature on a Matter light now works.**
  Colour-picker changes from Apple Home / Google Home were silently dropped (the
  bulb never changed); the ColorControl command path is now decoded correctly.
- Health view now shows a `scheduler` row that flags when a background job has
  recently failed (and clears once things settle).
- "Reload device" now refetches only that device and its channels instead of
  every device on the CCU — faster on large installations.
- Internal: filled in the OpenAPI schema for several REST responses so the
  documented API and the web UI stay in sync, and removed unused code. No
  functional change from these two.

## 0.18.0

- **Combined data points** for cover blinds (level + slats) and RGB lights (hue
  + saturation) are now published as their own MQTT / Home Assistant entities.
- **Service & alarm messages now show a readable name** (the translated message
  code, e.g. "Low Battery") instead of only the raw identifier.
- Removed an unused, speculative HmIP-COOK ("hood") device type that had no real
  HomeMatic counterpart.

## 0.17.5

- **Fixed: Matter string attribute writes (e.g. device name) are no longer
  stored as empty.** Plus a conformance sweep against matter.js HEAD (TLV
  string handling, Read/Subscribe path validation, write DataVersion/status
  rules, event-path urgency, VendorID validation).
- The embedded Matter schema snapshot now records the exact matter.js commit it
  was generated from.

## 0.17.4

- **Fixed: the device-detail header now uses the full width on narrow screens.**
  Inside the Home Assistant sidebar (Ingress), the device page header squeezed
  the device name and model into a sliver and wrapped them character-by-character;
  it now stacks cleanly and only places the action buttons beside the title when
  there is enough room.

## 0.17.3

- **Updated the embedded Homematic metadata to openccu-data 2026.6.1** (device
  translations + easymode definitions), including curated labels for HmIP-DLP,
  HmIP-UDI-SMI55, HmIP-SMO230 and HmIP-SWDO-PL-2.

## 0.17.2

- **Fixed: removed devices now disappear from Home Assistant immediately.** A
  device deleted from the CCU while the daemon runs has its HA-Discovery configs
  retracted right away, instead of lingering as "unavailable" until the next
  daemon restart.

## 0.17.1

- **Fixed: battery values now show in the Signal-quality table.** Battery level
  is read from the calculated voltage level (which was previously missed), and
  low-battery now also recognises HmIP's `LOWBAT` flag.
- **Every sensible table column is now sortable** by clicking its header,
  across all tables in the Config UI.

## 0.17.0

- **Homogeneous, sortable tables across the Config UI.** Devices, system
  variables, programs, firmware, the new Signal-quality view, plus messages,
  inbox, backups, the audit log, Matter, and the settings admin tables now
  share one table with click-to-sort columns, search, and remembered settings.
- **Signal quality** is its own menu entry: per-device RSSI (colour-coded),
  battery level (colour-coded), and reachability — searchable and sortable.
- **Device list** gains a cards ↔ table toggle (multi-select and grouping
  kept), and remembers your view, search, sort, and filters across reloads.

## 0.16.1

- **RSSI overview now works for HmIP devices.** The new "Signal quality (RSSI)"
  section on the Diagnostics page shows per-device reception strength
  (RSSI_DEVICE / RSSI_PEER, dBm) plus reachability for HmIP and BidCos devices
  alike — replacing the 0.16.0 approach that only worked for classic BidCos-RF
  and showed "no data" on HmIP.

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
