# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.46.0] — unreleased

### Added

- **First-time configuration when accepting an inbox device.**
  `POST /devices/{addr}/accept` (and the WebSocket command
  `inbox.accept`) now take an optional body — `name`,
  `include_channels`, `rooms`, `functions` — applied to the device right
  after it is accepted out of the inbox: the name is persisted to the
  CCU (optionally cascading to every channel), and the room / function
  (Gewerk) assignments go through the ReGa hub-writer. An empty or
  omitted body keeps the plain accept-only behaviour, so the change is
  backward compatible. The follow-up steps are best-effort but never
  swallow errors: if the accept succeeds and a follow-up step fails the
  response is a 502 whose title states the device was already accepted,
  so only the configuration needs re-applying. The SPA's "Accept" action
  now opens a dialog with a name field, room and function multi-selects
  (populated from `GET /rooms` and `GET /functions`) and a
  "rename channels" toggle; leaving everything blank just accepts. REST
  `APIVersion` 2.32.0.
- **Channel rename over REST + WebSocket.** A new
  `PATCH /devices/{addr}/channels/{no}` endpoint (and WebSocket command
  `device.rename_channel`) renames a single channel; the SPA exposes it
  via a pencil affordance on each channel in the device detail view.
- **Delete device with factory-reset / force options and a dependency
  warning.** `DELETE /devices/{addr}` gains optional `reset` and `force`
  query flags that map onto the CCU `deleteDevice` delete bitmask —
  `reset` also factory-resets the device during removal, `force` removes
  an unreachable device even when the CCU cannot complete the handshake
  (both default to off, preserving the plain-unpair behaviour). The SPA
  remove action becomes a small options dialog (unregister-only vs.
  factory-reset radio plus a force checkbox) that warns up front when
  direct links or CCU programs still reference the device. A backend
  without a pairing concept (CUxD) now answers 422 instead of 502. REST
  `APIVersion` 2.31.0.

### Fixed

- **Device and channel renames now persist to the CCU.** A rename used
  to mutate only the in-memory model and was silently lost on the next
  device reload. `PATCH /devices/{addr}` and the WebSocket
  `device.rename` command now dispatch to the CCU's `Device.setName` /
  `Channel.setName` JSON-RPC methods (resolving the ReGa ISE-ID first),
  and propagate the CCU error instead of swallowing it. A backend
  without JSON-RPC (Homegear, CUxD) answers 422 rather than pretending
  success. `PATCH /devices/{addr}` and `device.rename` gain an optional
  `include_channels` flag that also renames every channel with the
  `"<name>:<channelNo>"` pattern (the CCU WebUI convention); the SPA
  rename dialog offers it as a toggle, on by default. REST `APIVersion`
  2.30.0.
- **Reboot a CCU from the daemon.** A new admin-only endpoint
  `POST /api/v1/system/ccu/{central}/reboot` reboots one CCU host: it runs
  a ReGa script (`reboot_ccu`) that persists the CCU's state
  (`system.Save()`) and then triggers `/sbin/reboot`. The southbound
  connection to that central drops for the duration of the reboot and
  recovers automatically once the CCU is back (the readiness gate re-runs
  the bring-up). The SPA surfaces this as a new **CCU maintenance** card
  under Settings → System with a per-central reboot button behind the
  shared destructive-confirm dialog and a toast result. This reboots the
  CCU hardware — distinct from `POST /system/restart`, which restarts the
  OpenCCU-Loom daemon itself. REST `APIVersion` 2.33.0.
- **Permanently suppress service messages.** "Disable" now durably
  suppresses a service message's channel parameter on the CCU via
  `Interface.suppressServiceMessages` instead of merely acknowledging it
  once — the device stops raising the message until the suppression is
  cleared. `POST /api/v1/service-messages/{id}/disable` resolves the
  message's channel + service parameter and suppresses it; the new
  `GET /api/v1/service-messages/suppressed` lists the active suppressions
  (reconciled against each CCU's live
  `Interface.getSuppressedServiceMessages`); and `POST
  /api/v1/service-messages/unsuppress` (body `channel` + optional
  `parameter` / `interface`, optional `central` query) clears one. The
  matching `service_messages.suppressed` / `service_messages.unsuppress`
  WebSocket commands mirror the REST surface. This closes a long-standing
  gap: the client-layer suppression path
  (`InterfaceClient.SuppressServiceMessage` /
  `GetSuppressedServiceMessages`) and the `HubCoordinator` seam existed
  but were never wired — central bring-up now installs the suppressor so
  the calls reach the CCU. The Config UI Messages view gains a "Hide
  permanently" action per service message (confirm dialog + toast) and a
  new "Suppressed" tab listing active suppressions with a "Restore"
  action. REST `APIVersion` 2.32.0.
- **Bulk acknowledge for service and alarm messages ("Acknowledge
  all").** New `POST /api/v1/service-messages/ack-all` and
  `POST /api/v1/alarm-messages/ack-all` endpoints (and the matching
  `service_messages.ack_all` / `alarm_messages.ack_all` WebSocket
  commands) clear every quittable message of a class in a single CCU
  pass and return the number acknowledged (`{"acknowledged": n}`). Both
  accept an optional `central` query parameter to scope the operation to
  one CCU; when omitted every registered central is acknowledged. Two
  new ReGa scripts drive the wire pass — service messages honour the
  per-message writability gate, alarm messages are acknowledged
  unconditionally, mirroring the CCU WebUI's "acknowledge all" loop. The
  Config UI Messages view gains an "Acknowledge all" button per tab,
  shown only when acknowledgeable messages exist, guarded by a confirm
  dialog and reporting the acknowledged count via a toast. REST
  `APIVersion` 2.32.0.
- **Rename a direct link after it has been created.** A link's name
  and description were previously settable only at creation time. The
  daemon now exposes the CCU's `Interface.setLinkInfo` call end to end:
  a new `LinksDomain.SetLinkInfo` (interface resolved from the sender
  device, like `ListLinks`/`AddLink`, with an audit `link_update`
  entry), the REST endpoint `PATCH /api/v1/devices/{addr}/links`
  (body `{sender_address, receiver_address, name, description}`), the
  WebSocket command `links.set_info`, and a per-row rename action
  (pencil) in the SPA device-links view with a small name/description
  editor. Name and description are written verbatim, so either field
  can be cleared with an empty string. REST `APIVersion` 2.35.0.

## [0.45.0] — 2026-07-20

### Changed

- **REST is the single naming authority: `translated_name` now also
  carries the collapsed name for label-omitted data points.** When
  `label_omitted` is true (the "primary parameter" marker), the
  data-point summary's `translated_name` no longer arrives empty — it
  holds the channel-level collapsed name (channel name plus
  multi-channel `chN` marker, device prefix stripped; empty only when
  the collapse reduces to the device name alone). REST consumers such
  as `openccu-loom-client` no longer need any client-side entity-name
  composition. The MQTT discovery plane is unchanged (`name: null` on
  omitted labels). REST `APIVersion` 2.29.0 (2.28.0 + 2.29.0 across the two naming PRs).
- **Custom data points ship their entity names too.** The CDP summary
  gains `translated_name` (the fully composed channel-level display
  name: custom channel names verbatim, `ch<no>`/`vch<no>` group
  markers for derived names, locale-aware postfix labels for button
  locks; empty on the device-name collapse) and `parameter_name` (the
  untranslated marker/postfix portion). Composition lives in the new
  `device.BuildCustomDataPointName`, mirroring the reference's
  `get_custom_data_point_name` — including the marker digits following
  the channel-name suffix and the single-primary collapse applying
  only on the primary channel.

## [0.44.3] — 2026-07-20

### Fixed

- **Multi-channel postfix no longer overrides unique custom channel
  names.** The `ch<no>` postfix for parameters that exist on multiple
  channels of a device is now only appended when the channel name alone
  does not identify the channel — i.e. for device-derived names, names
  following the `<name>:<no>` scheme, or when several channels providing
  the same parameter share the same custom name. A channel with a unique
  custom name (e.g. a status channel named `<sub device> Status`) keeps
  its clean data point name. Mirrors aiohomematic 2026.7.10.

## [0.44.2] — 2026-07-19

### Fixed

- **OpenCCU-Loom Remote: instance names accept mixed case.** The
  add-on schema and the proxy rejected names like `OttoLoom`
  (lowercase-only slug); the constraint is relaxed to
  `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` — the name stays a single URL
  path segment, but capital letters are fine everywhere it is used
  (mount path, tile label, DOM id, cookie scope). Surrounding
  whitespace (a classic paste mistake) is trimmed instead of rejected,
  by the schema and the proxy alike.

## [0.44.1] — 2026-07-19

### Added

- **New HA add-on: OpenCCU-Loom Remote** (ADR 0054) — an ingress proxy
  that brings the Config UI of one or more **remote** OpenCCU-Loom
  instances into the Home Assistant sidebar, without running a local
  daemon. Multiple instances mount under one panel (overview page with
  live status tiles — health, version — when more than one is
  configured); an optional per-instance API token logs HA admins into
  the remote UI without a second credential, and without a token the
  remote login page is proxied through (session cookies are scoped per
  instance). Upstreams may be `http://` or `https://` (per-instance
  `tls_insecure` for self-signed certificates); WebSockets and the
  HA theme bridge work through the proxy. Ships as
  `ghcr.io/sukramj/openccu-loom-remote-ha-{arch}` from
  `packaging/ha-addon/openccu-loom-remote/`; the
  `openccu-loom-remote` binary also rides in the release image and
  archives.

## [0.44.0] — 2026-07-19

### Changed

- **CCU metadata now ships as the versioned go-openccu-data module**
  (ADR 0053) — the extracts (translations, easymodes, profiles,
  curated overlays, device semantics) come from
  `github.com/SukramJ/go-openccu-data` instead of a hand-synced
  `internal/ccudata/embedded/` copy. Regeneration is automated on
  every upstream release (repository_dispatch → module PR → dependabot
  bump); `make bump-ccudata` is the manual fallback, the old
  `update-ccu-data`/`refresh-ccudata` targets, `MANIFEST.json` and the
  drift-check script are gone. The migration surfaced (and upstreamed)
  loom-only curation: the 0.42.9 BWTH parameter labels now live in the
  shared source of truth.

- **Doorbells ring — with the shared curated model list** — press
  events of the doorbell devices now follow Home Assistant's doorbell
  contract: the discovered `event` entity announces (and the event
  topic fires) HA's standard `ring` type instead of `press_short`
  (mandatory for doorbell event entities from HA 2027.4; mirrors the
  reference stack's ring-event fix). Other press types are unchanged.
  The doorbell classification now comes from the upstream data
  package's new curated `device_semantics` extract — one source of
  truth shared with the reference stack — and gains the classic
  `HM-Sen-DB-PCB` alongside `HmIP-DBB` and `HmIP-DSD-PCB`.
  **Breaking:** consumers of the raw channel `/event` topic of these
  three models receive `{"event_type":"ring"}` for the short press
  now; HA automations triggering on the entity's `press_short` event
  type must switch to `ring`.

## [0.43.4] — 2026-07-19

### Fixed

- **MCP write tools rejected every real write** — `set_datapoint` and
  `write_paramset` checked device ownership by passing the raw target
  address to the per-device lookup; real writes always target a
  channel (`ADDR:n`), so the lookup missed and every write failed with
  `device … belongs to central ""`. The guard now strips the channel
  suffix before the ownership lookup — and still rejects a
  `central_name` that does not own the device. Found live on the first
  agent-driven switch attempt; the previous test only covered a
  device-level address.

## [0.43.3] — 2026-07-18

### Fixed

- **The MCP endpoint rejected every credential** — the `/mcp` mount
  wrapped its handler only in the auth chain's `Require` step, which
  checks the identity the `Resolve` middleware places in the request
  context; `Resolve` never ran on that mount (it sits outside the REST
  router's middleware stack), so every request — valid Bearer token,
  Basic auth, or none — got `401 no valid credentials`. The mount now
  resolves credentials before requiring them, restoring the documented
  "same auth chain as REST" behaviour. Pinned by a mount-level
  regression test.
- **Claude Desktop connection example fixed** — the documented
  `claude_desktop_config.json` snippet put `"Authorization: Bearer …"`
  into `args`, which Claude Desktop mangles (spaces in args); mcp-remote
  then fell into an OAuth discovery flow and failed with `/register`
  404s. The example now passes the full header value through an
  environment variable (`Authorization:${AUTH_HEADER}`), which
  mcp-remote expands itself.

## [0.43.2] — 2026-07-18

### Added

- **Localised siren tone / pattern / soundfile pickers** — the
  candidate extras now carry parallel `*_labels` lists translated
  through the curated CCU value translations (APIVersion 2.27.0), so
  the ASIR tone and pattern dropdowns show "Frequenz steigend" instead
  of `FREQUENCY_RISING`; raw wire values remain what is stored.
- **Optical pattern on the acoustic-siren card** — the acoustic
  activation writes the optical selection in the same atomic device
  paramset, so the acoustic card now exposes the optical dropdown too
  (previously only optical-siren and alarm-light cards had it).
- **Stale enrollments are visible and repairable** — pre-0.43
  enrollments could point at a non-siren channel (the old dialog
  defaulted to `:1`); such rows never fired and, since 0.43.0's save
  validation, block the whole output set with 422 — invisibly. The
  output card now flags an ineligible channel/class pair with an
  explanation and, when the device has exactly one eligible channel,
  offers a one-click channel repair.

## [0.43.1] — 2026-07-18

### Added

- **Sysvar-mirror outputs became configurable — and can target existing
  alarm variables** — the add-output dialog no longer demands a device
  for the sysvar-mirror class: it asks for the central and the variable
  target instead. Either a managed value-list variable (created on the
  CCU automatically, as before — but the name was previously not
  settable from the SPA at all, leaving the enrollment a silent no-op)
  or, new, an operator-owned ALARM-type variable: the mirror then
  writes true while triggered and false otherwise, never creates or
  retypes the variable, and accepts no inbound intents through it. The
  output card now edits the variable name and the allow-disarm opt-in,
  and saving without a variable name is rejected with 422.
- **Notification outputs actually notify now** — the class previously
  had no consumer. A notification output now emits a deliberate,
  per-area, mode-filtered `alarm_panel.notification` event at fire time
  (one-shot, never cancelled by silence): published as a NOTIFICATION
  entry on the area's MQTT alarm event topic, forwarded to outbound
  webhook receivers, and broadcast on the WebSocket alarm_panel topic
  (`alarm.notification`, APIVersion 2.26.0). Each plane can be toggled
  per output (`notify_mqtt` / `notify_webhook`, both default on) from
  the output card; the add dialog no longer demands a device for this
  class.
- **Alarm keyfobs surface first in the remote-key picker** — security
  remotes (HmIP-KRCA and peers) sort to the top of the guided binding
  picker and the KRCA carries an "alarm keyfob" badge; generic wall
  buttons and remotes follow.

### Fixed

- **Remote-key picker found no keys** — the candidate enumeration
  looked for press parameters in the channel's generic-event set,
  which the device pipeline never populates; every remote and
  wall-button key was invisible ("no remote or wall-button keys
  found"). Press parameters are ordinary VALUES data points, and the
  enumeration now checks exactly that. Pinned by a godevccu
  integration test that asserts the HmIP-KRCA surfaces with both
  press parameters.
- **Tone / pattern / soundfile dropdowns could miss their device
  lists** — the output card matched candidates strictly by
  central + channel address; enrollments whose stored central differs
  (older rows may carry an empty one) silently fell back to free-text
  fields. An unambiguous address-only fallback now covers those rows,
  and the ASIR/MP3P ENUM lists themselves are pinned end-to-end by a
  godevccu integration test.

## [0.43.0] — 2026-07-17

### Added

- **Capability-aware alarm output enrollment** — new REST endpoint
  `GET /api/v1/alarm/output-candidates` (APIVersion 2.25.0) derives,
  from the live domain model, which channels can back each
  device-backed output class (acoustic/optical siren, switched siren,
  smoke sounder, alarm light, chirp — including ON_TIME-gated
  switched-siren eligibility) plus the device's real ENUM label lists
  (siren tones, optical patterns, MP3 soundfiles). The SPA add-output
  dialog now lists these ground-truth candidates per class with a real
  channel picker (expert mode keeps the unfiltered device list), the
  tone / optical-pattern / chirp-tone editors offer the device's ENUM
  values instead of free text (e.g. HmIP-ASIR), and MP3-player chirp
  outputs (HmIP-MP3P) get a soundfile picker. Saving an output set now
  soft-validates enrollments: a resolvable channel that cannot carry
  its class is rejected with 422 instead of failing at fire time;
  unresolvable channels (CCU down) still save and remain guarded by
  the fault journal.
- **Guided remote-key bindings (keyfob arming, e.g. HmIP-KRCA)** —
  remote-key alarm codes no longer require a hand-written JSON binding:
  `GET /api/v1/alarm/remote-key-candidates` enumerates every physical
  remote/wall-button key channel (PRESS_SHORT / PRESS_LONG from the
  live model; virtual remotes excluded), and the codes editor gained a
  key picker plus trigger / action / area selects that assemble the
  binding document. Raw JSON remains available as an expert fallback
  (also the path for virtual remote channels).

## [0.42.9] — 2026-07-17

### Fixed

- **Untranslated climate parameters on HmIP-BWTH** — the parameter
  editor showed raw identifiers for `TEMPERATURE_COMFORT_COOLING`
  (the cooling-mode comfort temperature on climate transceivers) and
  `SUPPORTING_WIRED_OPERATION_MODE` (MAINTENANCE channel): the CCU's
  own stringtable never carried them, and the curated translation
  overlay had no gap-fill. Both now resolve in German and English
  ("Komfort-Temperatur (Kühl-Modus)" / "Comfort temperature (cooling
  mode)"), pinned by an overlay regression test.

## [0.42.8] — 2026-07-17

### Fixed

- **Firmware overview froze on the state from daemon start** — the
  daemon read each device's firmware data (installed / available
  version, update lifecycle state) exactly once, at device
  materialisation, possibly straight from the SQLite description cache.
  An update performed at the CCU never surfaced: the overview kept
  offering "Update" for firmware installed long ago, and the RPI-RF-MOD
  placeholder row persisted. Three gaps closed:
  - the periodic firmware polling jobs (`central.firmware_check` hourly,
    plus fast delivery/updating polls while an update transaction runs)
    existed as scheduler slots but were never wired — they run now, and
    the refresh propagates the fresh description fields onto the live
    device models (previously even the WS `firmware.refresh` command
    only updated an internal registry no surface reads).
  - new REST endpoint `POST /api/v1/devices/firmware/refresh`
    (APIVersion 2.24.0) forces the same sweep on demand.
  - the firmware overview's "Reload" button now actually reloads: it
    triggers the daemon-side refresh and re-fetches the per-device
    firmware details, which were previously served from a never-
    invalidated page cache — the button visibly did nothing.
- **Reload/interval audit follow-ups** — a sweep over every reload
  button and periodic-refresh job after the firmware finding:
  - the system-variables page's "Reload" now forces a CCU re-pull
    (`POST /sysvars/fetch`, which existed but was never called by the
    SPA) before reading the list — previously a value just changed at
    the CCU stayed invisible for up to one sysvar-scan interval.
  - the overview's error-retry drops its per-device tile caches and
    re-fetches expanded groups instead of serving the session's first
    detail/CDP snapshots forever.
  - the `hub.metrics_refresh` job is no longer scheduled: its inner
    hook was never wired anywhere, so it fired every 5 minutes as a
    permanent no-op (both existing hub metrics are produced by other
    jobs). All other reload buttons and refresh jobs verified working.

## [0.42.7] — 2026-07-17

### Fixed

- **LIST sysvar published its raw index instead of the label** — the
  CCU delivers every sysvar value as a string, and the scan kept LIST
  (and numeric) sysvar values string-typed instead of parsing them to
  the declared type. The MQTT state topic therefore carried the raw
  index (`0`) while the discovery advertised the labels as enum
  options, so Home Assistant rejected every update ("got '0', allowed:
  Aus, …") and the sensor stayed unknown. LIST values now parse to the
  integer index (resolved to its label on publish), INTEGER to int,
  NUMBER/FLOAT to float; REST/WS mirror the correctly typed value. A
  non-numeric payload still degrades to the string fallback.

## [0.42.6] — 2026-07-17

### Added

- **`alarm.v1` capability token** (#357) — `GET /api/v1/info` now
  advertises `alarm.v1` in `capabilities` whenever the daemon-level
  alarm service is mounted (the same condition that mounts the
  `/alarm` routes). External clients gate their alarm surface on the
  token instead of probing `GET /alarm/panels` for a 404, which cannot
  distinguish a disabled subsystem from an old daemon or a
  reverse-proxy misroute.
- **Per-area code policy on the panel entity** (#358) — the
  alarm-control-panel entity (`GET /alarm/panels`, WS
  `alarm_panel.panels`, and the `alarm.panel_changed` broadcast) now
  carries `code_arm_required` / `code_disarm_required`: the same
  effective per-verb code requirement the MQTT discovery already
  advertises (area code policy AND an applicable enabled pin code
  exists), so REST/WS consumers can prompt for a code upfront instead
  of surfacing the daemon's 403 after the fact. The master aggregate
  carries the any-area-requires union; live code CRUD re-derives the
  flags and pushes changed panels over the broadcast. APIVersion
  2.23.0.

## [0.42.5] — 2026-07-16

### Added

- **Hot-plug: newly paired devices appear without a restart** — a device
  taught in at the CCU while the daemon is running is now materialised
  live from the `newDevices` callback: the device pipeline hydrates
  exactly the new device (paramset descriptions, data points, custom
  data points, initial values, CCU-assigned name via a forced
  device-details refresh), MQTT publishes its discovery + state as soon
  as the model announces it, the Matter bridge reassembles its bridged
  endpoints (debounced, with the PartsList change notified to
  commissioners), and the WebSocket device-lifecycle broadcast continues
  to fire. The ingest dedups against the model and is serialised with
  the interface bring-up, so the CCU's full-inventory re-announcement
  after every reconnect stays a no-op instead of re-reading the whole
  interface. Previously the callback only updated internal registries —
  the device stayed invisible in REST/SPA/MQTT/Matter until the daemon
  restarted. Applies to the XML-RPC interfaces and CUxD (BIN-RPC) alike.

### Fixed

- **All-zero firmware placeholder rendered as an update** — devices the
  CCU has no OTA image for (e.g. the RPI-RF-MOD gateway module, which
  is updated through the CCU firmware itself) report the placeholder
  available version `0.0.0`. The firmware overview treated any version
  difference as a pending update and showed "Update available" /
  "Awaiting transfer to the device" next to 4.4.22 → 0.0.0. The
  placeholder now counts as "no available version": the row reads "Up
  to date", the available column shows "—", and the updates filter +
  summary no longer count the device. The domain gate
  (`GatedLatestFirmware`) applies the same rule, so a `0.0.0` can never
  surface as an installable target on the BidCos path or in the MQTT
  update entity either.

## [0.42.4] — 2026-07-16

### Fixed

- **Firmware overview contradicted itself** — a device whose CCU knows
  a newer firmware that is not yet delivered to the device (HmIP
  `NEW_FIRMWARE_AVAILABLE`, e.g. 1.2.2 installed / 1.4.10 available)
  showed "Up to date" twice, because the status column let the gated
  `update_available` flag (which only says an install can start *now*)
  overrule the real CCU lifecycle state. The status column now renders
  the CCU state ("Update available"), the action column explains
  "Awaiting transfer to the device" instead of claiming currency, and
  the updates filter + summary count include such devices.

## [0.42.3] — 2026-07-16

### Added

- **SPA↔contract path guard** — a new vitest contract test parses every
  URL the REST client constructs and matches it (method + path shape)
  against the OpenAPI contract via the generated types. A client call
  against a path or method the daemon never serves now fails CI instead
  of failing the user at runtime — the class behind the 0.42.2
  link-paramset 404. A full audit of all 184 client calls against the
  217 contract operations found no further mismatches; the existing
  router↔spec walk test already pins the other edge in both directions.

### Fixed

- **Install mode never opened** — `POST /api/v1/install-mode/interfaces`
  (and the per-device teach-in route) failed on every attempt: the
  install-mode writer looked its backend up under the bare interface
  type (`HmIP-RF`) while the registry keys backends by the canonical
  central-prefixed wire ID. The writer now translates to the wire ID;
  the unit-test fixtures register backends exactly like production
  wiring so this class of key mismatch can no longer hide.
- **Log viewer showed `"error": {}`** — the SPA log viewer and
  diagnostic captures passed error attributes through as raw values,
  which marshal to an empty JSON object, hiding the failure reason for
  every logged error (the install-mode failure above was undiagnosable
  from the UI). Error attrs now render as their message string,
  matching the stdout log.

## [0.42.2] — 2026-07-16

### Fixed

- **Link paramset saves from the SPA failed with 404** — the channel
  editor called `PUT /devices/{addr}/link-paramsets/{peer}`, a path the
  API never served (the contract route is
  `/devices/{addr}/link-ps/{peer}`), so every direct-link configuration
  save failed with "resource not found". The client now uses the
  contract path.
- **Percent-encoded path IDs are now decoded centrally** — the REST
  router routes on the percent-decoded path, so every `chi.URLParam`
  yields decoded values. Previously chi handed handlers the raw
  segment whenever a client percent-encoded a path ID (as the SPA does
  for every ID), which broke any endpoint whose IDs carry `:`, `|`,
  `@`, spaces, or non-ASCII — the class behind the custom-DP invoke
  fix, the 0.42.1 alarm test-fire fix, and the link-paramset lookup,
  and still latent in sysvar/room/function/program routes. The two
  per-handler workarounds are retired in favour of the router-level
  guarantee, pinned by a routing contract test.

## [0.42.1] — 2026-07-16

### Added

- **Alarm sensor hold time** — `hold_time` on a sensor now works: an
  activation must persist that many seconds before it counts; clearing
  earlier discards it. Filters twitchy PIRs and doors rattling in wind.
  Never applied to always-on hazard/panic sensors, so a smoke or panic
  alarm is never delayed.
- **Alarm cross-zoning groups** — the sensor `group` field now works:
  sensors sharing a group name only trigger when a second distinct
  member activates within 60 seconds; a lone activation is suppressed
  but journaled (`cross_zone_first_hit`). Kills single-PIR false
  alarms. Both windows are deliberately not restart-persisted
  (seconds-short).
- **Silent-panic flag in the sensor drawer** — panic-class sensors can
  now be marked as silent panic (duress) from the UI; the engine
  support existed but had no editor surface.

- **Alarm SPA help texts** — every alarm tab now opens with a short
  orientation line (what the view controls, how it relates to the other
  tabs), and the complex editors grew inline explanations: the Policies
  page explains every switch (code requirements incl. the automatic
  disarm-code rule, hazard/panic always-on semantics, pre-alarm,
  post-trigger/auto re-arm, schedules), the sensor detail drawer explains
  every behaviour flag, the output cards explain duration/tone/outdoor/
  shared-with-CCU, and the add-output drawer describes the selected
  output class. All texts in both locales. The previously missing
  `alarm.flag.chime` label (the door-chime switch rendered its raw i18n
  key) is fixed.
- **Alarm operator guide** — new `docs/alarm-user-guide.md`: an
  operator-facing walkthrough of the whole alarm system (concepts,
  safety promise, wizard, every tab, every policy and sensor flag,
  integrations), linked from the user guide.

### Fixed

- **Alarm output test fire 404** — `POST /api/v1/alarm/outputs/{id}/test`
  returned `404 Unknown alarm resource` for every output when the client
  percent-encoded the ID's `|`/`:` separators (as the SPA does), because
  the handler compared the still-encoded path segment against the
  enrolled output IDs. The ID is now URL-decoded before the lookup, so
  the SPA's siren/optical test buttons work again.
- **Alarm output editor wrote fields the engine never read** — the
  siren tone field saved `tone` while the driver reads `acoustic_tone`
  (the configured tone silently never played), the chirp card offered a
  single `chirp_chime_tone` the driver does not know (it reads
  `chirp_arm_tone` / `chirp_disarm_tone` / `chirp_tick_tone` — the card
  now exposes exactly those three), and the dimmer-level input accepted
  0–100 while the wire expects 0–1 (now 0–1 with the add-default fixed
  from 100 to 1). Legacy values saved under the old keys are read as
  fallbacks and migrated on the next save.
- **Per-output loud/silent toggle removed** — it wrote a `policy` field
  no engine path reads. Loud vs. silent is a property of the mode /
  hazard / panic output policies (Policies tab), where it already
  works; the dead toggle only suggested a control that did nothing.

## [0.42.0] — 2026-07-15

### Added

- **Native alarm system ("Alarmanlage")** — a complete, local-first
  intrusion-alarm engine inside the daemon, spanning six increments
  (#344–#349) on the concept in `docs/alarm-concept.md` (#343):
  - **Engine & safety core**: per-area arm-state machines (perimeter /
    full / night / vacation / custom) with real entry delays,
    force/bypass arming, bounded re-trigger cycles, and the full
    restart-restore semantics incl. a restart-loop breaker and a
    clock-plausibility rule. The seven hard safety invariants (S1–S7)
    are contract-tested: every siren activation is finitely bounded and
    budgeted per incident, every stop is verified with critical
    priority (and may probe an open circuit breaker), silence is
    persisted incident-scoped across restarts, reconciliation adopts
    sounding sirens before stopping them, and every degradation is
    journaled instead of swallowed.
  - **Output drivers** for HmIP sirens (ASIR family), plug-in sirens on
    switch/dimmer actuators (device-side auto-off travels with the
    switch-on), smoke-detector sounders (engine-watchdog bounded, with
    battery/group-fan-out caveats), alarm lights, chirps/countdown
    ticks, and an optional CCU sysvar mirror (inbound intents arm-only
    by default). Researched device assumptions in
    `docs/alarm-assumptions.md`.
  - **Surfaces**: REST namespace `/api/v1/alarm` and WebSocket category
    `alarm_panel` (APIVersion 2.22.0), a full SPA section (panel with
    single-tap silence, sensor picker with mode matrix, output
    management, journal, walk test, setup wizard — de/en, all theme
    combinations), Home Assistant MQTT discovery as
    `alarm_control_panel` entities (per area + aggregate master panel,
    daemon-level topics per ADR 0052), and an `hmcli alarm` break-glass
    group. The panel is a first-class model entity so REST, WS, and
    MQTT can never diverge.
  - **Codes & identities**: argon2id-hashed PIN codes with permissions,
    area restrictions, validity windows, rate limiting and lockout;
    duress codes with silent fan-out; per-area code policies reflected
    in the HA discovery (`REMOTE_CODE` command template); keypad
    (HmIP-WKP) and remote (KRCA/KRC4) intents with on-device PIN slots
    kept independent by design.
  - **Always-on hazard & panic classes**, pre-alarm stage, auto-rearm
    after quiet period, door chime, arm schedules with reminders (or
    explicit opt-in auto-arm), and alarm events on the webhook plane
    for user-land escalation chains.

### Changed

- Critical-priority commands may now probe an OPEN circuit breaker once
  (alarm stop path); non-critical traffic keeps the fail-fast shed.
- Repeated keypad press events (`PRESS_*`, `CODE_ID`) are no longer
  suppressed as unchanged values — edge-trigger parameters always
  publish.

## [0.41.0] — 2026-07-14

### Added

- **OIDC role mapping now works with your identity provider's real roles.** The
  `role_claim` setting is finally honored: it reads the configured claim from
  the ID token — a plain string, a string array, or a dotted path into a nested
  object such as Keycloak's `realm_access.roles` — and grants the highest of
  `admin` / `operator` / `viewer` it finds. Previously the field was ignored and
  only a single hardcoded top-level `role` string mapped, so Keycloak realm-role
  and group users all fell back to `viewer`. A new
  [Keycloak setup guide](docs/admin/keycloak-oidc.md) walks through the client,
  redirect URI, and role mapping.

### Security

- **OIDC no longer runs over cleartext.** The identity-provider issuer and every
  endpoint discovered from it must use https (plain http is allowed only on
  localhost). A misconfigured `http://` issuer used to run the whole login —
  including the code exchange and ID-token retrieval — in the clear; such a
  deployment is now refused. Move the issuer to https.
- **Stricter ID-token validation.** The discovery document's own `issuer` must
  equal the configured issuer (RFC 8414 §3.3), and the `azp` (authorized-party)
  claim is now checked — a present `azp` must name this client, and a
  multi-audience token must carry it (OIDC Core §3.1.3.7).

## [0.40.0] — 2026-07-13

### Changed

- **The HA-native visual skin (0.39.0) now covers the whole Config UI, not
  just the primitives and highest-traffic views.** `html[data-skin="ha"]`
  remaps Tailwind's `--color-*` palette scale (the CSS variables every plain
  `bg-slate-500`-style utility resolves through), so every remaining view
  follows the active skin automatically — no per-file sweep needed. Opacity-
  modified utilities (`bg-slate-900/50`) were rewritten to `color-mix()`
  against the same variables since Tailwind inlines those as literals. The
  HA-skin default values were also refreshed to the latest
  `home-assistant/frontend` design tokens (primary `#009ac7`, flat
  shadow-less cards, Roboto body font, refreshed neutral/semantic ramps).
  `data-skin="loom"` (the standalone default) is unaffected. See
  [`docs/design/ha-theme-bridge.md`](docs/design/ha-theme-bridge.md) §
  "Complete theme coverage via palette remap".

### Fixed

- **OpenCCU-Loom no longer exhausts the CCU's login-session pool.** The daemon
  discarded its CCU session roughly every 90 seconds and opened a fresh one,
  without releasing the old one — on the order of 40 abandoned sessions per
  hour per CCU. Because the CCU's session pool is shared with its WebUI, the
  leak eventually locked operators out of their own CCU with "invalid
  credentials or too many sessions". The cause was a wire-contract mismatch:
  the CCU answers `Session.renew` with the boolean `true` (it extends the
  session in place and does *not* issue a new ID), but the client decoded the
  reply as a session-ID string. Every renewal therefore failed to parse, was
  treated as a dead session, and triggered a re-login. A long-running daemon
  now holds exactly **one** CCU session for its entire life, pinned by a
  contract test.

- **Backup and firmware downloads no longer displace the CCU session.** Both
  authenticate against `cp_security.cgi` by session ID and asked the shared
  JSON-RPC client for a *forced fresh login* first, abandoning the session the
  rest of the central was working with — one burned CCU session slot per
  download. They now renew the existing session instead, which is what the
  code's own comment always said it did.

- **A session the daemon abandons is now handed back to the CCU.** When a
  session really does have to be replaced (CCU reboot, expired session,
  privilege error), the client issues `Session.logout` for the old one instead
  of leaving it to idle out of the pool.

- **A rejected login now engages the existing backoff.** The CCU reports both
  wrong credentials and an exhausted session pool as a JSON-RPC error rather
  than an empty result, which slipped past the backoff and made the daemon
  retry at full speed — turning an exhausted pool into a self-sustaining retry
  storm exactly when it needed to back off.

## [0.39.0] — 2026-07-12

### Added

- **The Config UI has a Home-Assistant-native visual skin.** Settings →
  Appearance gains a "Design" control offering the default OpenCCU-Loom
  teal/slate look or a Home Assistant look. Opened standalone (browser tab,
  not inside HA), the operator's choice is remembered and applied on every
  load. Opened inside Home Assistant via Ingress, the HA skin is applied
  automatically and mirrors the operator's real, live HA theme — including
  any custom theme, not just the built-in ones — via a same-origin bridge
  that reads Home Assistant's own color and light/dark settings and re-syncs
  whenever they change. The sidebar, header, and navigation are identical in
  every context; only the color palette adapts, and the browser tab title is
  left to Home Assistant while embedded. See
  [`docs/design/ha-theme-bridge.md`](docs/design/ha-theme-bridge.md) for the
  design. The shared UI primitives and the highest-traffic views carry the
  new skin now; a handful of less-visited views are a documented follow-up.

- **A CCU's operational readiness is now visible in the UI.** Many actions
  depend on a CCU having finished its readiness-gated southbound bring-up
  (device list fully loaded, Matter coupling available), but that state was
  invisible — an initializing CCU looked "offline" and its half-loaded device
  list looked "empty". Each central now exposes an explicit readiness phase
  (`waiting_for_ccu` → `loading_hub` → `loading_devices` → `ready`, with a
  per-interface `x/y` progress count) as a new `readiness` object on
  `GET /api/v1/system/ccu` and live over the WebSocket topic
  `central.{name}.readiness`. Readiness is tracked **per central**, so a mixed
  fleet (one CCU ready, another still initializing) is represented faithfully.
- **The SPA surfaces readiness everywhere it matters.** A shared
  `CentralStatusBadge` renders the full state set (Ready / Waiting for CCU /
  Initializing *names* / Initializing *devices x/y* / Offline), so an
  initializing CCU is no longer indistinguishable from an offline one on the
  Fleet view. The device overview shows a "devices are still loading" state
  (plus a per-CCU banner in a mixed fleet) instead of a bare "no devices", and
  auto-refreshes the moment a CCU flips to ready. Matter pairing now explains
  when it is waiting for a CCU (a 503 is no longer collapsed into "disabled")
  and — per the "allow + hint" policy — stays available as soon as at least one
  CCU is ready, so a single stuck CCU never blocks pairing of a healthy one.

## [0.38.0] — 2026-07-12

### Fixed

- **English Home Assistant users saw German MQTT entity names.** Four
  HA-discovery entity names were authored (hardcoded) in the central event
  bridge rather than the MQTT layer, two of them German-only
  (`Zeitplan Kanal N`, `Zeitdauer`), so they leaked German to every locale.
  They now resolve from the i18n catalogues in the daemon's `locale`
  (schedule-switch, combined-level, HS-colour and combined-timer labels).

### Changed

- **All remaining daemon-authored translatable strings now go through the i18n
  catalogues (`internal/i18n/catalogs/<locale>.json`).** Adding a language is a
  single new catalogue file — no Go change. Migrated in this release:
  - The server-rendered `/health` and `/about` pages (headings, table headers,
    overall-status label, version/license/repository labels). The health status
    token stays the raw value for the CSS class; only the visible text is
    localized.
  - The Matter bridged-endpoint NodeLabel channel-number fallback
    (`Kanal N` → localized "Channel N" / "Kanal N").
  - The SPA's two inline-bilingual helper tables (daylight-saving headers and
    time-preset labels) now use the SPA `t()` catalogue instead of hand-rolled
    EN/DE branching.
  What is intentionally NOT localized (documented): REST/WS API contract
  messages (clients localize off the stable code), matter.js-mirrored spec
  names, and log messages (English by convention).
- **Health/diagnostics component notes are now localized.** `health.Sample`
  gains an additive `NoteKey` (i18n key) alongside the existing English `Note` —
  which stays the stable sentinel the health-scoring logic matches on, so
  scoring is unaffected. Static notes (client connected, breaker states,
  recovery, initial-sync) render localized on the SPA diagnostics view, the
  connectivity tooltip and the `/health` page; interpolated notes keep their
  English text. `GET /api/v1/health` components carry a new optional `note_key`
  field. REST API version bumps to **2.18.0** (additive).
- **MQTT-discovery entity names are now localized through the i18n catalogues
  instead of being hard-coded.** Every daemon-synthesised HA entity name — the
  hub entities (system health, connection latency, last-event age, alarm /
  service messages, inbox, system update, per-interface install-mode and
  connectivity) and the per-device schedule / firmware / week-profile
  entities — is resolved from `internal/i18n/catalogs/<locale>.json` in the
  daemon's configured `locale`. Previously these names were a mix of hard-coded
  English and German. Adding another language is now purely a new catalogue
  file; no code change. (Side effect: names follow `locale` now, so e.g. the
  schedule sensor reads "Zeitplan" only when `locale: de`.)
- **Auto-generated CCU counter system variables get a friendly, localized name
  in Home Assistant.** The CCU synthesises per-channel counter variables with
  machine-token names (`svEnergyCounter_<ise_id>_<addr>:<ch>`,
  `svHmIPRainCounter…`, `svHmIPSunshineCounter…` and their FeedIn / Today /
  Yesterday variants); MQTT discovery previously surfaced that raw token as both
  the entity name and the entity_id (e.g.
  `sensor.…_svenergycounter_14007_0001dbe9915be4_6`). They now render with the
  reference integration's friendly name — e.g. "Energiezähler Gesamt" /
  "Energy Counter Total" — plus the matching HA `device_class`, unit
  (Wh / mm / min) and a cumulative `total_increasing` state_class so energy
  counters feed long-term statistics. The stable `unique_id` is unchanged;
  entities discovered before this release keep their old entity_id until removed
  and rediscovered.

## [0.37.0] — 2026-07-12

### Fixed

- **Home Assistant showed every CCU device's parent as an "unknown device" and
  never surfaced system variables (nor their device assignments).** The entire
  hub-discovery plane (system variables, programs, the named central "hub"
  device, alarm/service messages, connectivity, install-mode) is gated on the
  CCU serial. Because the readiness-gated central bring-up resolves that serial
  asynchronously — after the composition root had already stamped the discovery
  builder with a still-empty serial — every hub discovery payload was silently
  skipped, including the only payloads that give the synthetic central device a
  name. Raw state topics (`…/hub/sysvars/<name>/state`, …) were unaffected,
  which is why the plane looked half-alive. The hub publisher now reads the
  serial live from the central's `SystemInformation` and re-publishes hub
  discovery on each central's `CentralSouthboundReadyEvent`, so the central
  device is named (no more "unknown device" parent) and system-variable /
  program entities — including their device links — appear once the CCU's
  serial resolves. No API surface change.

## [0.36.0] — 2026-07-12

### Added

- **System variables honour the CCU's explicit channel assignment and expose
  their device link over REST/WS.** The sysvar scan now reads the channel the
  operator assigned to a variable in the CCU WebUI ("Kanalzuordnung") and uses
  it as the primary device-link source — the existing name-based matching
  (channel address, channel/device `ise_id` in the variable name) remains the
  fallback, so e.g. the CCU's auto-generated
  `svEnergyCounter_<ise_id>_<ADDRESS>:<ch>` variables keep resolving. An
  assignment referencing a device the daemon does not serve falls back to name
  matching. Linked sysvars/programs appear under their physical device in Home
  Assistant both via MQTT discovery (as before) **and now via the REST/WS
  API**: `GET /api/v1/sysvars` / `GET /api/v1/programs` (and the WS
  `sysvars.list` / `programs.list` commands plus the `hub.sysvar_changed` /
  `hub.program_executed` broadcasts) carry new optional `channel` and
  `device_address` fields — absent fields mean the entity belongs on the
  central hub card. Assignment changes are logged
  (`hub.sysvar.channel_assigned`, source `explicit`/`name`/`none`) for field
  diagnosability. REST API version bumps to 2.17.0 (additive).

## [0.35.0] — 2026-07-11

Ingress and dark-mode fixes for the Home Assistant add-on deployment (device
icons were placeholder-only behind Ingress and glaring white in dark mode;
the "Login with OIDC" button 404'd behind Ingress), plus CCU system variables
and programs now linked to their physical device. No REST/WS API surface
changes.

### Added

- **System variables and programs are now linked to their device.** A CCU
  system variable (or program) whose name carries a device or channel
  identifier — the channel address, a channel `ise_id`, or the device
  `ise_id` — is associated with that device, mirroring aiohomematic's
  `channel_lookup.identify_channel`. The identifier must appear as a
  standalone token (bounded by non-word characters), so an `ise_id` of `123`
  no longer matches inside a larger number such as `41234`. In Home Assistant
  MQTT discovery the linked sysvar/program entity now renders on the physical
  device's card instead of the synthetic central hub card. The association is
  a device-population-driven pass: it is (re)established after every interface
  ingest and after each periodic sysvar/program refresh, and is cleared again
  when the referenced device disappears.

### Fixed

- **CCU device icons now load behind Home Assistant Ingress.** The
  device-list card built its icon URL as a hard-coded `/api/v1/…` path,
  which bypasses the Ingress proxy prefix and hits the Home Assistant
  origin — so every icon 404'd and fell back to the generic glyph when the
  daemon ran as an HA add-on (it worked untouched as a CCU add-on, where no
  prefix applies). The URL now carries the Ingress prefix (`apiBase()`),
  like every other REST call. The icon proxy additionally sends the
  central's credentials, so icons also resolve against a CCU with
  authentication enabled when reached off-box.
- **The "Login with OIDC" button works behind Home Assistant Ingress.** The
  login page linked the OIDC start endpoint with a hard-coded `/api/v1/…`
  path, which bypasses the Ingress proxy prefix and hits the Home Assistant
  origin (404) when the daemon runs as an HA add-on. The link now carries the
  Ingress prefix via `apiBase()`, like every other REST URL, so the SSO flow
  is reachable. (Completing the round-trip still requires the operator's
  registered OIDC `redirect_uri` to point at a daemon URL the browser can
  reach — an inherent Ingress/OIDC deployment constraint the SPA cannot
  derive, since Ingress strips its prefix server-side.)
- **Device icons are no longer glaring white tiles in dark mode.** The CCU
  model artwork is monochrome line-art (some ships transparent, some with a
  baked-in white background) and sat on a permanent white plate. In dark
  mode the grayscale art is now inverted and the plate goes transparent, so
  icons sit cleanly on the dark card instead of as bright white squares.

## [0.34.0] — 2026-07-11

SPA design & UX overhaul driven by a structured UI review, plus one backend
data-quality fix. No REST/WS API surface changes.

### Fixed

- **RSSI no-signal sentinels no longer surface as measurements.** The
  ReGa-script boot seed decodes JSON numbers as floats, which bypassed the
  RSSI normalisation (it only handled integer wire types) — devices could
  show "RSSI 128 dBm" until the first radio event. The SQLite value-cache
  restore path had the same gap. All numeric wire types now funnel through
  the sentinel filter, and the SPA additionally masks out-of-range RSSI
  values defensively.
- **Dark mode no longer flashes/strands a white top bar.** The `.dark` class
  was applied after first paint; a pre-paint script in `index.html` now sets
  it synchronously, the header carries an explicit token background, and
  `html`/`body` base colours track the theme tokens.
- **Seven previously undefined `brand-*` colour rungs.** The Tailwind theme
  declared only 4 of the 11 brand shades used in markup; utilities like
  `dark:text-brand-100` silently produced no CSS (unreadable active states
  in the settings navigation, washed-out accents). The scale is now complete.
- **Disabled buttons are recognisably disabled in dark mode** (explicit
  disabled fills instead of opacity-only dimming).

### Changed

- **Brand palette switched from generic blue to the logo's Loom teal** —
  full `brand-50…950` scale derived from Tailwind teal, contrast-shifted so
  white button labels meet WCAG AA (4.5:1); info tones stay blue on purpose.
- **Login, loading and empty surfaces carry a subtle woven signature**
  (reusable `WeavePattern` SVG, loom-thread loader; `prefers-reduced-motion`
  respected), and page titles use a tighter display treatment.
- **Overview tiles**: grid rows no longer stretch short tiles to the tallest
  neighbour; widgets without observed state and without operable controls
  collapse to a compact single-row tile ("No state received yet").
- **Matter exposure table**: rows are grouped per device with a header
  (name + address + row count), platform-dependent emoji state glyphs are
  replaced by registry icons with a legend, disabled checkboxes explain why,
  and the redundant selection column header is gone.
- **Device views**: product photos sit in a neutral tile (no more white
  boxes in dark mode), "Remove device" is a red outline action instead of
  the loudest button in the header, and "CCU Refresh" became a secondary
  "Reload from CCU" action.
- **Channel configurator**: the sub-navigation is a quiet segmented control
  (the active top tab is the only brand-marked nav level), channel tabs are
  visually subordinated, duplicate parameter labels within a group are
  disambiguated (upper/lower threshold qualifiers), and long labels wrap.
- **Settings editor**: value-source dots have tooltips + screen-reader
  labels (bootstrap source is violet now), Go type badges only show in
  expert mode, booleans use the design-system switch, and section panels
  keep a single primary action ("Save").
- **Connection badge**: the disconnected state is amber (red stays reserved
  for real errors) and all states explain themselves via tooltip, including
  the mobile dot-only variant.
- **Empty states** across messages, audit log, signal quality and fleet
  views gained explanatory descriptions; "Un-Ignore" is now called
  "Hidden parameters" / "Ausgeblendete Parameter".
- **Theme can be set in Settings → Interface** (light/dark/system, same
  preference the sidebar toggle cycles).

## [0.33.0] — 2026-07-11

Matter interop hardening. The changes below adopt verified findings from a
systematic comparison against the
[home-assistant-matter-hub](https://github.com/RiDDiX/home-assistant-matter-hub)
project's field experience, with every mechanism mirrored from matter.js HEAD
(not from the compared project).

### Fixed

- **Matter endpoint numbers now genuinely survive daemon restarts.** The
  boot-time topology assembly ran before the readiness-gated CCU device load and
  garbage-collected every persisted endpoint-ID row of a still-loading central —
  on every boot. Endpoint numbers stayed stable only by accidental re-allocation
  order, so any fleet change across a restart renumbered endpoints and broke
  controller-side groups/automations. Vanished-device GC is now gated on the
  per-central `ModelComplete` readiness latch; endpoint numbers stay reserved
  until a device is genuinely removed (mirroring matter.js
  `ServerEndpointStores` number reservation, Matter §9.12.4).
- **One GenericSwitch endpoint per physical button.** All press parameters of a
  button channel (PRESS_SHORT / PRESS_LONG / PRESS_CONT / PRESS_LONG_RELEASE)
  now share a single endpoint with a spec-conformant press-cycle state machine
  (InitialPress → LongPress → LongRelease). Previously each press parameter
  materialised its own endpoint, so long-press releases landed on a different
  cluster than the long press (orphaned by construction) and a held BidCos
  button emitted a LongPress event every ~300 ms. Also fixes press events never
  reaching commissioners from real devices (a dead wiring type-assertion) and
  duplicate events after topology reassembly. **Button devices receive new
  Matter endpoint numbers; Apple/Google re-learn button accessories once after
  the update.**
- **WindowCovering `EndProductType` reports enum-correct values** per cover
  profile — Curtain→CentralCurtain(16), Shutter/Window→RollerShutter(17),
  Awning→AwningTerracePatio(19), Blind→InteriorBlind(10), Garage→RollerShade(0)
  — instead of wrongly reusing the `Type` enum code (which read as
  PleatedShade/LayeredShade/CellularShade/SheerShade) or a fabricated
  GarageDoor value. Controllers cache Type/EndProductType at commissioning, so
  already-paired bridges show the corrected values after a re-sync or re-pair.
- **WindowCovering target/direction for externally started movement.** When a
  cover moves via wall button or CCU program, TargetPosition is now inferred
  from the CCU-reported motion instead of staying stale, a device-side stop
  snaps the target back to the current position, Target attributes are
  proactively reported, and the motion parameter (DIRECTION / ACTIVITY_STATE,
  including the previously unwired HmIP fallback) fires Matter reports — Apple
  Home now shows the correct direction arrow for wall-button movement.
- **Thermostat SystemMode/RunningMode conformance.** HM AUTO (week-program)
  mode no longer reads back as `SystemMode=Auto(1)` on a FeatureMap without the
  AUTO feature — controllers echoing that read value on state sync received
  ConstraintError. Reads clamp to Heat/Cool per the active HEATING_COOLING
  direction, RunningMode uses the Auto-less enum, and cooling-capable profiles
  advertise COOL without AUTO (Matter AutoMode requires dual setpoints +
  MinSetpointDeadBand, which HM single-setpoint devices cannot provide).
- **Controllers no longer see the bridge as unresponsive for minutes after a
  restart.** The bridge now sends a Secure-Channel CloseSession StatusReport
  before dropping a session (stale same-peer CASE eviction, idle-session reap
  — operational sessions without traffic for 5 minutes are evicted on a 60 s
  sweep; controller acks on subscription heartbeats keep live sessions marked
  active — and daemon shutdown, capped at 2.5 s), closes a session immediately
  when the peer sends CloseSession (with subscription cleanup), and resumes
  mDNS broadcast once a peer has no remaining session. Mirrors matter.js
  ExchangeManager / SecureChannelProtocol behaviour.
- **`BasicInformation.SoftwareVersion` is derived from the build version**
  (`major*1_000_000 + minor*1_000 + patch`) instead of a hard-coded `1`,
  keeping it consistent with `SoftwareVersionString` — a divergent pair crashed
  some ecosystem hubs (e.g. Aqara) during bridge synchronisation.
- **`ElectricalEnergyMeasurement.CumulativeEnergyImported` is wire-valid.**
  The attribute was emitted as a bare int64 instead of the spec's
  `EnergyMeasurementStruct` (Matter §2.14.5.2) — typed decoders (chip-tool and
  potentially ecosystem controllers) rejected a plain read with "Wrong TLV
  type"; only the untyped report dump masked it. The value now encodes as a
  struct carrying the mandatory `Energy` field.
- **Matter endpoint readiness is race-free and covers runtime-adopted CCUs.**
  The per-central readiness latch is now queryable and seeded at subscribe
  time (a CCU whose device load finished before the Matter bridge subscribed
  was previously never latched), and centrals adopted at runtime get the same
  readiness + reassemble-on-ready wiring as boot-time centrals.

### Changed

- **Matter schema refreshed to matter.js HEAD (spec 1.5.1).** Cluster
  revisions bumped (AccessControl 3, BasicInformation 6,
  BridgedDeviceBasicInformation 6, GeneralDiagnostics 3, GroupKeyManagement 3,
  BooleanState 3, SmokeCoAlarm 2, Thermostat 11, OccupancySensing 7, and the
  measurement/concentration clusters +1 each), device-type revisions updated
  (Thermostat 6; WaterLeakDetector, WaterFreezeDetector and RainSensor 2), and
  the new spec-1.5.1 read-only attributes were added to the write gate.

- **Leak-class sensors will materialise as ContactSensor (0x0015), not
  WaterLeakDetector (0x0043).** Amazon Alexa is pinned below the Matter-1.3
  detector device types; field evidence shows a single 0x0043 endpoint renders
  an entire Alexa bridge unresponsive. The wire surface is identical (mandatory
  BooleanState), polarity stays non-inverted (leak detected → StateValue=true).
  No classifier emits the leak class yet, so this lands before any moisture
  parameter is wired.
- **WindowCovering slider gestures are debounced.** `GoToLiftPercentage` /
  `GoToTiltPercentage` commands debounce per axis (400 ms gesture-start /
  150 ms active-drag; commands within 1 % of the current position are
  acknowledged without a radio write), so Apple/Google slider drags send one
  settled position to duty-cycle-limited HmIP actuators instead of 5–10
  stuttering intermediate writes. The commanded TargetPosition updates
  immediately; StopMotion / UpOrOpen / DownOrClose cancel pending writes.

### Added

- **Dimmer transitions.** LevelControl `MoveToLevel` / `MoveToLevelWithOnOff`
  now honour `TransitionTime`: a positive value is delegated to the device as
  `RAMP_TIME` in one atomic paramset write (a WithOnOff min-level target ramps
  off via ramp-time off). Null/0 keep the instant path; devices without
  `RAMP_TIME` support fall back to instant.
- **Regression guard for Google Home's LevelControl wire shape.** Google omits
  the `transitionTime` field entirely (not TLV-null); the lenient decode of the
  absent field is now pinned by tests so a future strict-validation pass cannot
  silently break Google Home dimming.
- **Controller-ecosystem caveats documentation** (`docs/user/matter.md`): Alexa
  commissions only on UDP port 5540 (also noted in the `north.matter.listen`
  config help, en+de), Alexa's ~80–100 device cap per bridge, detector device
  types breaking Alexa bridges, and WaterValve being unsupported on Google
  Home/Alexa.

## [0.32.0] — 2026-07-10

### Fixed

- **Power/energy meters on switching devices now push live readings to Matter.**
  A HmIP-BSM (and similar metering switches) reports its POWER / ENERGY_COUNTER
  from a sibling meter channel that the bridge attaches onto the switch's Matter
  endpoint. The endpoint's change-notifier only covered the OnOff cluster, so a
  power/energy change was never pushed to a subscribed controller — the value was
  correct on a read but updated only on demand. The ElectricalPowerMeasurement /
  ElectricalEnergyMeasurement clusters now forward their source's notifier, so a
  meter reading change reaches Apple/Google Home as a proactive report.
- **Matter event subscriptions now establish and deliver events.** A subscription
  naming only events (e.g. a GenericSwitch button press — a momentary control with
  no persisted attribute) was rejected when no matching event had been recorded
  yet. Since a button subscription is necessarily placed before the press, it
  could never receive one. Event-only subscriptions now establish on the requested
  event paths and stay up to deliver future events, matching the reference Matter
  stack.

## [0.31.0] — 2026-07-10

### Added

- **Matter now bridges `valve.Irrigation` as an on/off endpoint.** The
  irrigation valve's primary channel projects its `STATE` onto OnOff /
  OnOffPlugInUnit like any other switch — resolving the ADR 0012 "stays
  MQTT-only" gap. Its redundant group-STATE transmitter and sibling actor
  channels stay folded so the device presents as one Matter endpoint by
  default. See [ADR 0049](docs/adr/0049-matter-one-endpoint-per-device.md).
- **Expert flag `north.matter.expose_secondary_channels`** (default off). When
  enabled, the Matter projection additionally surfaces a custom entity's
  secondary actor channels and its group-STATE channel as their own endpoints,
  for power users who want per-channel control. Matter-only — MQTT,
  HA-Discovery and REST are unaffected.

### Changed

- **The Matter exposure candidate list only shows real entities now.** The
  `GET /api/v1/matter/exposable` collector now applies the same visibility gate
  MQTT / HA / REST use: service, status-validity and overflow parameters
  (`INSTALL_TEST`, `*_STATUS`, `*_OVERFLOW`, `PROCESS`, `CONFIG_PENDING`, …,
  all usage `ignored`) and the raw constituents an aggregating custom entity
  consumes (usage `no_create`) no longer clutter the exposure UI as
  "unmappable" rows. A candidate is now a data point that would be a standalone
  entity elsewhere; un-ignoring a parameter makes it reappear automatically.

### Fixed

- **Matter commissioning no longer spins after CommissioningComplete.** When a
  commissioner (Apple Home, Google Home, chip-tool) finished pairing, the
  commissioning-window auto-close routed through `RevokeWindow`, which disarmed
  the fail-safe via `ArmFailSafeFor(…, 0, …)`. That path treated a zero-second
  arm as "arm for 0 seconds" — immediately expired — so it spawned an expiry
  watcher that fired the expiry hook, which itself calls `RevokeWindow`: an
  unbounded loop that pegged a CPU core and flooded the log. The bridge stopped
  answering the commissioner's post-commissioning reads, so the pairing aborted
  ("could not add accessory"). A zero-second `ArmFailSafeFor` is now a pure
  disarm (no watcher, no expiry hook), matching the cluster-wire disarm
  semantics. Verified end-to-end against a real Apple Home commissioner.
- **Matter thermostat setpoint / mode writes from a controller now take
  effect.** The Climate Thermostat cluster asserted `value.(int16)` /
  `value.(uint8)` on writes, but the bridge's TLV decoder delivers write values
  as `int64` / `uint64`, so every Apple/Google `OccupiedHeatingSetpoint` or
  `SystemMode` write failed with IM status `Failure`. The handler now coerces via
  `cluster.AsInt16` / `cluster.AsUint8` (matching every other cluster), so the
  setpoint and mode actually reach the CCU.
- **External CCU changes now propagate to Matter controllers for every bridged
  device class.** Only generic sensors/switches implemented the
  `MatterChangeNotifier`, so a change made at the wall switch or by a CCU program
  never reached a controller's Subscribe for custom-DP-backed accessories
  (dimmers, thermostats, covers, locks, sirens) — they reflected only the
  commands the controller itself sent. `generic.Float` and every custom endpoint
  class now implement `OnMatterValueChanged`, and a source-walking contract test
  guarantees no future device type reopens the gap. Verified end-to-end against
  Apple Home.
- **Read-only ENUM state now reaches Matter controllers for every affected
  device class.** A custom data point that read a read-only ENUM wire parameter
  through a string-sensor accessor got `nil`: the resolver projects read-only
  ENUM parameters onto a raw-index integer sensor, which the `*Sensor[string]`
  cast never matched, so the projected value stayed unobserved and the Matter
  attribute reported TLV-null forever. This silently disabled DoorLock
  `LockState` (`LOCK_STATE`) and SmokeCOAlarm `SmokeState` / `ExpressedState`
  (`SMOKE_DETECTOR_ALARM_STATUS`) in Apple / Google Home, plus the door lock's
  `DIRECTION`, the RF lock's `ERROR`, the siren sound player's `SOUNDFILE` /
  `DIRECTION`, the fixed-colour light's active `CHANNEL_COLOR` slot, and the
  garage door's `DOOR_STATE` (so a garage never reported a position). The custom
  DPs now read the index sensor and resolve the VALUE_LIST label on demand, and a
  resolver-boundary contract test pins the "read-only ENUM → index sensor"
  invariant so the accessor mismatch cannot recur.
- **Docker image builds again.** The builder stage pinned `golang:1.26.4-alpine`
  while `go.mod` requires Go 1.26.5, so with `GOTOOLCHAIN=local` the image build
  failed at `go mod download` (`requires go >= 1.26.5`). The builder base image is
  bumped to `golang:1.26.5-alpine`, matching `go.mod` and the CI `GO_VERSION`.
- **Matter bridge survives boot-time database contention with a large device
  fleet.** Endpoint-ID assignment runs a read-then-write SQLite transaction;
  under WAL a concurrent boot writer (the device-load pipeline) could fail the
  upgrade with `SQLITE_BUSY` — a case `busy_timeout` does not cover — so the
  bridge logged `matter.bridge.start … database is locked` and never brought up
  its Matter listener. The assignment now retries on a BUSY/locked error with a
  bounded backoff, so bring-up no longer fails when many devices are assembled
  at once.
- **A structurally-incomplete device no longer crashes the Matter exposable
  list.** Enumerating exposure candidates calls promoted accessors on each
  device; a custom device with a nil embedded data point (e.g. a colour light
  whose LEVEL DP never materialised) panicked `GET /api/v1/matter/exposable`
  with a `500`, which in turn left the Matter bridge with no bridged endpoints.
  Candidate collection now isolates each device — a panicking one is logged and
  skipped — so every healthy device still surfaces on the exposable list and in
  the bridge.

## [0.30.0] — 2026-07-09

### Fixed

- **HmIP climate away/party mode now writes the reference wire parameters.**
  `SetAway` on an IP thermostat wrote `PARTY_TEMPERATURE` and a `dd.mm.yy hh:mm`
  time format that the CCU did not honour; it now writes `SET_POINT_MODE=2`,
  `SET_POINT_TEMPERATURE`, and the `PARTY_TIME_START/END` window in the CCU's
  `yyyy_mm_dd hh:mm` format, matching aiohomematic — so the away setpoint and end
  time actually take effect.
- **`PUT /devices/.../value` reports client mistakes as `400`, not `502`.** A
  value rejected by validation (read-only parameter, out-of-range, wrong type,
  string too long) never reaches the CCU, but was being surfaced as a `502`
  upstream failure. Such rejections now return `400` (validation); genuine
  upstream failures still return `502`.
- **SPECIAL values bypass MIN/MAX consistently.** A parameter value equal to a
  declared SPECIAL sentinel is now accepted on the REST write-coerce path, the
  validation path, and the runtime validity check alike (previously the write
  path rejected a special-below-MIN that the runtime accepted). The rule handles
  both the object (`{"NOT_USED":0.0}`) and list (`[{"ID":…,"VALUE":…}]`) wire
  encodings, mirroring the CCU's own clamp behaviour.

### Changed

- **Duration/ramp editor values match the CCU.** The time base/factor encoder now
  selects the same natural base as the reference (e.g. 2 min → `MIN_1 × 2` rather
  than `SEC_5 × 24`), so the value the CCU editor displays matches what was set.

### Internal

- **Cross-stack model-parity gate is no longer vacuous and now guards pull
  requests.** The datasource wire-identity step provisions the pydevccu/godevccu
  data roots and fails a zero-key run instead of passing silently; a scoped
  model-parity check runs on PRs; and `model_snapshot_diff.py` now counts missing
  channels and device-field mismatches toward the drift total.
- **Broad test hardening across the model core and every north-bound surface:**
  a runtime value-coercion parity golden against the reference; a chi-router ↔
  OpenAPI route walk; a WebSocket broadcast-emitter binding contract and
  model→WS fan-out assertion; an MQTT command write-roundtrip and a committed
  broker-discovery reference; a matter.js schema-staleness contract and per-
  category (Thermostat/DoorLock/WindowCovering/ColorControl/SmokeCoAlarm) bridge
  smoke; custom-profile constructor-resolution and wire-reference guards; and
  Svelte channel-editor undo/redo, cross-validation, edit-lock, schedule,
  preset, and login/OIDC coverage.

## [0.29.0] — 2026-07-08

### Added

- **HmIP-LSC lights now expose colour temperature alongside full colour.** The
  HmIP-LSC uses RGBW hardware but, unlike the HmIP-RGBW, has no
  `DEVICE_OPERATION_MODE` — it runs hue/saturation and colour temperature at the
  same time and reports whichever axis is currently inactive as an empty value.
  It was previously treated as a plain colour light, so its white/colour-
  temperature control never surfaced. It is now modelled as a combined
  colour + colour-temperature light: MQTT discovery advertises both
  `color_temp` and `hs`, the state stream reports the active `color_mode`
  (derived from which value is set), and the Matter Color Control cluster
  reflects the active mode. Ports aiohomematic's `CustomDpIpRGBWColorTempLight`.
- **Doorbell devices are announced as doorbells, not generic buttons.** The ring
  event of the HmIP-DBB and HmIP-DSD-PCB now carries the `doorbell` event
  device class in Home-Assistant MQTT discovery instead of the generic `button`,
  matching the reference stack (homematicip_local #3276).

## [0.28.2] — 2026-07-08

### Fixed

- **Config-UI control tiles: several Custom-DP actions were silently sending the
  wrong parameter or operation name, so the click either returned HTTP 422 or did
  the wrong thing.** The Svelte CDP widgets and the Go custom-DP dispatcher had
  drifted apart on the parameter/operation contract, and the only test that
  looked like it covered the path (`spa_e2e_switch`) exercised the backend keys
  rather than the payload the SPA actually emits — so the divergence went
  unnoticed. Fixed across the board:
  - **Switch → "on for" and Valve → "open for":** the widgets sent
    `{seconds: …}` / `{duration: 600}` while the dispatcher required `duration`
    (and interpreted a bare number as *milliseconds*), so the switch button
    returned `422 missing required param "duration"` and the valve's 10-minute
    preset actually opened for 0.6 s. The dispatcher now accepts a canonical
    `seconds` key (a number of seconds) for `turn_on_for` / `set_on_time` /
    timed valve `open`, keeping the `duration` string form as a
    backward-compatible alias for API/MQTT clients.
  - **Light → brightness slider** sent `{level}` but the dispatcher reads
    `brightness`; **fixed-colour palette** sent the colour *name* while the
    dispatcher expects the numeric `slot`; **effect dropdown** sent `{effect}`
    instead of the accepted `{label}`; and the **HSV saturation** was sent on a
    0..1 scale while the dispatcher treats it as 0..100 (so saturation landed
    ~100× too low). All corrected in the widget.
  - **Climate → "away for duration"** invoked a non-existent operation
    (`set_away_for_duration`); it now calls the real `enable_away_by_duration`.
  - The Switch/Valve timed actions are now driven by a shared input-plus-presets
    control (30 s / 1 min / 5 min, or a free value) instead of a single fixed
    preset button. A new contract test pins every SPA CDP widget's emitted
    operation and parameter keys against the dispatcher's accepted set so this
    class of drift fails the build.

## [0.28.1] — 2026-07-08

### Fixed

- **JSON-RPC cold-start session storm: concurrent callers no longer each open
  a separate CCU session.** `loginOrRenew` read the cached session ID under a
  short lock but released it before the actual `Session.login` round-trip, so a
  burst of concurrent calls at start-up (or after a session expiry) all saw an
  empty session and fired parallel logins — opening several CCU sessions at once
  and tripping the CCU's "too many sessions" limit. Login/renew is now serialized
  through a dedicated lock with a lock-free fast path for a valid, recently
  refreshed session and a re-check under the lock, so a cold-start burst performs
  exactly one login; the auth-failure retry path dedupes the same way. Ports the
  hardening from the aiohomematic reference client (login-storm serialization).

### Security

- **The two unauthenticated callback listeners now cap concurrent
  connections and no longer eager-allocate an attacker-declared frame
  size.** The XML-RPC (`:8120`) and BIN-RPC (`:8129`) callback listeners
  bind on the LAN without authentication. Neither had a concurrent-
  connection limit, so a host on the same segment could open thousands of
  sockets and pin one goroutine (plus its read buffers) per connection.
  BIN-RPC additionally allocated the payload size declared in the 8-byte
  frame header up front (`make([]byte, size)`, up to 10 MiB) before any
  body byte arrived, so a flood of stalled headers amplified memory use.
  Both listeners now honour `callback.max_connections` (default 64) via a
  connection cap, and BIN-RPC grows the payload buffer with the bytes that
  actually arrive instead of the declared size. The BIN-RPC source-IP
  allowlist is now enforced in the accept loop (before a handler goroutine
  is spawned), and a new opt-in `callback.restrict_source_ips` extends the
  same allowlist — resolved from the configured CCU hosts plus loopback —
  to the XML-RPC listener as well. Defaults preserve existing open-LAN
  behaviour except for the new connection cap.

## [0.28.0] — 2026-07-07

### Fixed

- **Disabling or editing an already-live CCU in the Config UI no longer
  silently leaves the old connection running.** `PUT /admin/centrals/{name}`
  persisted the row but, for a central already registered at runtime,
  returned success without touching the live `central.Unit` — disabling a
  CCU left it fully connected and polling until the next daemon restart,
  with no log line, while the Config UI showed a plain "CCU disabled"
  toast. Disabling a currently-registered central now tears the live
  connection down (mirroring the existing Delete order) and logs it. A
  config edit to an already-live CCU still cannot be hot-applied, so it now
  logs a `central.edit.restart_required` warning, and the Config UI shows
  an explicit "a daemon restart is required" toast instead of an
  unqualified "CCU updated" success when a southbound-relevant field
  (host, ports, TLS, credentials, interfaces) actually changed.
- **MQTT: command and HA-birth handlers no longer block the client's
  read loop, which could self-deadlock or stall unrelated commands.**
  `go-mqtt` runs every inbound `MessageHandler` synchronously on the
  single goroutine that also processes PUBACK/PINGRESP for the same
  connection. `BirthSync.handle` called `Bridge.RepublishDiscovery`
  inline — a blocking QoS1 `Publish` per declared discovery topic,
  each waiting on a PUBACK only that same (now-busy) goroutine could
  ever deliver — a guaranteed self-deadlock on every Home Assistant
  restart once more than a handful of entities were declared.
  `CommandSubscriber`'s handlers (`SetValue`/`SetMasterValue`/
  `InvokeChannelService`/…) called the sink inline too, so a CCU
  write stuck for seconds behind the circuit breaker/retry stack
  stalled every other in-flight MQTT message on the same connection.
  Both now dispatch the actual downstream call onto a small bounded
  worker pool (`boundedDispatcher`) and return immediately; per-worker
  routing is keyed by the inbound MQTT topic so writes to the same
  data point never reorder, and a full queue blocks briefly with a
  logged warning rather than silently dropping the command. Both
  `BirthSync` and `CommandSubscriber` now expose `Close()` for a clean
  goroutine drain on teardown.
- **MQTT: removed devices no longer keep stale retained raw-plane
  topics.** `onDeviceRemoved` only retracted the device's
  HA-Discovery `/config` entries; the retained raw-plane
  `values`/`master`/`calculated`/custom-DP state topics plus the
  device-scoped `availability`/`info`/`diagnostics` topics survived
  forever, so non-HA MQTT consumers kept seeing a removed device as
  permanently `available:true` with a stale last value. Device
  removal now also calls the new `Bridge.RetractRawStateForDevice`,
  which publishes an empty retained payload to every raw-plane topic
  the bridge declared for that device address.
- **MQTT: `QoSProfile.Commands` is no longer dead configuration.**
  `command_subscriber.go` hardcoded QoS 1 on every inbound `/set` /
  `/trigger` / `/invoke` subscription regardless of the configured
  `QoS.Commands` value. `CommandSubscriber` now subscribes at a
  configurable QoS (default QoS 1, unchanged) via `WithQoS` /
  `Bridge.CommandQoS()`; `docs/mqtt-topic-schema.md` corrected — it
  previously and incorrectly described command topics as QoS 0.
- **Backup docs no longer claim `secret.key` is bundled in the CLI
  archive.** `docs/admin/backup.md` said the archive was
  self-contained for decryption; `backup create` has always skipped
  `secret.key` while sealing the archive with it instead. The doc now
  states plainly that `secret.key` (or `OPENCCU_LOOM_SECRET_KEY`) must
  be preserved out-of-band, and `backup create` prints a one-line
  reminder of this to stderr after every successful run.
- **Cache metrics: command-tracker/ping-pong sizes now populated.**
  `metrics.Aggregator.Cache()` looped over connected interface clients
  but discarded each one, so `CacheMetricsSnapshot.CommandTracker` and
  `.PingPongTracker` (and therefore `TotalEntries`/`OverallHitRate`)
  always reported zero. The client adapter now exposes
  `CommandTrackerSize`/`PingPongSize` and the aggregator sums them
  across every connected client.
- **Manual backup/restore is now multi-CCU-correct.** `POST
  /backups` triggered a create-and-download backup for only the
  first registered central, and — worse — the CCU backup restorer
  was wired once, globally, so restoring *any* stored `.sbk`
  uploaded it to whichever central had come up first, regardless of
  which CCU actually produced it. `BackupAdapter` now resolves a
  backup's owning central from its id and holds one restorer per
  central; `Restore` targets strictly that central's restorer and
  never falls back to a different one. `POST /backups` accepts an
  optional `{"central_name": "..."}` body to trigger a specific
  central explicitly (the bare/empty-body call keeps backing up the
  first registered central, unchanged); a new admin-only WS command
  `backups.trigger` exposes the same central-scoped trigger. The
  Config UI's Backups page shows a target-CCU picker once more than
  one central is registered.
- **Config UI: Programs and Sysvars lists no longer silently cap at
  one page.** `listPrograms()`/`listSysvars()` fired a single
  unparameterized request; the client now pages through `/programs`
  and `/sysvars` (`page`/`per_page`) until a short page signals the
  end, mirroring the loop `devices.svelte.ts` already used for
  `/devices`. `Favorites.svelte`'s pinned-sysvar lookup benefits for
  free since it shares the same client call.
- **Config UI: Settings now matches the shared operating concept.**
  The MQTT-reload result was an inline coloured `<span>`; it is now a
  `toastStore.success`/`.error` call like every other action result.
  The schema-load failure was an ad-hoc `<Card>` with red text; it is
  now the shared `ErrorState` component with a working retry button.
- **Config UI: the shared `ConfirmDialog` now traps focus.** Opening
  the dialog moves focus to the Cancel button and restores it to the
  triggering element on close; Tab/Shift+Tab now cycle only between
  the dialog's two buttons instead of leaking focus to the page
  behind the modal backdrop. Every destructive-action flow in the SPA
  reuses this one component, so the fix applies everywhere at once.

### Added

- **Config UI: reliability + values-cache admin panel on the
  Diagnostics page.** New `client.ts` wrappers (`getReliability`,
  `getValuesCacheStats`, `resetValuesCache`) surface the existing
  `GET /diagnostics/reliability` and `/admin/values-cache/*` /
  `/devices/{addr}/values-cache/reset` endpoints, which previously had
  no Config UI surface at all. The Diagnostics page now shows a
  per-`(central, interface)` circuit-breaker/connection-state table
  next to the interfaces table, plus a values-cache stats card with a
  confirm-gated reset action.

- **`GET /incidents` gained `central`/`since`/`until`/`limit` filtering.**
  Previously an unbounded, unfiltered `SELECT` across every registered
  central (unlike the equivalent `/audit` endpoint). The bounds are now
  pushed down to SQL via a new `IncidentStore.GetIncidentsFiltered`;
  `?limit=` defaults to 500 (max 5000).
- **Master profiles, bulk incident clear, and service-message disable are
  now reachable over REST**, not just the WS command protocol:
  `GET /devices/{addr}/channels/{no}/master-profiles[/{id}]`,
  `POST .../master-profiles/match`, `DELETE /incidents` (operator),
  and `POST /service-messages/{id}/disable` (operator). All four share
  their domain call with the equivalent WS command
  (`master_profiles.list/get/match`, `incidents.clear`,
  `service_messages.disable`) rather than re-implementing the logic.
- **"Before you upgrade" guidance in `docs/admin/backup.md`.**
  Recommends `openccu-loom backup create` ahead of any upgrade and
  states the rollback reality: migrations carry `Down` blocks but the
  daemon only runs `goose.UpContext` and exposes no rollback
  subcommand, so recovering from a bad upgrade means restoring a
  pre-upgrade backup.
- **Values cache: periodic dead-row garbage collection.**
  `ValuesCacheStore.GCDeadRows` used to be exercised only by tests —
  nothing in the daemon ever called it, so a row whose channel or
  parameter permanently disappeared from a device's model (firmware
  update, profile change) stayed in `values_cache` forever. The
  background flusher goroutine now also drives a much lower-frequency
  GC ticker (derived from the flush interval; 30 min under the
  default 60 s flush cadence) that rebuilds the alive-key set from
  every central's current device model and deletes rows that no
  longer map to a live parameter.
- **Test coverage: MQTT combined-DP/schedule discovery, SSDP
  discoverer lifecycle, the DeviceDetail/Diagnostics SPA routes, and
  the CCU add-on `update_script` exit-code contract.** Closes four
  previously-untested surfaces: table-driven `Build*`/`Publish*`
  discovery + state tests for `internal/north/mqtt/discovery_combined.go`
  and `discovery_schedule.go`, plus an eventbridge-level check proving a
  broker-publish failure during schedule/combined-DP discovery still
  increments the bridge's `publish_errors` counter even though the call
  sites discard the error value; `internal/north/discovery/ssdp`'s
  `New`/`fetch`/`List`/stale-eviction paths via `httptest` and an
  injectable clock; `DeviceDetail.test.ts` (loading/error/happy-path) and
  a `Diagnostics.test.ts` page-load case, plus a new
  `tests/e2e/device-detail.spec.ts` Playwright spec driving a real MASTER
  paramset write and asserting the success toast; and a hermetic
  `tests/contract/ccu_addon_update_script_test.go` that runs the real
  `packaging/ccu-addon/ccu/update_script` against stubbed system commands
  and locks its 0=no-reboot / 10=reboot exit-code contract per platform
  identifier — the exact contract that regressed in 0.27.1.

### Changed

- **Values cache: dirty tracking is now per-`(channel, parameter)`
  key, not per-central.** The periodic flusher used to mark an
  entire central dirty on any single data-point change and then
  re-serialise every live/stale data point of that central on the
  next tick — on a ~1000-DP install, one hot data point forced a
  full-fleet SQLite UPSERT every tick. The flusher now tracks the
  exact set of changed `(channel, parameter)` keys per central and
  persists only those, falling back to a full walk only for the
  initial post-boot tick (covering values that changed before the
  flusher's event subscriptions were installed) and the final
  shutdown flush.
- **The v1 token-admin API (`GET`/`POST /auth/tokens`) is now marked
  `deprecated: true`** in the OpenAPI spec; it remains served (existing
  external API consumers keep working, and `DELETE /auth/tokens/{id}`
  still revokes v1 tokens) but new integrations should use
  `/auth/tokens/v2`, which the Config UI already exclusively uses.
  `docs/admin/auth.md` now documents v2 as the primary surface. Removed
  the dead `listTokens()` v1 client wrapper (unused by the SPA).
- **Config UI: hand-rolled native `<select>` filter dropdowns migrated
  to the shared `lib/components/ui/Select.svelte` primitive.** Covers
  the static central/action/role/interface pickers in `AuditLog`,
  `MessageList`, `ProgramList`, `SignalQualityList`, `UnIgnoreList`,
  `FirmwareList`, `Inbox`, `DeviceDetail` (history channel/parameter
  pickers), and the `UsersAdmin`/`TokensAdmin`/`CentralsAdmin`/
  `RoomsFunctionsAdmin` settings panels — all now match the rest of
  the SPA's themed dropdown look instead of the browser's native
  control chrome. `DeviceList`, `Overview`, `Diagnostics`, and the
  `Settings` general-tab language picker keep their native `<select>`
  deliberately: each sits on a route with a committed Playwright
  visual-regression baseline that this branch cannot regenerate, so
  swapping the control there risked an unverified layout diff.
  `Logs.svelte`'s two selects also stay native — they share a toolbar
  styled entirely off `--ha-*` CSS custom properties, and the shared
  `Select` primitive uses fixed Tailwind slate colors, so migrating
  only those two controls would have made that one toolbar row look
  inconsistent with its neighbors.

## [0.27.2] — 2026-07-07

### Added

- **Config UI: About page (`#/about`).** New "About" entry at the
  bottom of the sidebar's System cluster, plus a version line in the
  sidebar footer that links there. The page collects the support
  answers in one place: daemon version / commit (linked to GitHub) /
  build date / build variant (standalone vs CCU add-on) / start time
  and uptime / API version / active capabilities, a card per central
  (model, CCU firmware, serial, pending CCU update), and the
  license + project links mirrored from the no-JS `/about` page.
- **REST: `GET /api/v1/info` now reports `addon_build`** — `true`
  when the binary was built as the CCU/RaspberryMatic add-on, so
  clients can tell where the daemon runs. API version 2.15.0
  (backwards-compatible addition).

## [0.27.1] — 2026-07-07

### Fixed

- **CCU add-on: installing or updating no longer reboots the CCU on
  RaspberryMatic / OpenCCU.** The add-on's `update_script` returned the
  exit code that explicitly requests a reboot, although nothing in the
  add-on needs a boot-time hook (no ReGa patch, no interface
  registration). It now starts the daemon in place and reloads monit,
  returning the no-reboot exit code; RaspberryMatic / OpenCCU install
  and update the add-on without restarting the CCU. The stock CCU3
  firmware is unchanged — its WebUI reboots unconditionally and
  performs the installation during that boot, which an add-on cannot
  influence.

## [0.27.0] — 2026-07-07

### Security

- **REST: idempotency replay cache no longer leaks responses across
  users.** The cache key now includes the authenticated subject (the
  middleware runs after auth resolution), so two users sharing an
  `Idempotency-Key` value can no longer receive each other's cached
  responses. Concurrent requests with the same key are serialized: the
  second in-flight duplicate is rejected with `409 Conflict` instead of
  executing the mutation twice.
- **REST: request bodies are size-capped everywhere.** The OpenAPI
  validator and the token-create endpoint read bodies through
  `http.MaxBytesReader` (1 MiB) instead of unbounded buffering;
  oversized bodies now return `413` (previously `400` or unbounded
  memory use). Firmware/backup downloads from the CCU are capped as
  well.
- **OIDC: the login flow now mints a per-flow `nonce`** (OIDC Core
  §3.1.2.1), binds it to the pending state server-side, and rejects ID
  tokens whose `nonce` claim is missing or mismatching — a captured ID
  token can no longer be replayed into a different session.
- **REST: 5xx responses no longer echo internal error strings.**
  Server-side failures log the real error and return a generic detail;
  attachment filenames in `Content-Disposition` headers are escaped via
  `mime.FormatMediaType`.
- **REST: `GET /config/effective` no longer leaks array-nested secrets.**
  The secret-masking walk now descends into arrays, so per-central CCU
  passwords (`centrals[].password`) are masked to `***` like every other
  secret — previously they were returned in cleartext to any
  authenticated reader.
- **BIN-RPC decoder hardened against malformed length fields.** A struct
  member / array element count is now validated against the minimum wire
  size per element before allocating, so a crafted count on the callback
  listener (`:8129`) can no longer panic `makeslice` (a single packet
  crashed 32-bit/armv7 builds) or pre-allocate many times the payload
  size.
- **Resource caps against unauthenticated floods.** The Matter UDP
  receive loop bounds concurrent per-datagram dispatch goroutines; the
  Matter unsecured-PASE duplicate-detection map is bounded per
  commissioning window (spoofable `SourceNodeID`); each WebSocket
  connection's subscription set is capped. The CCU JSON-RPC client now
  size-limits response bodies (configurable; default 128 MiB, generous
  for bulk `getAllDeviceData` / `listAllDetail` fetches) and the XML-RPC
  callback HTTP server gained read/write/idle timeouts.

### Fixed

- Data races: `InterfaceClient.GetVersion` vs `SetVersion`; the Matter
  secure-session `closed` flag (now atomic); the Matter fabric-index
  read in the session resolver (now lock-guarded); the command-retry
  jitter source (a shared `*rand.Rand` used concurrently by per-key
  retry chains, now mutex-guarded).
- Connection-recovery coordinator: concurrent recovery triggers for the
  same interface are now genuinely serialized (the wait re-checks in a
  loop instead of once, and the active-slot cleanup is ownership-checked)
  — previously a single completion could release several waiters that
  then ran duplicate recovery pipelines against one CCU interface.
- Device coordinator: `DeviceCreated` events are published outside the
  coordinator lock (a subscriber calling back into the coordinator
  could deadlock); the paramset-consistency check goroutine now has
  panic recovery and a stop path that `Stop()` waits for.
- XML-RPC decoder: nested payloads are depth-limited (mirrors the
  BIN-RPC decoder), closing a stack-exhaustion vector on the callback
  listener.
- Config: invalid numeric values in `OPENCCU_LOOM_*` environment
  overrides now fail the boot with the variable name instead of being
  silently ignored.
- Payload parameter parsing rejects trailing garbage (`"42xyz"` was
  accepted as `42` via `fmt.Sscanf`; now `strconv`).
- Background firmware-data refresh after a device update surfaces its
  error instead of discarding it silently; the async audit sink's
  closer now waits for the worker to drain.
- WebSocket: every registered command must be classified read-only or
  mutating (contract-enforced), so a forgotten role entry can no longer
  expose a mutating command to viewers; writes go through a single
  writer goroutine per connection.

### Changed

- Internal deduplication: shared XML-RPC backend helpers (CCU / CUxD /
  Homegear), one SQLite handle passed through the daemon wiring instead
  of four independent opens, shared REST rate-limiter store, shared
  MQTT topic builder; repo-wide cleanup of truncated provenance
  comments.
- Dependency maintenance: refreshed direct Go modules (`go-mqtt`
  1.1.0 → 1.2.0, `go-chi/chi/v5` 5.3.0 → 5.3.1, `pressly/goose/v3`
  3.27.1 → 3.27.2, `golang.org/x/text` 0.38.0 → 0.39.0) and updated the
  Svelte SPA toolchain within its declared version ranges (Svelte, Vite,
  Vitest, Tailwind, svelte-check, and related dev dependencies).

## [0.26.6] — 2026-07-05

### Fixed

- **Matter pairing: subtype query replies were dropped by strict mDNS
  stacks — commissioners across an mDNS reflector never got the
  answer.** The subtype responder's reply to a
  `_L<disc>._sub._matterc._udp` PTR query echoed the query's question
  section and ID (miekg/dns `SetReply` semantics). RFC 6762 §6 forbids
  questions in multicast responses (§18.1 requires ID 0); Avahi-class
  stacks — including the reflectors that bridge mDNS between subnets —
  validate and silently drop such packets. Field capture showed the
  full chain: query received (`query_seen`), reply sent (`write4_ok`),
  nothing arriving in the querier's subnet, while grandcat/zeroconf's
  primary-type replies (which null the question section) passed the
  same reflector. The reply now clears the question section and forces
  ID 0, mirroring grandcat/zeroconf `server.go` and the RFC. With
  0.26.5's announcements this completes both cache-fill paths a
  commissioner can take: unsolicited announcement AND live
  query/response.

## [0.26.5] — 2026-07-05

### Fixed

- **Matter pairing: commissioners never saw the discriminator-filter
  record — "device not found" despite a valid, resolvable bridge.**
  Apple's commissioning browse filters by the QR code's discriminator
  via the `_L<disc>._sub._matterc._udp` mDNS subtype and satisfies that
  browse from the peer's mDNS cache. The primary instance record lands
  in caches through register-time announcements, but the side-car
  subtype responder was purely reactive: it never announced its PTRs
  and thus relied on a live subtype query reaching its socket — which
  the field capture showed does not happen for commissioner browses.
  The responder now multicasts every registered subtype PTR as an
  unsolicited RFC 6762 §8.3 announcement (twice, one second apart, on
  every multicast-capable interface, both address families) when a
  commissioning window opens, refreshes them on the periodic
  re-announce, and emits TTL=0 goodbyes when the window closes.
  Verified end-to-end: a subtype-filtered browse
  (`dns-sd -B _matterc._udp,_L3840`) that returned nothing now
  surfaces the bridge instance immediately.
- The subtype PTR TTL now matches the primary record's 3200 s (was
  120 s) so the filter record no longer expires from commissioner
  caches while the instance it points at is still valid.
- Inbound subtype queries are traced at debug level
  (`matter.mdns.subtype.query_seen`) so field diagnosis can tell
  "query never arrived" from "query arrived but was not answered".

## [0.26.4] — 2026-07-05

### Fixed

- **Matter pairing failed out of the box: the bridge never advertised
  itself.** `north.matter.mdns_advertise` defaulted to the in-memory
  `noop` advertiser, so an enabled bridge with a configured passcode
  published **no** `_matterc._udp` records — commissioners (iPhone /
  Home Assistant app, Apple Home, Google Home) reported "device not
  found" after scanning a perfectly valid QR code. The default is now
  `zeroconf`; `noop` remains available as an explicit opt-out for
  hermetic tests and out-of-band discovery. Switching the value still
  requires a daemon restart, which the config-change surface now
  reports.
- **Commissioner-visible surfaces used the raw (un-defaulted)
  discriminator.** The commissioning-window opener, the mDNS
  advertisement, and `GET /api/v1/matter/setup-payload` read
  `north.matter.discriminator` directly, so an unset value produced QR /
  manual codes and mDNS TXT records carrying discriminator 0 while the
  bridge core applied the documented 0xF00 default. All Matter config
  consumers now share one defaulting point
  (`config.NorthMatter.WithDefaults`), covering vendor/product ID, node
  label, discriminator, advertiser selection, and PBKDF iterations (the
  startup log also showed the raw `iterations: 0` instead of the
  effective 1000).
- **Matter mDNS records no longer advertise container-bridge
  addresses.** The commissionable/operational A/AAAA records included
  IPs from `docker0` / `hassio` / `br-<hex>` / `veth*` interfaces on
  host-network deployments (e.g. the Home Assistant add-on); iOS
  iterates the advertised address list and times out on unroutable
  addresses during resolve. The Matter advertiser now applies the same
  virtual-interface filter as the client-discovery mDNS.
- **Misleading "commissioning_published" log under the noop
  advertiser.** With `mdns_advertise: noop` the bridge logged
  `matter.mdns.commissioning_published` although nothing left the
  process; it now logs an explicit
  `matter.mdns.commissioning_not_advertised` warning (and the
  operational-record counterpart `matter.mdns.fabric_not_advertised`)
  naming the config remedy.

## [0.26.3] — 2026-07-05

### Changed

- **CI: the Windows test leg no longer gates PRs.** Windows is not a
  shipped target (goreleaser builds linux only) and the runner was the
  slowest, flakiest matrix leg; the run moved to the nightly workflow
  as a non-gating canary for a potential Windows release comeback.

### Added

- **`centrals[].host` is validated syntactically.** The host must be a
  bare hostname or IP literal (IPv4, bare or bracketed IPv6, FQDN with
  optional trailing dot; underscores tolerated for nonstandard LAN
  names). Values carrying a scheme, path, query, fragment, credentials,
  or an embedded port are rejected at config load — the value is
  interpolated into every south-bound URL (XML-RPC / JSON-RPC
  endpoints, the CCU readiness probe), so malformed shapes would
  silently reshape those URLs. Also closes the CodeQL
  `go/request-forgery` findings at that trust boundary. The TCP port
  keeps its own config field.

## [0.26.2] — 2026-07-05

### Fixed

- **Non-string sysvar writes were silently dropped.** Every sysvar
  write went through the `set_system_variable` Rega script, whose own
  guard writes string-typed variables ONLY and emits nothing when it
  declines — so writes to LIST/ENUM (e.g. `Aus;Niedrig;Normal;Hoch`),
  FLOAT, INTEGER, and BOOL variables reported success without changing
  anything on the CCU (reads were unaffected). Writes now dispatch by
  type like the reference client: bool → `SysVar.setBool`, numeric
  values including enum/list indices → `SysVar.setFloat`, and only
  strings use the Rega script — whose decline (missing or non-string
  sysvar) now surfaces as an error instead of a silent no-op.
- **Sysvar writes normalise the payload to the declared type.** The
  wire value now derives from the sysvar's descriptor type instead of
  the caller's payload shape (mirrors the reference `parse_sys_var`):
  LOGIC/ALARM accept bool, 0/1, and the usual string forms
  (`true/on/yes/1`, `false/off/no/0`); FLOAT/NUMBER and INTEGER accept
  numeric strings; STRING stringifies scalars; LIST resolves labels to
  their zero-based index. Mismatches (unknown enum label, fractional
  value for INTEGER, non-numeric string) are rejected with a clear
  error instead of silently picking the wrong wire method. The full
  type × payload matrix is locked by table tests.

## [0.26.1] — 2026-07-05

### Added

- **chip-tool CI workflow.** `.github/workflows/chiptool.yml` runs the
  chip-tool capability suite (`make chiptool-test`) and the compose
  PASE smoke (`make matter-smoke`) on Linux runners — nightly, on the
  `needs-chiptool` PR label, and via manual dispatch. The chip-tool
  binary is extracted from the `connectedhomeip/chip-cert-bins` image
  pinned in `compose/matter-smoke.yml` and cached keyed on that pin,
  so both layers always exercise the same chip-tool build. This closes
  the gap that chip-tool cannot run on macOS developer hosts (Docker
  Desktop does not support host networking).

### Fixed

- **Expired CCU JSON-RPC sessions broke sysvar/program writes until
  restart.** The CCU reports an invalid or expired session as HTTP 200
  + JSON-RPC error 400 ("access denied"), not as an HTTP auth status —
  but the client only re-logged-in on HTTP 401/403, and no production
  path called `Renew`, so after a session lapse (ReGa restart, CCU
  reboot, inactivity timeout) every JSON-RPC operation failed
  permanently with `access denied ("ADMIN" needed 0)` — surfaced e.g.
  as REST 502 on `PUT /sysvars/{name}` — even though the configured
  CCU account had admin rights. The client now maintains the session
  on both layers: proactively (login-or-renew ahead of every call,
  bounded by the existing 90 s freshness guard) and reactively (on
  error 400 it invalidates the session, re-logs-in, and retries once).
  A genuine privilege mismatch still fails with 400 on the fresh
  session and propagates as before.
- **Matter commissioning deadlock — new pairings have been broken
  since 0.23.0 (critical).** `PaseAdapter.ProcessPake3` invoked the
  session-pickup callback while holding the adapter mutex; the
  daemon's pickup callback calls back into the adapter
  (`PeerMRPParams`, added in the 0.23.0 behaviour-parity wave), so
  every successful PASE handshake self-deadlocked the receive
  goroutine after Pake3. The commissioner never received the closing
  `SESSION_ESTABLISHMENT_SUCCESS` StatusReport and timed out —
  observed uniformly with chip-tool, and affecting every controller
  (Apple Home / Google Home / Home Assistant) attempting a NEW
  commissioning on 0.23.0–0.26.0; already-commissioned fabrics
  resuming over CASE were unaffected. The callback now runs outside
  the lock (verifier state is torn down under the lock first),
  mirroring matter.js `PaseServer.ts`, which reads initiator session
  params post-verify with no lock held. Found by the new chip-tool CI
  workflow on its first real run — the exact gap it was built to
  close (the suite cannot run on macOS developer hosts). Regression
  test: `TestPaseAdapter_OnEstablishedRunsOutsideAdapterLock`.
- **From-source Docker build.** The SPA build stage in `Dockerfile`
  now copies `assets/ui/.npmrc` (`legacy-peer-deps=true`) next to the
  package manifests — without it npm 11 fails `npm ci` on the
  `typescript@^6` vs `openapi-typescript` peer range, breaking
  `docker build .` (and with it the compose matter-smoke stack) even
  though every non-container `npm ci` path worked.
- **`make matter-smoke` chip-tool invocation.** The compose exec now
  calls `/root/chip-tool` by absolute path — the `chip-cert-bins`
  image ships its binaries as `/root` symlinks without putting them on
  `PATH`, so the previous bare `chip-tool` invocation could never
  resolve. The target also waits for the daemon's `matter.bridge.up`
  log marker before pairing instead of racing the container
  healthcheck (which only proves the binary execs, not that the Matter
  UDP listener is bound).

## [0.26.0] — 2026-07-04

### Changed

- **Warm-boot descriptor cache now covers the whole fleet.** Hydration
  stores every fetched paramset description in the registry (and, via
  the persistence sink, in SQLite) instead of only the per-channel
  reload path — after one boot against a reachable CCU, all VALUES and
  MASTER descriptors survive restarts. Entity naming is unaffected:
  verified by a byte-identical model snapshot (the naming source
  counts materialised sibling data points and never consulted the
  registry index, which has no production reader).

### Removed

- **Masked `loom:reachable` annotations resolved.** The whitelist audit
  (`script/reachability/whitelist_audit.go`) now counts same-file
  production call sites (outside the symbol's own declaration), which
  removes its false MASKED positives (`TypeOfValue`, `RecalcUnit`,
  `NewParamsetRegistry`, `NewAlarmMessagesWithCentral`,
  `NewConnectionRecoveryCoordinatorWithLimit`). Genuinely dead exports
  whose annotation reasons claimed call sites that never existed were
  deleted: `adapter.StaticCallbackBaseURL`, `metrics.Round2`,
  `device.CheckChannelIsOnlyPrimaryChannel`, `naming.NewHubPathData`
  (plus the orphaned `HubSetPathRoot`/`HubStatePathRoot` constants),
  `payload.ForKinds`, `hmlog.BuildStack`, `hmlog.DefaultSensitiveKeys`,
  and `hmlog.RequestContextFilter` (an unwired duplicate of the
  production `reqctx.ContextHandler` chain). The nine deliberately
  retained seams (functional options, test constructors, schema
  introspection helpers, the values-cache GC key encoder) carry
  truthful annotation reasons now — no fabricated call-site claims.
  Production-unreachable baseline: 3093 → 3091.

### Changed

- **MQTT publishes are circuit-protected.** The bridge now publishes
  through go-mqtt v1.1.0's `Breaker`: during a degraded-broker phase
  (link up, acks missing) publishes fail fast with `ErrCircuitOpen`
  instead of each stalling on the 20-second AckTimeout, with bounded
  half-open probing once the recovery window elapses. Open-transitions
  increment the previously never-incremented `CircuitBreakerOpened`
  counter and are logged. The unwired local circuit-breaker copy is
  gone — breaker semantics live in the shared transport module now.

### Changed

- **MQTT transport upgraded to go-mqtt v1.0.0 (MQTT 5.0 by default).**
  The bridge now speaks MQTT 5.0 on the wire; brokers without v5
  support are pinned via the new expert setting
  `north.mqtt.protocol_version: "3.1.1"` (there is no silent
  downgrade — a v5 connect against a v3-only broker fails with a
  named error). Subscribes now block until the broker's SUBACK, so a
  rejected subscription surfaces as an error instead of being
  silently logged, and the last-will message is configured through
  the new `Will` shape.

- **Dead-code guard is enforced, not advisory.** A new CI ratchet
  (`.github/workflows/reachability.yml`) regenerates the reachability
  inventory on every PR and fails when the number of
  production-unreachable exports grows past the checked-in baseline.
  `loom:reachable` whitelist claims are now audited mechanically
  (`script/reachability/whitelist_audit.go` →
  `docs/parity/loom-reachable-audit.md`) — several annotations claimed
  wiring that never existed. The generic SQLite `PersistentCache`
  wrapper (superseded by the direct descriptor-persistence sinks) was
  removed, and the historical dead-code classification carries an
  addendum marking which of its verdicts the cleanup executed or
  disproved.

- **Superseded duplicate subsystems removed.** The unwired
  aiohomematic-config form-schema port (configui generator/grouping/
  labels/widget half plus the easymode use-case tree — the live UI
  schema comes from the central adapter's UISchema service), the
  duplicate core-package PowerSource cluster (production uses the
  measurement package's server), the schedule facade layer, the event
  bus batching helper, the untyped CentralRegistry and the model-less
  query-facade constructor are gone. Rationale and pointers to the
  live twins are catalogued in `docs/parity/by_design.md` ("Removed
  Unwired Subsystems").

### Fixed

- **Client lifecycle state has a single source of truth.** The interface
  client kept its state twice: a raw field (feeding `ClientState()`,
  `WaitForState` and all predicates) plus the validated state machine —
  and `Close()` only updated the raw field, leaving the machine claiming
  CONNECTED on a stopped client. The machine is now authoritative: reads
  and predicates consult it, `Close()`/`SetState` route through it, and
  its transition listener wakes `WaitForState` waiters (armed before the
  state check, closing a lost-wakeup window). Graceful STOPPING is now a
  valid transition from every non-terminal state, so shutdown paths no
  longer depend on forced transitions. A second, never-instantiated
  client state machine (with a silently diverged transition table) and
  the unused ConnectionState tracker were removed.

- **Warm boot: device and paramset descriptions are actually persisted.**
  `docs/caching.md` promised that descriptors survive a restart, the
  `devices`/`paramsets` tables and their stores existed, and the boot path
  (`CheckAndCreateDevicesFromCache`) was written to consume the cache — but
  the glue between the in-memory registries and SQLite was never wired, so
  the tables stayed empty forever and every boot re-pulled all descriptions
  from the CCU. Each central's registries are now hydrated from SQLite
  before its bring-up starts and mirror every later mutation back
  (normalised + patched, with content hashes for change detection). The
  ADR-0042 cache-clear now also clears the persisted descriptor rows —
  previously its `Devices`/`Paramsets` clearer slots were silently nil.

- **Reliability observability is actually wired.** The hooks connecting
  the per-client reliability primitives to the bus and the incident
  store existed but were never installed: circuit-breaker transitions
  now publish `CircuitBreakerStateChangedEvent` (the signal connection
  recovery, health tracking and the diagnostics event tap already
  subscribed to, but which never fired) and record incidents; ping/pong
  mismatches and exhausted retry chains are recorded as incidents;
  coalesced requests surface in the diagnostics event stream. Six event
  types that had neither publisher nor consumer were removed
  (`FirmwareStateChanged`, `HealthRecorded`, `DataPointsCreated`,
  `ConnectionStageChanged` and the internal `AlarmMessage`/
  `ServiceMessage` duplicates of the hub WS frames), along with a
  diagnostics subscriber waiting on the never-published
  `DataPointStatusChangedEvent` and the redundant scheduler event
  wrapper that duplicated the job instrumentation in `jobs.go`.

- **Metrics: the diagnostics snapshot's model section is populated.** The
  aggregator's device and hub providers existed but were never passed in
  `daemon` wiring, so `model.devices_total`, channel/data-point counts and
  program/sysvar totals silently stayed at zero. Both providers are now
  wired per central. The parallel bus-event metric funnel
  (`EmitLatency`/`EmitCounter`/`EmitGauge`/`EmitHealth` plus the four
  metric event types and `SubscribeObserver`) had subscribers but no
  publisher anywhere in production and was removed — the direct provider
  path is the single metrics pipeline.

- **Auth: `north.rest.auth.basic_enabled` / `bearer_enabled` now actually
  gate their schemes.** Both flags existed in the config (and the SPA
  editor) but were never evaluated — Basic auth was active whenever users
  were configured, Bearer whenever tokens existed. They are now tri-state
  gates (omit → enabled, explicit `false` rejects the scheme even with
  credentials configured), so what the config claims is what the daemon
  does. The never-evaluated `session_enabled` flag was removed: session
  cookies are the SPA's core login mechanism and are always on.
- **CCU data: `ccu_data.easymode_path` override is honored.** The
  documented file override (ADR 0003) was silently ignored — the easymode
  archive always loaded from the embedded bundle. It now follows the same
  file-first/embedded-fallback contract as `translations_path`.

## [0.25.0] — 2026-07-04

### Fixed

- **Matter: bridge could not be commissioned after a restart even with devices
  exposed.** The bridge topology is assembled at daemon start, before the CCU
  device load finishes, so it was empty of bridged endpoints — and the
  commissioning window (correctly) refuses to open an empty bridge — until an
  operator toggled a device exposure to force a rebuild. The bridge now
  reassembles automatically once each central's initial device load completes
  (`CentralSouthboundReadyEvent`), so persisted exposures take effect on their
  own. The 503 error text no longer references a signal that was never emitted.

### Security

- **Northbound auth hardening from a code-vs-code security audit.** A deep audit
  of the REST/WebSocket/auth surface found and closed several issues:
  - **WebSocket commands now enforce role gating.** State-changing commands
    (`paramset.put`, `device.install_mode`, `master_profiles.apply`,
    `backup.trigger`, …) require the connection's identity to hold the same
    minimum role the equivalent REST route demands. Previously a read-only
    *viewer* could invoke operator/admin writes over the socket, because the
    command dispatch dropped the caller identity — that also collapsed the
    per-command rate-limiter and `system.user_permissions` to "anonymous". Both
    are fixed by threading the connection identity into the dispatch context.
  - **CCU passwords are no longer returned in the clear.** `GET
    /api/v1/admin/centrals` now masks `password_plain` to `***` (with a
    restore-on-save round-trip), mirroring `GET /config`.
  - **Credential changes revoke sessions immediately.** A password change,
    role change, or user deletion now revokes that subject's other sessions
    (and, on deletion, purges its API tokens) instead of letting a stolen or
    stale session live out the 12 h TTL.
  - **The at-rest encryption key is excluded from backups**, so a stolen backup
    tarball no longer carries both the key and the ciphertext. *Operator note:*
    restoring a backup onto a fresh host now requires the `OPENCCU_LOOM_SECRET_KEY`
    env key (or copying `secret.key` out of band); otherwise encrypted secrets
    must be re-entered.
  - **Daemon-state backups are now fully encrypted at rest and hardened on
    restore.** The `backup create` archive previously wrote the unencrypted
    SQLite DB world-readable (0644), leaking live session tokens, Matter PSKs,
    and CCU passwords. The whole archive is now sealed with AES-256-GCM using
    the data-dir master key (a versioned container; legacy plaintext archives
    are auto-detected and still restorable) and created `0600`; if no master
    key is available the tool warns loudly rather than silently writing
    plaintext. `backup restore` gained a Zip-Slip guard (tar entries that
    escape the data dir are rejected), decompression-bomb bounds
    (total/per-entry/entry-count caps, streamed to disk), a schema-compat check
    (a backup from a newer daemon is refused unless `--force`), and
    all-or-nothing atomic staging (a mid-restore failure rolls back instead of
    leaving half-applied live data). *Operator note:* as with the key
    exclusion above, restoring an encrypted archive onto a fresh host needs the
    original `OPENCCU_LOOM_SECRET_KEY` (or `secret.key`).
  - **Scheduled-backup rotation race fixed.** The per-central scheduled job now
    awaits backup creation before pruning, so rotation settles at exactly
    `KeepLast` instead of `KeepLast+1`; concurrent runs for one central are
    serialized.
  - **OIDC login-CSRF closed:** the `state` value is now bound to an HttpOnly
    cookie and verified on callback; abandoned-flow state entries are swept.
  - **Session cookies are marked `Secure`** whenever the deployment terminates
    TLS (directly, via `csrf_secure`, or an `https` `public_url`).
  - Hardened the unauthenticated device-icon proxy against unbounded cache
    growth / address enumeration (only known devices are cached); added the
    1 MiB body cap to channel-config import; a dummy bcrypt compare on the
    unknown-user login path (anti-enumeration timing); and a fail-fast guard
    that refuses to serve REST with no auth middleware wired.

- **`hmcli` admin-CLI hardening from a code-vs-code security audit.** Five
  issues found and closed across every command group (`devices`, `sysvar`,
  `program`, `paramset`, `events`, `export-def`, `cache`):
  - **Off-argv credentials.** A bearer token / basic-auth password no longer has
    to be passed via `--token` / `--password` (where it leaks into shell history
    and the process table). Both now fall back to the `OPENCCU_LOOM_TOKEN` /
    `OPENCCU_LOOM_PASSWORD` environment variables, and a missing basic-auth
    password is prompted for on an interactive terminal. The flags remain a
    last-resort override; the token is never logged.
  - **User arguments are URL-escaped.** Device addresses, sysvar/program IDs,
    paramset keys, and query values are now `url.PathEscape`/`url.QueryEscape`-d
    before being spliced into the REST path/query, so a `/` (or other special
    character) can no longer inject extra path segments.
  - **Terminal-output sanitisation.** Server-controlled strings (device
    name/model, sysvar name/value, event type/topic/payload, …) are stripped of
    C0/C1 control bytes and ANSI escape sequences before being printed to a
    human-readable table, closing a terminal-injection vector. JSON output is
    unchanged (already escaped).
  - **`events tail` distinguishes clean from abnormal stream ends.** A clean
    server close still exits 0; an abnormal drop (daemon death, network loss,
    abrupt close) now triggers a bounded auto-reconnect with exponential backoff
    and, only after exhausting the retry budget, exits non-zero — a lost stream
    is no longer silently reported as success.
  - **TLS trust controls.** New `--cacert` (trust a custom PEM CA bundle) and
    `--insecure` (explicit opt-out of verification; **off** by default —
    verification is never weakened implicitly) flags, plus a warning when
    credentials would be sent over plaintext `http://` to a non-loopback host.
- **Audit / observability hardening from a code-vs-code audit of the
  change-log surface.** A follow-up audit of the audit and telemetry paths
  found and closed several leaks:
  - **`GET /api/v1/audit` is now admin-only.** The change-log — which exposes
    subjects, device addresses and operator actions across every central — was
    readable by any authenticated user (including a viewer); it is now gated on
    the admin role like `/auth/users` and `/auth/tokens`.
  - **Custom-DP write audit notes no longer embed the raw written values.** The
    note records only the *names* of the written parameters, so a write payload
    that carries a secret (e.g. a lock PIN) never lands in the append-only audit
    log.
  - **Trace/span export no longer bypasses log redaction.** Span and event
    attributes keyed like a secret (`password`, `token`, `client_secret`, …) are
    now masked to `***REDACTED***` in the OTLP payload, matching the logging
    redactor instead of shipping cleartext to the collector.
  - **User-management audit notes are unspoofable.** New usernames carrying
    `=`, whitespace, or control characters are rejected at creation, so a
    username can no longer tamper with the `subject=<name> role=<role>` note
    shape or inject forged log lines.
  - Prometheus MQTT counters now **sanitize the central-name segment** (with a
    deterministic hash suffix to avoid collisions) so an unusual CCU name can no
    longer emit an invalid exposition line that makes Prometheus drop the whole
    scrape.
- **Server-side edit-lock enforcement for configuration writes.** The
  per-resource edit lock (`POST /sessions/edit`) is now enforced, not merely
  advisory: every MASTER and LINK paramset write must present a valid
  `X-Edit-Token` that currently holds the lock, else it is rejected `423
  Locked` before any CCU call. This applies to `PUT
  /devices/{addr}/paramsets/{MASTER,LINK}`, `PUT /devices/{addr}/link-ps/{peer}`,
  and the WebSocket `paramset.put` command (via a new optional `edit_token`
  arg, rejected with a `locked` command error). Real-time VALUES writes
  (device control) are **not** gated. The config UI already holds the lock and
  now sends its token automatically; non-interactive clients must open an edit
  session first — `hmcli paramset set … MASTER|LINK` does this transparently.
  API version 2.14.0.

### Added

- **Optional API-token expiry.** `POST /auth/tokens/v2` accepts
  `expires_in_days`; expired tokens are rejected at authentication and
  `expires_at` is surfaced in the token list. Omitted → never expires
  (unchanged). API version 2.13.0.

- **User access-permission switches for HmIP-DLD and HmIP-FWI.** The per-user
  access-receiver channels (HmIP-DLD channels 2–9, HmIP-FWI channels 1–8) are now
  exposed as switches: the value reflects the current permission (`STATE`), and
  turning the switch on/off grants/revokes access by writing
  `ACCESS_AUTHORIZATION` = ENABLE/DISABLE. Ported from aiohomematic (#3262) via a
  profile regeneration against aiohomematic 2026.7.1. Available over REST/WS.
  The switches are intentionally **not** exposed to the Matter bridge — a per-user
  access control is deliberately kept out of Matter ecosystems; MQTT
  Home-Assistant-Discovery surfacing is a planned follow-up.

### Fixed

- **Measurement-history rollup + energy engine, from a data-integrity audit.**
  Six confirmed correctness bugs in the history / rollup / energy subsystem are
  fixed with a coherent rollup redesign:
  - **Bounded, watermark-driven rollups.** The hourly and daily folds used to
    re-scan every source row below the lag cutoff and ON-CONFLICT-rewrite every
    historical bucket on every tick — an ever-growing write that held a long
    write lock and starved the recorder's `SaveBatch`, whose flush then silently
    dropped the batch (lost live samples). Each tier now tracks a per-tier
    high-water-mark and folds only the newly-eligible, bucket-aligned window
    `[watermark, cutoff)`; a new history migration adds the watermark table and
    the `measurements(ts)` / `measurements_hourly(bucket_ts)` fold-scan indexes.
    A failed flush now re-queues the batch (bounded, metered via a new
    `history.flush_errors` gauge) instead of silently dropping it.
  - **Energy endpoint shows the current period.** `GET /api/v1/energy` merged
    only the rolled-up tiers, so the un-rolled recent window ("energy today",
    the current hour) was missing. The query now merges the raw (and hourly)
    tail for the un-rolled window into every group (hour/day/month).
  - **Retention no longer corrupts finalized aggregates.** Purge cutoffs are
    bucket-aligned and floored by the fold watermark, so a purge can never
    split a boundary bucket or delete a source row before it has been folded.
  - **Device energy totals include inter-bucket consumption.** A device total
    was a sum of per-bucket `last-first` deltas, dropping everything consumed
    between buckets. It is now a reset-aware range total (positive counter
    segments across the whole range), so a meter reset is handled without a
    negative or under-counted total.
  - **`history.retention` is clamped** to at least the hourly-rollup lag (1 h)
    at config load, so it can never be set below the point where the purge would
    delete raw rows before they are folded.
  - **Chart bucketing off-by-one fixed.** `QueryBuckets` no longer emits a
    spurious extra tail bucket for samples adjacent to the range end.

- **Config pipeline hardening from a code-vs-code audit.** Four issues in the
  config + configstore pipeline were found and closed:
  - **`OPENCCU_LOOM_REST_LISTEN` / `north.rest.listen` now survives every boot.**
    The REST bind address is bootstrap-tier: it is no longer persisted into or
    read back from the `north.rest` config section, so the env/YAML value always
    wins instead of being pinned to a stale value seeded on first boot. The SPA's
    source pill reports it as `bootstrap`, and the field is no longer shown as an
    editable REST field.
  - **`restart_required` in the section-PUT response is now computed per changed
    field** (via `config.RestartRequiredDiff`), so a mixed section (e.g.
    `north.rest`, where CORS is hot-appliable but `public_url` needs a restart)
    and the fully-restart-required webhook section report correctly — consistent
    with `GET /system/config-changes`.
  - **Semantic validation now runs on the section-PUT path.** A well-typed but
    semantically-invalid section (empty `broker_url` with MQTT enabled, a callback
    port out of range, an `ftp://` `public_url`) is rejected with 400 and never
    persisted, instead of being saved and only warned about at the next boot.
  - **A REST PUT can no longer wipe basic-auth users or API tokens.** Credentials
    are managed exclusively by the SQLite user/token stores (the `/api/v1/users`
    and `/auth/tokens` CRUD) and no longer round-trip through the `north.rest`
    section. Config-file (YAML) users **and** API tokens are migrated into SQLite
    once on boot (idempotent, preserving each token's exact secret), so no
    operator loses a login on upgrade.
- **A slow or half-open MQTT broker can no longer stall event delivery.** The
  internal event bus dispatches handlers serially, and the value-change handler
  used to publish to MQTT inline — so a QoS1 publish waiting for a PUBACK (up to
  the broker AckTimeout) froze bus dispatch, and with the broker shared across
  every CCU that meant no central delivered events until the broker recovered.
  The north-bound MQTT fan-out for live value changes is now decoupled onto a
  bounded per-broker worker queue with drop-oldest backpressure (a dropped
  counter is exposed for monitoring); the bus dispatch goroutine never blocks on
  the broker again. The boot-time snapshot path stays synchronous.
- **Event-bus unsubscribe is now a barrier.** The closure returned by
  `Subscribe` waits for any in-flight dispatch that already captured the handler
  to finish before returning, and a handler detached mid-dispatch is skipped
  rather than invoked — so a consumer can free the resources a handler touches
  immediately after unsubscribing, without racing a late callback (previously a
  recovered-and-swallowed send-on-closed-channel / nil-map panic).
- **`EventBridge.Start` is now idempotent** — a second call detaches the previous
  run's subscriptions and fan-out worker before re-attaching instead of
  double-subscribing; the subscription list is mutex-guarded.
- **CCU value loading: skip the init getValue fallback for BidCos-RF.** Passive /
  battery BidCos-RF devices that have not reported since a CCU restart no longer
  have their readable VALUES seeded from the paramset default and marked as a
  valid observation, which masked an actually-uncertain state. The data point now
  stays unobserved until a real value arrives via event (as already done for
  VirtualDevices); the timestamp-gated bulk ReGa fetch remains the trustworthy
  init source. Mirrors aiohomematic #3260.

## [0.24.0] — 2026-07-03

### Security

- **Matter bridge: access-control and commissioning hardening from a
  code-vs-code re-audit against matter.js.** Event reads and subscriptions now
  enforce ACL and fabric-sensitive filtering, so a controller on one fabric can
  no longer read another fabric's `AccessControl` change events; partial-wildcard
  reads re-authorize per resolved endpoint; the PASE brute-force cap now counts
  wrong-passcode attempts (and Pake1 decode failures) and revokes the window at
  the limit; CASE Sigma3 verifies the peer NOC's FabricId against the selected
  fabric; and reserved-range Node IDs / version-0 CATs are rejected as ACL
  subjects. Writes to read-only Matter attributes (e.g. `OnOff`) are now rejected
  instead of being dispatched to the CCU.

### Fixed

- **Matter bridge: broad matter.js parity pass.** Tolerate 21-octet
  operational-certificate serials so LG-TV-class controllers can commission;
  keep a stable per-endpoint DataVersion across config edits so controllers stop
  re-downloading the whole bridge on every exposure toggle; require a Timed
  interaction for `DoorLock` lock/unlock/unbolt; stop heating-only thermostats
  from advertising cooling controls; correct `WindowCovering` ConfigStatus/Mode,
  `ColorControl` ExecuteIfOff gating, and `OnOff` GlobalSceneControl behaviour;
  return the correct Interaction-Model status codes for malformed
  `OperationalCredentials` commands; and set the mDNS default pairing hint to
  power-cycle + manual. Every change mirrors matter.js HEAD; intentional and
  deferred divergences are catalogued in `docs/parity/by_design.md`.

## [0.23.1]

### Changed

- **MQTT transport extracted to the shared `github.com/SukramJ/go-mqtt`
  module.** The wire codec, TCP/TLS adapter, and reconnecting lifecycle that
  previously lived under `internal/north/mqtt` now come from the standalone,
  dependency-free module shared with the `go-*2mqtt` bridges, so a transport
  fix lands once for every consumer instead of drifting across copies. Loom's
  MQTT surface and behaviour are unchanged; the HA Discovery, entity-description,
  topic-building, and command-handling code stays in `internal/north/mqtt`.

### Fixed

- **MQTT: fewer spurious reconnects on a healthy broker.** The keep-alive
  watchdog now tolerates a single delayed PINGRESP and only declares the socket
  dead after two consecutive missed heartbeats (≈ one full KeepAlive), so a
  momentary network blip or scheduler stall no longer forces a
  `mqtt.tcp.ping_timeout` + reconnect. A genuinely half-open socket is still
  detected, one keep-alive interval later.

## [0.23.0]

### Security

- **Matter bridge: UpdateNOC validates the new certificate before persisting
  it.** The handler previously stored whatever NOC bytes the commissioner sent
  — no chain verification against the fabric's trust root, no check that the
  certificate covers the pending CSR key, no FabricID pinning. A defective (or
  malicious) UpdateNOC could replace a fabric's identity with an unusable or
  foreign certificate. The handler now mirrors matter.js/chip: chain
  verification against the stored root, `InvalidPublicKey` when the NOC does
  not certify the pending CSR key, `InvalidNOC` on a FabricID change. A NodeID
  change now propagates to the fabric record, the CASE identity, and mDNS (old
  instance withdrawn, new instance announced), and the fabric's CASE
  resumption records are invalidated as the spec requires.
- **Matter bridge: AddNOC rejects duplicate fabrics and wrong-key NOCs.**
  Installing the same `(FabricID, root public key)` pair twice now returns
  `FabricConflict` before touching the store, and a NOC whose subject key does
  not match the pending CSR key returns `InvalidPublicKey` — both previously
  unreachable status codes that matter.js/chip emit.
- **Matter bridge: removing a fabric withdraws its operational mDNS record.**
  RemoveFabric only triggered a republish, which cannot retire a record the
  advertiser still holds — the removed fabric's `_matter._tcp` instance kept
  answering and commissioners resolved a dead identity. The instance is now
  explicitly withdrawn (and the same path retires the old-NodeID instance
  after an UpdateNOC).
- **Matter bridge: PASE sessions are cleared on CommissioningComplete and
  fail-safe expiry.** Matter §11.10.6.6 step 4 requires the server to drop any
  still-established PASE session once commissioning completes; the expiry path
  does the same, so a failed commissioner's channel no longer outlives its
  fail-safe.
- **Matter bridge: CASE session resumption (Sigma2_Resume) now establishes a
  live, correctly-scoped session.** The resume fast path derived fresh keys but
  never adopted the peer's identity: the resumed session registered with
  peer-node-id 0 (every inbound decrypt failed the AES-CCM nonce check),
  peer-session-id set to the bridge's own id (every reply was dropped by the
  controller), no fabric scoping, and no CASE Authenticated Tags — and the
  fresh resumption id was never persisted, so the controller's next resume
  attempt referenced a stale id. A controller reconnect that offered
  resumption (Apple Home after idle timeout or reboot) hit a dead session
  until it gave up and re-ran the full handshake. The resume path now mirrors
  matter.js `CaseServer.ts` — session identity (fabric, peer node id, peer
  session id, CATs) comes from the stored resumption record, the responder
  identity is resolved by the record's fabric, and the rotated resumption id
  is written back. Resumption records also persist the peer's CATs now;
  previously every persist wiped them, silently stripping CAT-scoped ACL
  privilege from resumed sessions.

- **Matter bridge: AccessControl.ACL rejects out-of-range Privilege / AuthMode
  values.** An ACL write with an invalid `Privilege` (outside View..Administer)
  or `AuthMode` (outside PASE/CASE/Group) enum value was persisted unchecked; it
  is now rejected with `ConstraintError`, matching matter.js schema validation.
- **Matter bridge: OperationalCredentials.NOCs requires Administer to read.**
  The NOC / ICAC certificate bytes were readable — and streamable via a
  wildcard subscribe — at View privilege. Reading them now requires Administer,
  matching matter.js (`operational-credentials.element.ts` access "R F A").
- **Matter bridge: PASE pairing failures are now capped.** An open
  commissioning window accepted unlimited passcode guesses for its whole
  duration (up to 15 minutes commissioned, or 48 hours for an uncommissioned
  bridge), letting a LAN attacker brute-force the setup passcode. The bridge
  now counts pairing failures and revokes the window after 20, mirroring
  matter.js `PaseServer`'s `PASE_COMMISSIONING_MAX_ERRORS`; the counter resets
  when a new window opens.
- **Matter bridge: writes and command invokes are now gated by the
  per-element ACL privilege, not a flat Operate.** Previously every write
  and command was authorised at Operate, so a subject holding an
  Operate-privilege CASE entry (as controllers issue to household members)
  could write `AccessControl.ACL` to grant itself Administer, invoke
  `AdministratorCommissioning.OpenCommissioningWindow` to admit a rogue
  administrator, or `OperationalCredentials.RemoveFabric` to evict another
  ecosystem. The IM layer now looks up the required privilege per attribute
  / command (AccessControl.ACL → Administer, RemoveFabric → Administer,
  BasicInformation.NodeLabel → Manage, …), mirroring matter.js. Commissioning
  is unaffected (PASE sessions bypass ACL as before).
- **Matter bridge: subscriptions now enforce ACL on every report.** The
  subscription read paths (initial and ongoing, attributes and events)
  previously called the dispatcher directly, bypassing the access check the
  one-shot Read path applies — so a View-only or ACE-less subject could
  subscribe wildcard and stream fabric-sensitive data (`AccessControl.ACL`,
  `OperationalCredentials.NOCs`). Every subscription result is now authorised
  against the subscribing subject and fabric-projected, matching matter.js.

### Fixed

- **Matter bridge: only one PASE commissioning handshake runs at a time.**
  A second commissioner's PBKDFParamRequest arriving mid-handshake silently
  replaced the first one's in-flight verifier state; the bridge now rejects the
  overlapping request (self-expiring after 60 s so a crashed commissioner
  cannot lock the window), matching matter.js's single-active-PASE rule.
- **Matter bridge: PASE honours and advertises MRP session parameters.** The
  commissioner's InitiatorMRPParams (PBKDFParamRequest tag 5) is now applied to
  the PASE session's retransmit timing, and the bridge advertises its own
  ResponderMRPParams in the response — previously both were ignored, so
  retransmissions used spec defaults regardless of what either side asked for.
- **Matter bridge: MRP message counters resist nonce reuse.** New session
  counters seed in the low 28 bits (so a fresh counter never starts near
  exhaustion) and secure-session counters refuse to roll over past
  0xFFFFFFFF (a wrapped counter would reuse an AES-CCM nonce under the live
  key); the session is retired instead, matching matter.js. Reliable-send and
  subscription bookkeeping are now keyed per session, closing a
  cross-session counter-collision.
- **Matter bridge: NodeLabel and Location survive a restart.** A commissioner
  write to either attribute (both non-volatile per spec) is now persisted and
  restored at boot, instead of reverting to the configured default.
- **Matter bridge: event numbers stay monotonic across restarts.** The
  EventNumber counter was in-memory and reset to 1 on every boot, so
  controllers filtering on the last number they saw silently dropped every
  fresh event; it now persists a counter ceiling and resumes past it.
- **Matter bridge: device-type ACL targets are honoured.** An AccessControl
  entry whose target names only a device type previously always denied; it now
  matches endpoints advertising that device type, mirroring matter.js/chip.
- **Matter bridge: the periodic mDNS re-announce no longer churns caches.** The
  30-minute operational re-announce re-registered every record, emitting TTL-0
  goodbye packets that made Apple flush and re-learn the bridge each interval;
  unchanged records are now left in place and only real changes re-register.
- **Matter bridge: bridged endpoints report a stable DataVersion.** Bridged
  cluster servers are rebuilt on every dispatch, and each rebuild installed a
  fresh random DataVersion — the same cluster reported a different version on
  every read, so controllers' DataVersionFilters never matched and Apple
  re-transferred all endpoints on every re-subscribe. The version now lives on
  the persistent endpoint keyed by cluster, is stable across reads, and is
  bumped on real state changes (value pushes from the CCU, reachability
  flips, successful writes), matching matter.js's once-per-lifetime /
  increment-per-change semantics.
- **Matter bridge: GroupKeyManagement enforces the per-fabric key-set
  budget.** KeySetWrite accepted unlimited new key sets; adding one beyond
  MaxGroupKeysPerFabric now returns ResourceExhausted (updates of an existing
  key set stay allowed), matching matter.js.
- **Matter bridge: duplicates are acknowledged immediately.** The standalone
  ACK for a retransmitted (duplicate) reliable message was queued behind the
  200 ms piggyback grace window, so a peer that had already waited out its
  retransmission timeout kept retransmitting; the ACK now goes out
  immediately, matching matter.js.
- **Matter bridge: a Subscribe matching nothing is rejected.** A Subscribe
  request naming no attribute/event paths, or whose (wildcard) paths matched
  zero attributes and events, was accepted and registered as a dead
  subscription burning engine ticks; both cases now return InvalidAction
  before registration, matching matter.js. Re-subscribes whose reports are
  fully suppressed by DataVersionFilters still establish.
- **Matter bridge: outbound retransmissions honour the peer's MRP session
  parameters.** The retransmit schedule used a fixed 300 ms base and a bare
  1.6× growth; the intervals a controller advertises during PASE/CASE session
  establishment were parsed and thrown away. The initiator's Sigma1 session
  parameters are now stored on the operational session, the base interval is
  selected per transmission from the peer's active/idle interval (active when
  the peer sent within its active threshold; spec defaults 300 ms / 500 ms /
  4 s otherwise), and the full spec backoff formula (margin, exponential
  growth past the threshold, jitter) applies — matching matter.js `MRP` and
  chip `GetMRPBaseTimeout`. Retransmits to a sleepy or slow controller no
  longer fire ~40 % too early, and active peers get snappier recovery.
- **Matter bridge: chunked WriteRequests are validated.** The
  MoreChunkedMessages flag was ignored entirely; a chunked write combined
  with SuppressResponse, or inside a timed interaction, is now rejected with
  InvalidAction per spec, and each valid chunk is answered with its own
  WriteResponse (matching matter.js, which responds per chunk).
- **Matter bridge: MRP ack bookkeeping is session-scoped.** ACK obligations,
  standalone-ack reply routes and the per-chunk StatusResponse rendezvous were
  keyed on the bare 16-bit exchange ID, which is only unique per session — two
  concurrent controllers (or an old and a new CASE session of one controller)
  sharing an exchange ID could discharge each other's pending ACKs, hijack the
  synthesised standalone-ack route, or release the wrong chunk-streaming
  waiter. All three maps now key on (session, exchange), matching matter.js's
  session-scoped exchange resolution.
- **Matter bridge: WindowCovering reports a real TargetPosition.** The
  TargetPositionLift/TiltPercent100ths attributes always mirrored the current
  position, so controllers never saw where a moving cover was heading (Apple
  Home shows "Opening…"/"Closing…" from the target-vs-current delta). The
  attributes now carry the commanded destination — set by UpOrOpen /
  DownOrClose / GoToLift- / GoToTiltPercentage, snapped back to the current
  position by StopMotion — matching matter.js `WindowCoveringServer` across
  the cover, blind (lift + tilt) and garage projections.
- **Matter bridge: OnOff implements the OnWithTimedOff engine and writable
  LT attributes.** OnWithTimedOff previously collapsed to a plain On — the
  timed-off countdown, the AcceptOnlyWhenOn gate and the delayed-off guard
  period were all dropped, and the OnTime / OffWaitTime / StartUpOnOff
  attributes were read-only constants. Lights now run the full matter.js
  `OnOffServer` state machine: OnTime counts down in 100 ms ticks and turns
  the device off at expiry, an Off during a timed-on phase enters the
  delayed-off guard, AcceptOnlyWhenOn is honoured while off, and all three
  LT attributes are writable (StartUpOnOff requiring Manage privilege;
  parking a countdown at 0/0xFFFF stops it).
- **Matter bridge: DoorLock emits the mandatory LockOperation event.** A
  successful remote LockDoor / UnlockDoor / UnboltDoor produced no event, so
  controllers tracking lock activity (Apple Home notifications, event
  subscribers) saw state flips without the spec-mandated operation record.
  The bridge now emits LockOperation (priority critical) with the operation
  type (UnboltDoor reports Unlatch), OperationSource=Remote and the invoking
  fabric + node, matching matter.js `DoorLockServer`. The DoorLock EventList
  now advertises the three conformance-mandatory events.
- **Matter bridge: LevelControl honours ExecuteIfOff and the MinLevel/MaxLevel
  bounds.** A plain `MoveToLevel` / `Step` (the non-WithOnOff variants) sent to
  a light that is off executed anyway and turned it on; per Matter they must be
  a silent no-op unless the command's options set ExecuteIfOff. The requested
  level is now also cropped to the spec range [1, 254] (a plain `Step` down can
  no longer switch the light off — it floors at MinLevel), the WithOnOff
  variants couple a MinLevel target to Off, and `Move` with a zero rate returns
  `InvalidCommand`, all matching matter.js `LevelControlServer`.
- **Matter bridge: device reachability updates reach attribute-subscribers.**
  When a bridged HomeMatic device went (un)reachable the bridge fired the
  `ReachableChanged` event but did not mark the `Reachable` attribute changed,
  so a controller tracking the attribute (Google Home) showed stale
  reachability until it re-subscribed. The attribute is now marked dirty too.
- **Matter bridge: vendor-specific protocol datagrams are rejected, not
  misrouted.** A datagram carrying a vendor id whose low protocol id collided
  with Interaction Model / Secure Channel was fed into those handlers; it is now
  rejected as an unknown protocol, matching matter.js's full 32-bit protocol
  dispatch.
- **Matter bridge: empty subscription keepalives set SuppressResponse.** A
  no-op max-interval heartbeat no longer asks the controller for an IM
  StatusResponse, matching matter.js.
- **Matter bridge: GroupKeyManagement.KeySetRead / KeySetRemove return
  NotFound.** Reading or removing a non-existent group key set returned a
  generic failure (KeySetRemove even succeeded silently); both now return the
  spec `NotFound` status, matching matter.js.
- **Matter bridge: colour lights advertise and accept their mandatory
  ColorControl commands.** The mounted CT / HS / RGBW colour servers advertised
  an empty `AcceptedCommandList` and rejected the mandatory Move / Step /
  StopMoveStep commands, so a conformance controller (and Alexa's hue-only
  changes on an RGBW light) failed. Each server now lists its
  feature-appropriate command set and accepts the continuous-rate commands as
  no-ops (HM lights have no rate sweep), matching matter.js.
- **Matter bridge: colour-temperature lights advertise the mandatory
  `StartUpColorTemperatureMireds` + `CoupleColorTempToLevelMinMireds`
  attributes.** They were missing from the ColorControl read surface, so a
  conformance/controller read of them returned UNSUPPORTED_ATTRIBUTE. Both are
  now served (CoupleColorTempToLevelMinMireds = PhysicalMinMireds;
  StartUpColorTemperatureMireds = null), matching matter.js.
- **Matter bridge: read-event timestamps are correct.** Out-of-band event reads
  stamped the EpochTimestamp in microseconds instead of the spec-mandated POSIX
  milliseconds, so a read event's time read ~1000× off (and inconsistent with
  the subscribe path, which was already correct). Both paths now emit
  milliseconds.
- **Matter bridge: GeneralDiagnostics.TestEventTrigger is now enumerated.** The
  mandatory command was missing from `AcceptedCommandList` and returned a
  generic failure; it is now listed and returns `ConstraintError` (the bridge
  configures no test-event enable key), matching matter.js.
- **Matter bridge: commissionable mDNS Session Idle Interval corrected to
  500 ms** (was 5000 ms, from a non-existent matter.js default). A too-large SII
  made commissioners space PASE retransmits ~10× too slowly on a lossy link.
- **Matter bridge: multi-admin "add to another ecosystem" now works.** An
  Enhanced Commissioning Window opened by a second controller
  (`AdministratorCommissioning.OpenCommissioningWindow`) supplies a PAKE
  verifier derived from a passcode that controller chose. The bridge validated
  the verifier and then discarded it, so PASE ran against the bridge's own
  passcode and every "add to Google/Alexa/…" attempt failed at the Pake2
  confirmation. The bridge now installs a PASE acceptor built from the supplied
  verifier for the window lifetime (restoring the configured acceptor on close)
  and advertises the Enhanced window over mDNS with `CM=2` + its discriminator
  so the second controller can discover it — mirroring matter.js.

- **Matter bridge: ArmFailSafe ownership is now enforced.** A commissioning
  fail-safe armed by one fabric could previously be re-armed or disarmed by a
  different fabric, and a CASE session could arm the fail-safe during another
  admin's open commissioning window — letting one controller roll back another
  controller's in-progress commissioning. The handler now rejects a re-arm or
  disarm from any fabric other than the one that armed it, and rejects a CASE
  arm while a window is open, both with `BusyWithOtherAdmin`, mirroring
  matter.js. PASE commissioning is unchanged.
- **Matter bridge: cancelling a commissioning window no longer Busy-locks the
  next one.** `RevokeCommissioning` (and the internal revoke path) now expires
  the fail-safe as Matter §11.19.7.3 step 1 requires, so cancelling a pairing
  in a controller and retrying immediately no longer fails with "busy" for the
  remainder of the original window (up to 15 minutes).

- **Matter bridge: blinds, colour-temperature, dimmer-step and thermostat
  setpoint commands now actually execute.** The bridge's command decoder
  delivers fields as a context-tag-keyed map, but the WindowCovering
  `GoToLift`/`GoToTiltPercentage`, LevelControl `Step`, ColorControl
  `MoveToColorTemperature` and Thermostat `SetpointRaiseLower` handlers only
  accepted a differently-shaped payload, so every real Apple Home / Google
  Home / chip-tool invocation of those commands was rejected — or, for the
  thermostat, silently ignored. The handlers now accept the real wire shape;
  `GoTo*Percentage` values are additionally clamped to their 100.00 % maximum.
- **Matter bridge: IM responses and CASE/PASE handshake replies are now
  MRP-reliable.** `WriteResponse`, `InvokeResponse`, the `TimedRequest`
  `StatusResponse`, and the PASE/CASE continuation replies (Pake2, Sigma2)
  were shipped best-effort, so a single dropped UDP datagram surfaced as
  "Not Responding" on a command or aborted commissioning outright. They are
  now retransmitted until the controller acknowledges them, matching
  matter.js. Unsecured (PASE) retransmits are also now detected as duplicates
  and acknowledged without re-running the handshake handler.
- **MQTT: detect half-open broker connections via a PINGRESP watchdog.**
  The keep-alive loop sent `PINGREQ` but never checked for the matching
  `PINGRESP`, and the read loop blocks in `ReadFrame` without a deadline.
  On a half-open socket (broker or network gone without a TCP FIN/RST) the
  read loop stayed blocked forever, `handleConnectionLost` never fired, and
  the lifecycle never reconnected — QoS-1 publishes (HA discovery, command
  acks) then timed out with "context deadline exceeded" on a dead socket
  until a manual restart. The keep-alive loop now arms an outstanding-ping
  flag after each `PINGREQ`; if the next tick still sees it unanswered, the
  connection is declared lost so the lifecycle re-dials.

## [0.22.0]

### Added

- **Persistent hourly/daily measurement-history rollup tiers.** Two new
  aggregate tables (`measurements_hourly`, `measurements_daily`) fold raw
  history rows into low-resolution buckets (sum/min/max/count plus
  first/last, needed for cumulative `ENERGY_COUNTER` deltas) so long-term
  history stays cheap. An hourly job rolls raw rows into the hourly tier
  and re-aggregates hourly→daily exactly (never average-of-averages)
  before the existing raw-row purge runs, so nothing is dropped before it
  is folded. Two new opt-in retention knobs bound each tier independently:
  `persistence.history.retention_hourly` (default 13 months) and
  `persistence.history.retention_daily` (`0` = keep forever). This is the
  backend foundation the `/api/v1/energy` endpoint below reads from.

- **`GET /api/v1/energy` — per-device power/energy breakdown.** Reads the
  hourly/daily measurement-history rollup tiers (this release, see below)
  and folds them into a per-device breakdown: `POWER` samples
  become `avg_power_w`/`peak_power_w`, `ENERGY_COUNTER`/
  `ENERGY_COUNTER_FEED_IN` become `consumed_wh`/`feed_in_wh` bucket deltas.
  A meter reset within a bucket (`last < first`) reports the delta as the
  counter's post-reset value and sets `reset` — never a negative delta.
  `?group=hour|day|month` selects the bucket granularity (month
  re-aggregates the daily tier); `?device=<address>` optionally scopes to
  one device. Requires the opt-in history feature
  (`persistence.history.enabled`); the route is absent (404) when
  disabled, same as `/history`. REST API version bumped to 2.12.0.

- **`hmcli` gains a `devices` command group + shared REST client.** The admin
  CLI moved onto the Cobra framework with a root carrying shared connection
  flags (`--host` default `http://localhost:8119`, `--token`, `--user`,
  `--password`, `--timeout`); the existing `version`/`config`/`cache`/
  `export-def` commands are unchanged. New: `hmcli devices list|get|get-value|
  set` — list the inventory, read a device / datapoint, and flip a value from
  the shell, each with `--json` for scripting. Pure REST client (no daemon
  change). The `cache clear` online default host was corrected to
  `http://localhost:8119` (was `:2121`). (More command groups — `sysvar`,
  `program`, `paramset`, `events tail` — are follow-ups.)

- **`hmcli` gains an `events tail` command.** Connects to the daemon's
  `/api/v1/events` WebSocket stream and prints live events until interrupted.
  `--topics` (comma-separated; default `*`) controls the subscribe filter;
  `--json` emits each event as a compact JSONL line for scripting; `--classify`
  requests inline `category`/`data_point_type` on datapoint value-changed
  payloads. `--timeout` bounds the handshake only — the stream itself runs
  indefinitely until the connection closes or Ctrl-C / SIGTERM. This completes
  the B1 `hmcli` command surface: `devices`, `sysvar`, `program`, `paramset`,
  `events`. No daemon change.

- **`hmcli` gains `sysvar`, `program`, and `paramset` command groups.** Three
  new command groups extend the admin CLI: `hmcli sysvar list|get|set|fetch`
  manages CCU system variables (list with type/value table, read, write a
  runtime value, force re-pull with optional `--central` scope); `hmcli program
  list|get|run|enable|disable` lists programs, reads one, triggers execution,
  and toggles the active flag; `hmcli paramset get|set` reads or writes a device
  paramset (VALUES, MASTER, LINK) as a sorted key=value table. All commands
  support `--json` for scripting and share the same `--host`/`--token`/`--user`/
  `--password`/`--timeout` connection flags. Pure REST client — no daemon change.

- **Scheduled / automatic CCU backups (`backup:`).** A new opt-in config
  section runs an automatic backup of each configured CCU on an interval
  (`backup.schedule`, e.g. `24h`; off by default) via a per-central
  `central.scheduled_backup` scheduler job. Old backups are rotated per CCU
  (`backup.keep_last`, keep the newest N; `0` keeps all). The archives appear
  in the same Backups view as manual backups; a failed backup surfaces on the
  `scheduler.failures` gauge and is retried on the next tick without crashing
  the daemon. Off-box targets (S3/WebDAV) remain out of scope. No API change.

- **Inbound webhook (`north.webhook.inbound`).** Two new REST endpoints let
  external systems drive the CCU: `POST /api/v1/webhook/value` sets a datapoint
  value and `POST /api/v1/webhook/program` runs a program. Disabled by default;
  the routes are mounted only when `north.webhook.inbound.enabled` is set
  (restart-required, 404 otherwise). Authorization requires an operator identity
  via the normal auth chain **or** the optional inbound bearer token
  (`north.webhook.inbound.token`, constant-time compared) — so a header-only
  caller (e.g. a doorbell) can POST without a session or user login. These are
  real device writes / program runs, reusing the same write/trigger paths as the
  equivalent REST endpoints. REST `APIVersion` → **2.11.0**.

- **`additional_information` on per-DP MQTT state and the REST datapoint DTO.**
  Data points that expose enriched model metadata (currently the calculated
  operating-voltage sensor's battery info — type, quantity, low-voltage limits)
  now publish it under an optional `additional_information` object on both their
  MQTT state topic and the REST `DataPointSummary` (`GET /devices/.../datapoints`).
  Strictly additive: the key is omitted for plain scalar DPs, so existing
  payloads are byte-identical. The REST datapoint schema gained the optional
  property (`assets/openapi.yaml`); REST `APIVersion` → **2.10.0**. Documented
  in `docs/mqtt-topic-schema.md`. (Exposing the same metadata on the hub
  service-/alarm-message aggregates remains a planned follow-up — see
  `docs/parity/by_design.md` §A1-BD01.)
- **MCP `list_incidents` tool.** The Model Context Protocol bridge gains a
  read-only `list_incidents` tool that projects the reliability incident
  journal (circuit-breaker trips, ping/pong mismatches, retry exhaustion) to
  LLM agents — the same enriched, source-tagged data `GET /incidents` serves
  REST clients. Newest-first, limit-clamped (default 50, max 1000), with an
  optional `central_name` filter. Read-only (not gated by `allow_writes`);
  available whenever the incident store is wired and `north.mcp.enabled`.
- **Outbound webhook bridge (`north.webhook`).** A new north-bound adapter
  that POSTs a signed JSON payload to an operator-configured URL on datapoint,
  system-status and incident events. Disabled by default. Each delivery carries
  an HMAC-SHA256 body signature (`X-OpenCCU-Signature: sha256=…`, GitHub-webhook
  convention) when a secret is set, plus `X-OpenCCU-Event` and a unique
  `X-OpenCCU-Delivery` header. Deliveries run off a bounded queue with jittered
  exponential-backoff retry, so a slow or unreachable endpoint never blocks the
  event bus. Filters: per-event-type allowlist, per-CCU allowlist, and an
  optional parameter glob on datapoint events. A single endpoint is supported;
  multi-endpoint fan-out is a planned follow-up. Webhook config changes are
  restart-required (the bridge is wired once at boot). A new `incident.recorded`
  event lets reliability incidents flow to the webhook alongside the existing
  diagnostics surface.

- **Energy view (`#/energy`).** A new SPA route renders the `/api/v1/energy`
  aggregation: a central + group (hour/day/month) selector with 24h/7d/30d/12mo
  range presets, total consumed/feed-in cards (Wh→kWh), an inline
  consumption-over-time chart with an all-devices/per-device toggle, and a
  per-device breakdown table (consumed/feed-in kWh, avg/peak power). Buckets
  where a meter reset occurred carry a reset badge. Shows a "history recording
  is off" state with a settings link when the history feature is disabled.

- **Cross-CCU fleet overview (`#/fleet`).** A new read-only SPA route lists
  every configured CCU at a glance: online/offline availability, host +
  CCU-reported hostname, model/version/serial, per-central device count, a
  chip per configured interface, and an "Open CCU WebUI" link. Reflects
  CCUs adopted live (see below) without a page reload. No REST/API change.

- **Access-control view (`#/access`).** A new admin-only SPA route manages
  Basic-auth users and API tokens from the browser instead of hand-editing
  `config.yaml`: add/edit a user (role `viewer`/`operator`/`admin` + optional
  password), delete via the shared confirm dialog, create an API token with a
  copy-once plaintext reveal, and delete a token by fingerprint. Frontend-only
  — the underlying REST CRUD routes already existed.

- **Whole-home device overview (`#/overview`).** A new top-of-nav SPA route
  renders the existing auto-tile dashboard across every device at once,
  grouped by room / function / CCU and filterable, with per-group lazy
  loading so the page starts interactive immediately. Rooms are never merged
  across CCUs. No new REST endpoint.

- **Live CCU adopt / remove — no daemon restart.** Adding or removing a CCU
  through the REST central-admin path (`POST /api/v1/centrals`, `DELETE
  /api/v1/centrals/{name}`) now brings the southbound connection, model,
  scheduler jobs, and CCU auth up or down live, instead of only taking effect
  after a restart. `centrals` no longer appears in the config
  restart-required diff for a pure add or remove — only an in-place edit of
  an existing central still requires a restart. Device-icon proxying and the
  cross-CCU fleet list (`GET /api/v1/system/ccu`) resolve a runtime-adopted
  central immediately as well.

- **Matter bridge enforces server-side timed-interaction conformance.** A
  command tagged "Timed Required" in the Matter model (currently
  `AdministratorCommissioning`'s `OpenCommissioningWindow` /
  `OpenBasicCommissioningWindow` / `RevokeCommissioning`, Matter §8.7) is now
  rejected with `NEEDS_TIMED_INTERACTION` when invoked outside a timed window,
  even if the controller left the request's own Timed flag clear. Previously
  the bridge only honoured the controller-asserted flag.

### Changed

- **North-bound bridges (MQTT, Matter, REST, MCP, webhook) migrated onto a
  shared, phased-start `bridge.Registry`.** Each surface now registers as a
  `Service` (`Name`/`Start`/`Stop`) instead of being hand-wired in
  `cmd/openccu-loom`; the registry starts `PhaseEarly` services (MQTT, so the
  boot-time retained-state snapshot still reaches the broker before
  southbound hydration) before `PhaseLate` ones (Matter, REST, webhook),
  rolls back already-started services on a mid-start error, and stops
  everything in reverse order on shutdown. Internal lifecycle unification —
  no user-facing behaviour change.

## [0.21.3]

### Changed

- **Default REST / Config-UI port is now `8119` (was `8080`) in all environments.**
  8080 is a busy, commonly-claimed port; the daemon now defaults
  `north.rest.listen` to `:8119` everywhere (binary, Docker, both add-ons).
  Override with `north.rest.listen` / `OPENCCU_LOOM_REST_LISTEN` as before.
- **HA add-on: all three ports are now operator-configurable options.**
  `rest_port` (8119), `xmlrpc_callback_port` (8120) and `binrpc_callback_port`
  (8129) are add-on options (passed through to the daemon's env overrides in
  `run.sh`), instead of a `ports:` mapping block that did nothing under
  `host_network: true`. The inert `ports:`/`ports_description:` block was
  removed. `ingress_port` stays static at 8119 and must equal `rest_port` for
  the Ingress panel — change `rest_port` only for direct-access setups.

## [0.21.2]

### Fixed

- **A CCU configured by host (e.g. `localhost`) is now recognised as already
  configured by discovery.** The discovery "already configured" check matches by
  serial, but centrals created before serial capture (or added manually) carry no
  stored serial, and a host match can never succeed for a `localhost`-configured
  central against the CCU's real discovered IP — so the same CCU showed up as a
  fresh discovery. The daemon now backfills a central's serial from its live ReGa
  connection at bring-up (the same canonical 10-character form discovery
  produces, resolved in `hub_wiring`), writing only the serial column and only
  when empty. After a central connects once, discovery recognises it by serial
  regardless of host. No wire-schema change (`APIVersion` stays 2.9.0).

## [0.21.1]

### Changed

- **CCU discovery now identifies a CCU by its canonical 10-character serial.**
  The serial captured from SSDP/UPnP (the UDN tail, which is longer than 10
  characters) is reduced to its last 10 characters — the same canonical form the
  runtime `system.GetSerial` reader produces — so a discovered CCU and a
  configured central are compared by an identical string. Both producers now
  funnel through one helper (`routingkey.CanonicalSerial`), guaranteeing the two
  serial worlds line up. The "already configured" check still falls back to a
  host match, and also canonicalises a stored full serial at compare time so a
  central adopted under 0.21.0 (which stored the untruncated serial) keeps
  matching. No wire-schema change (`APIVersion` stays 2.9.0).

## [0.21.0]

### Changed

- **CCU discovery now matches "already configured" by serial, not host**
  ([ADR 0046](docs/adr/0046-ssdp-ccu-discovery.md)). Adopting a discovered CCU
  persists its hardware serial (new `serial` column on the centrals store,
  migration 024); the discovery list then flags a CCU as already configured by
  that stable serial, so a CCU whose IP changed (DHCP lease, rotating docker
  address) is no longer offered as a duplicate. Centrals configured before this
  release carry no serial and continue to match by host.
- **Discovery suggests a stable adoption address.** Each discovered CCU now
  carries a `suggested_host` the SPA pre-fills instead of the raw SSDP IP:
  - `localhost` when the CCU is on the daemon's own host (its discovered IP is
    one of the daemon's interface addresses), and
  - on the supervised HA add-on, a reverse-DNS-resolved **container hostname**
    when the CCU is reachable only on a docker-range IP (172.16.0.0/12) — the
    docker IP rotates, the hostname does not.
  The raw host is still shown, and is used unchanged when no better address
  applies. `APIVersion` → 2.9.0 (added `suggested_host` / `serial` fields).

## [0.20.1]

### Fixed

- **HA Ingress users are no longer trapped in the first-run setup wizard.**
  After onboarding moved into the SPA ([ADR 0045](docs/adr/0045-login-and-onboarding-into-spa.md)),
  the boot probe decided wizard-vs-app purely from the persistent first-run
  state and ignored the request's identity. Behind Home Assistant Ingress the
  operator is already authenticated as admin via the Supervisor passthrough
  ([ADR 0044](docs/adr/0044-single-port-onboarding-and-ha-ingress-auth.md)),
  yet the SPA still showed the onboarding wizard — which could not be completed
  because the Ingress session lapsed mid-flow, then dead-ended on a login
  screen the Ingress-only operator has no credentials for. Now `GET
  /api/v1/setup/status` reports `required: false` whenever the caller already
  carries an authenticated identity, and the SPA only renders the wizard when
  no one is logged in. Guided CCU/MQTT configuration under Ingress happens in
  Settings → CCUs (with the SSDP auto-discovery added in 0.20.0).
- **A momentarily lapsed session no longer dead-ends the SPA on the login
  screen.** On a `401` the SPA now re-probes `/auth/me` before giving up: under
  Ingress the Supervisor re-authenticates the request and the session survives;
  a genuinely stale session still falls through to the login view as before.

## [0.20.0]

### Added

- **CCUs are now discovered automatically on the LAN via SSDP/UPnP**
  ([ADR 0046](docs/adr/0046-ssdp-ccu-discovery.md)). The daemon periodically
  multicasts an M-SEARCH, follows each Homematic/OpenCCU central's
  `basic_dev.cgi`, and surfaces the matches in the UI — in Settings → CCUs and
  in the first-run onboarding wizard — so a CCU can be added with its host
  pre-filled instead of typed by hand. Found CCUs already configured are
  flagged; unwanted ones can be **ignored** (persisted, so they stop
  reappearing) and un-ignored later. Discovery accepts OpenCCU **and** classic
  eQ-3 / HomeMatic / RaspberryMatic centrals.
- New REST endpoints: `GET /api/v1/centrals/discovered`,
  `GET /api/v1/centrals/discovered/ignored`, and admin-gated
  `POST|DELETE /api/v1/centrals/discovered/{serial}/ignore`. REST `APIVersion`
  → `2.8.0`.
- New config `north.discovery.ssdp` (`enabled`, default true; `interval`,
  default 60s). Discovery only reads the network — nothing about the daemon
  leaves the LAN — and finds nothing where multicast is unavailable.

## [0.19.0]

### Changed

- **Login, OIDC, and first-run onboarding now live entirely in the Svelte
  SPA** ([ADR 0045](docs/adr/0045-login-and-onboarding-into-spa.md)). The SPA
  already handled login and OIDC; the four-step first-run wizard (admin →
  language → CCU → MQTT) is now a SPA view (`Setup.svelte`) that finalizes
  through a single atomic `POST /api/v1/setup`. The previous server-rendered,
  no-JS login/setup forms have been removed. The server-rendered surface
  shrinks to `/health` and `/about`, kept only as a no-JS diagnostic anchor for
  when the SPA bundle cannot load.
- REST `APIVersion` → `2.7.0` (capability addition: the setup endpoints).

### Added

- **`GET /api/v1/setup/status`** and **`POST /api/v1/setup`** — the SPA probes
  status on boot to decide between the onboarding wizard and the login screen,
  and finalizes onboarding atomically. `POST /api/v1/setup` is unauthenticated
  by necessity (no admin exists yet) but hard-gated: it returns 409 once any
  authentication source exists, so a second admin can never be registered
  through it.
- A dedicated per-IP login brute-force speed-bump on `POST /api/v1/auth/login`
  (preserved from the removed server-rendered login, now a REST middleware).

### Removed

- Server-rendered `/login`, `/logout`, `/login/oidc/*`, and the `/setup*`
  wizard, plus their templates and the wizard session store.

## [0.18.5]

Bundles the work developed as 0.18.1–0.18.5 (none were tagged individually).

### Fixed

- **Matter colour and colour-temperature writes now work.** The ColorControl
  command handlers (`MoveToHue` / `MoveToSaturation` / `MoveToHueAndSaturation`
  for RGB lights, `MoveToColorTemperature` for tunable-white lights) decoded the
  command payload into the wrong shape, so every colour or CT change requested
  from a Matter controller (Apple Home / Google Home colour picker) was silently
  rejected and the bulb never changed. The bridge now decodes ColorControl
  commands into typed requests and the cluster servers consume them, with an
  end-to-end test that exercises the real TLV-payload → dispatcher → device path.
  Read-side attributes (current hue/saturation/CT) were already correct, so this
  is a write-path-only fix.

### Added

- **Scheduler health component.** The per-central health view now carries a
  `scheduler` liveness row: if a periodic job has failed since the last health
  heartbeat the component reads degraded for that interval and recovers once a
  quiet interval passes (a failure delta against the cumulative
  `scheduler.failures` metric). This completes the health-producer coverage
  (MQTT, Matter and SQLite already reported status; REST and WebSocket stay
  metric-only by design, since their liveness is implied by the daemon serving
  `/health`).

### Changed

- **"Reload device" now refetches just that device.** Reloading a single device
  from the UI previously pulled the descriptions for *every* device on the same
  CCU interface; it now fetches only the target device and its channels
  (`GetDeviceDescription` over the device plus its `CHILDREN`). One unreachable
  channel is logged and skipped rather than failing the whole reload. The
  operator-visible result is unchanged — only less work on the CCU, which
  matters on large installations.
- **API schema completeness.** Nine response shapes the web UI carried as
  hand-written types — backups, incidents, rooms, functions, linkable channels,
  RPC-recording status, live-log records, edit-session, and inbox devices — are
  now defined in `assets/openapi.yaml` and consumed by the SPA from the
  generated client, so the documented contract and the UI stay in sync (API
  contract 2.5.0 → 2.6.0, additive). Two latent UI/daemon mismatches were
  corrected in passing: the inbox device's `central` field is optional (matching
  what the daemon serves), and the install-mode interface entry now carries its
  `observed` flag.

### Removed

- **Internal only:** deleted unused scaffolding that never had a production
  caller — the standalone `health.Connection`/`ConnectionRegistry` model
  (superseded by the live health tracker), the `ClientCoordinator.PollClients` /
  `SubscribeToHealthEvents` / `RestartClients` methods, and the never-published
  `DeviceStateChangedEvent`. None were part of the REST/WebSocket contract, so
  there is no API change and downstream clients are unaffected; the concept each
  embodied is recorded in `docs/parity/by_design.md`.

## [0.18.0]

### Added

- **Combined data points are now published.** Cover blinds expose a combined
  level/slats position and HmIP RGB lights expose a combined hue/saturation as
  their own data points — materialised through the device pipeline and surfaced
  as Home Assistant discovery sensors over MQTT (plus REST/WebSocket). (Mapping
  HS colour onto the Matter ColorControl cluster is a follow-up.)
- **Service and alarm messages carry a human-readable `display_name`.** The
  message code (e.g. `LOW_BAT`, `UNREACH`) is now translated (de/en) and added
  as an additive `display_name` field on the messages REST/SPA surface
  (north-bound API contract 2.4.0 → 2.5.0). The raw MQTT attribute form is
  unchanged, so existing automations keep working.

### Removed

- **Dropped the speculative HmIP-COOK ("hood") custom data point.** It had no
  counterpart in any of the HomeMatic reference sources — a placeholder added in
  the very first commit — and was never registered, so it was dead code.

## [0.17.5]

### Fixed

- **Matter: string attribute writes are no longer dropped to empty.** A UTF-8
  attribute write (e.g. `NodeLabel`) was read from the wrong decoded field and
  always stored as the empty string; it now keeps the written value.
- **Matter conformance sweep against matter.js HEAD.** Mirrored five upstream
  fixes the schema snapshot does not track: truncate decoded character strings
  at Information Separator 1 / 0x1F (§7.19.2.40); reject a write that carries a
  DataVersion against a wildcard endpoint, and clamp the status when a
  cluster-specific status is returned (§8.9.2.8.1 / §7.10.7); reject illegal
  Read/Subscribe paths (wildcard cluster with a concrete non-global attribute
  or event) up front with InvalidAction (§8.4.3.2); strip `IsUrgent` from event
  report paths (§8.9.3.4); and reject a VendorID outside 0x0001-0xFFF4 as an
  invalid device identity.

### Changed

- **Matter schema snapshot now records its provenance.** The embedded
  `matter-schema-snapshot.json` captures the matter.js source commit and the
  Matter spec revision (1.5.1) it was extracted at, so the pinned reference is
  traceable to an exact upstream commit.

## [0.17.4]

### Fixed

- **Device-detail header no longer collapses on a narrow content area.** When
  the page was shown in a narrow container (most visibly inside the Home
  Assistant Ingress iframe, where the sidebar leaves little width), the action
  buttons stayed on one row and squeezed the title/metadata column to a sliver,
  wrapping the model and description character-by-character. The header now uses
  a container query: it stacks (title + metadata on their own full-width rows,
  actions below) until the content area is wide enough, then restores the
  side-by-side layout with the actions top-right.

### Changed

- **Unified the list-view page headers behind a shared `PageHeader` component.**
  Devices, firmware, signal quality, system variables, programs, messages,
  inbox, un-ignore, backups, and the audit log now render their title +
  actions through one component with a single, robust responsive rule (the
  title never shrinks, so a crowded header wraps its actions onto their own
  line instead of squeezing). Behaviour is unchanged; the device-list title
  drops a stray `tracking-tight` to match the other headers.

## [0.17.3]

### Changed

- **Refreshed the embedded openccu-data extracts to 2026.6.1** — updated device
  translations (`translation_extract`) and easymode definitions
  (`easymode_extract`), with the manifest hashes regenerated to match. 2026.6.1
  also restores the curated model labels for HmIP-DLP, HmIP-UDI-SMI55,
  HmIP-SMO230 and HmIP-SWDO-PL-2 (the extract ships those models only as icons).

## [0.17.2]

### Fixed

- **Removed devices now disappear from Home Assistant immediately.** When a
  device was deleted from the CCU while the daemon was running, its MQTT
  HA-Discovery configs lingered on the broker — so the entities stayed visible
  as permanently "unavailable" until the next daemon restart (when the boot
  orphan-cleanup pass evicted them). The device-removed handler now retracts
  the device's discovery configs right away (empty retained payload), scoped to
  the device's own node_id so unrelated entities are never touched.

## [0.17.1]

### Fixed

- **Battery values now appear in the Signal-quality table.** The battery
  level read the derived `OPERATING_VOLTAGE_LEVEL`, but that is a *calculated*
  data point and the lookup only searched the VALUES paramset — so the column
  was always empty (most visibly for HmIP). The maintenance-channel lookup now
  also searches calculated data points. Low-battery detection additionally
  recognises HmIP's `LOWBAT` parameter (previously only BidCos `LOW_BAT`).

### Changed

- **Every sensible table column is now sortable.** Across all DataTable views
  (devices, firmware, signal quality, system variables, programs, messages,
  inbox, un-ignore, backups, audit, Matter, the settings admin tables, and the
  Diagnostics sub-tables) every data column now sorts on header click;
  selection and action columns stay non-sortable. Timestamp columns sort on
  their raw value while still rendering the formatted date.

## [0.17.0]

### Added

- **Shared, sortable, searchable tables across the Config UI.** A new
  `DataTable` design-system component gives every list view one consistent
  interaction model: click a column header to sort (▲/▼, keyboard-operable),
  a built-in search field, a responsive layout that reflows to cards on
  phones, and persisted sort/search. The device, system-variable, program,
  firmware, and signal-quality views all render through it.
- **Signal quality view (own nav entry).** Per-device RF reception strength
  (RSSI_DEVICE / RSSI_PEER, colour-coded by quality), **battery** state
  (colour-coded ≤20 % red / ≤55 % amber, from `OPERATING_VOLTAGE_LEVEL`),
  and reachability — searchable and sortable. Moved out of the Diagnostics
  page into its own top-level entry. Backed by `battery_level` / `low_battery`
  added to `GET /diagnostics/rssi` (API contract 2.3.0 → 2.4.0).

### Changed

- **Device list: cards ↔ table toggle.** The former single-column "list"
  mode is now a real table (multi-select, interface grouping, bulk actions
  preserved); the card grid stays as the alternative view. The view mode,
  search, sort, and filters are all remembered across reloads.
- **Firmware, programs, and system variables are now tables** with click-to-
  sort headers and search, replacing the previous card grids. System
  variables keep their inline value editor (now in the value cell). Each
  view also remembers its search, sort, and central/mode filter.
- **The remaining list/table surfaces now share the same DataTable too**:
  messages, the device inbox, the un-ignore picker, backups, the audit log,
  the Matter exposure list (with multi-select) and fabrics, the Settings
  admin tables (users, API tokens, CCUs, rooms/functions), and the
  Diagnostics sub-tables (interfaces, client health, RPC recordings) — all
  sortable and consistently styled.

## [0.16.1]

### Fixed

- **RF reception (RSSI) overview now works for HmIP devices.** The 0.16.0
  feature read the BidCos-RF-only XML-RPC `rssiInfo` matrix, which HmIP-RF
  answers with a generic fault — so HmIP setups saw "no data" after a
  multi-second delay. It now reports **per-device** reception strength
  (`rssi_device` = RSSI_DEVICE, `rssi_peer` = RSSI_PEER, dBm) plus
  reachability, read from the device model's maintenance channel with no CCU
  round-trip, so it covers HmIP and BidCos alike. The Config-UI Diagnostics
  section became a per-device "Signal quality (RSSI)" table that loads with
  the page.

### Changed

- **`GET /diagnostics/rssi` / `ccu.get_rssi_info` response shape.** Each
  device entry now carries `rssi_device`, `rssi_peer`, and `reachable`
  directly instead of a nested `partners` list. North-bound API contract
  version 2.2.0 → 2.3.0 (the removed `partners` field is the only
  non-additive part; oasdiff classifies it as non-breaking).

## [0.16.0]

### Added

- **RF reception matrix — WS command, REST endpoint, and Config-UI view.**
  Exposes the CCU's pairwise RF reception matrix (device ↔ communication-
  partner RSSI pairs) read from `Interface.rssiInfo`, across every central
  and RF interface; the CCU's 65536 "no data" sentinel is normalised to
  `null`. Reachable via the `ccu.get_rssi_info` WebSocket command, the
  admin-only `GET /diagnostics/rssi` REST endpoint, and an on-demand
  "Signal / RSSI matrix" section on the Config-UI Diagnostics page.
  Complements `ccu.get_signal_quality` (a flat per-device list) with the
  link-level matrix the CCU reports directly. Bumps the north-bound API
  contract version to 2.2.0.

### Changed

- **JSON-RPC permission errors are now distinguishable from generic
  failures.** A CCU JSON-RPC `error.code 400` ("access denied" —
  authenticated but the session's privilege level is too low) maps to the
  new `ErrPermissionDenied` sentinel and short-circuits retry, instead of
  being retried as a generic client exception. Surfaces a mis-configured
  user level instead of silently hammering the CCU.
- **SPA list search accepts regular expressions.** The device, system-
  variable, and program list filters now match by regex when the term is a
  valid pattern (e.g. `BidCos-RF\.MEQ`, `MEQ|HEQ`), and fall back to a
  case-insensitive substring match otherwise.
- **SPA favorites became a quick-control surface.** Pinned system variables
  can now be changed inline (toggle / number / select) directly on the
  start page instead of only linking back to the sysvar list. Inactive
  programs are visually dimmed in the program list for faster scanning.

### Removed

- **`north.ui.listen` config setting + `OPENCCU_LOOM_UI_LISTEN` env var.** The
  bootstrap UI (login / setup / health / about) has shared the REST listener
  since 0.14.0 (ADR 0044), so the separate UI bind address was a deprecated
  no-op. It is now removed entirely — config struct, bootstrap tier, the
  startup deprecation warning, restart-required handling, and the SPA i18n
  entries. `north.ui.enabled` (the bootstrap-surface on/off toggle) stays.
  Delete `north.ui.listen` from existing config files; the loader already
  ignores unknown keys, so no migration is required.

## [0.15.0]

### Added

- **`firmware.refresh` WebSocket command is now wired.** It re-pulls device
  descriptions (and with them the firmware-version fields) from the CCU across
  every central and interface. Previously the command was registered only as a
  no-op stub (the `FirmwareRefresher` was left nil). Mirrors aiohomematic
  `ws_refresh_firmware_data`.
- **Optional Matter TimeSynchronization cluster (0x0038), operator opt-in.**
  New `north.matter.enable_time_sync` flag (tri-state, default off) mounts the
  already-implemented TimeSynchronization cluster on the Matter Root endpoint.
  Off by default because it is optional-only on a RootNode and some controllers
  (e.g. Apple Home) may reject the bridge at pairing when it appears; enable
  only when a controller needs a time-sync surface. ARL (0x002B) and Actions
  (0x0025) remain intentionally deferred (no current use-case) — see
  `docs/parity/by_design.md`.
- **Matter cluster-revision parity tests** added for previously-untested
  clusters, closing the documented behavioural-parity test gaps against the
  matter.js schema snapshot.

### Changed

- **A manual device reload now also refreshes link-peer addresses.**
  `config.reload_device_config` / `ccu.reload_device_config` re-pull the device's
  link peers as part of the (already RPC-bound) reload, keeping link data current
  on demand — without adding a boot-time per-device RPC sweep across the whole
  fleet.

### Fixed

- **`GET /api/v1/incidents` now returns the recorded incidents.** The REST
  incident reader was wired to an empty MVP stub, so the endpoint (and the
  diagnostics envelope) always returned an empty list even though incidents were
  being persisted to SQLite. It now reads from the incident store across every
  central. Dead stub code (`ParamsetsAdapter`, `IncidentsAdapter`) removed.
- **OpenAPI spec now matches the server's actual JSON, removing the hand-written
  type divergence in the SPA.** Seven response schemas drifted from what the Go
  handlers emit — `openapi.yaml` was missing fields the server always sends
  (`DeviceSummary.central`/`model_label`/`model_icon`/`update_available`/
  `functions`/`has_sub_devices`/`master_pushes_config_pending`; `central` on
  audit/alarm/service entries; alarm `address`/`state_value`; service
  `description`/`priority`) and over-constrained others. The spec is corrected to
  the Go json tags, the generated client types are regenerated, and the SPA's
  `TODO(openapi-typescript)` overrides are removed (it now re-exports the
  generated types). Composite-key call sites guard the optional `central`. REST
  API version bumped 2.0.0 → 2.1.0 (additive).

## [0.14.6]

### Fixed

- **CLI tools now honor `OPENCCU_LOOM_DATA_DIR` without `--config` too.**
  Completing the 0.14.5 daemon fix: `hmcli` subcommands (`backup`, `config`)
  resolved their data directory via the bootstrap default `./var` when run
  without `--config`, so a containerised CLI run could open the wrong (empty)
  database instead of the daemon's `/data` store. `BootstrapConfig` now applies
  the bootstrap-tier env overlay (`OverlayFromEnv`), and `loadBootstrapForCLI`
  calls it (and validates), so the CLI and daemon resolve the same data
  directory.

## [0.14.5]

### Fixed

- **Critical: configuration and database were lost on every restart / add-on
  update.** When the daemon started without a config file — the standard HA
  add-on case — it used `config.Default()`, which ignored the environment
  overlay and therefore `OPENCCU_LOOM_DATA_DIR`. The add-on sets that to `/data`
  (the only persistent location), but the daemon fell back to the default
  `./var` inside the ephemeral container, so the SQLite database lived there and
  every restart/update started on an empty database — losing configured CCUs,
  users and all SPA-edited config. The no-config-file path now applies the env
  overlay (`config.DefaultWithEnv`), so `OPENCCU_LOOM_DATA_DIR` is honoured and
  state persists under `/data`. **After updating, re-create your CCU and admin
  user once more; from then on they survive restarts and updates.**

## [0.14.4]

### Fixed

- **mDNS discovery no longer advertises container-internal addresses.** When the
  daemon runs as a Home Assistant add-on with host networking, the zeroconf
  advertiser put every host interface IP into the A-record — including the
  `hassio`/Docker bridge addresses (e.g. `172.30.232.1`). A discovering client
  (homematicip_local) resolved the daemon to that internal address and failed to
  connect (`[404] GET http://172.30.232.1:8080/api/v1/info`). The advertiser now
  publishes only routable LAN addresses (interfaces named like `docker*`, `br-*`,
  `veth*`, `hassio`, `virbr*`, … are excluded, as are loopback/link-local IPs)
  while still broadcasting on every interface so peers on a container bridge
  still receive the announcement. If no routable address survives the filter it
  falls back to the previous all-interfaces behaviour rather than going silent.
- **Config fields without a description / translation.** The REST `public_url`,
  `tls_cert_file` and `tls_key_file` fields rendered with a machine-humanised,
  untranslated label and no help text. They now have proper labels and inline
  help in English and German (the TLS help also explains that the cert/key paths
  are both the HTTPS on/off switch and the location an uploaded certificate is
  written to and hot-reloaded from).

### Added

- **Guard against undescribed config fields.** `TestConfigFieldsHaveLabelsAndHelp`
  (in `tests/contract/`) walks every `cfg`-tagged config field and fails the
  build unless each has a `config.field.<path>` label AND a `config.help.<path>`
  description in both the EN and DE catalogues of `assets/ui/src/lib/i18n.ts`. A
  matching Critical Rule was added to `CLAUDE.md`.

## [0.14.3]

### Changed

- **The HA Ingress auth passthrough is now ON by default in the HA add-on.**
  `north.rest.auth.ha_ingress.enabled` became tri-state: unset → on when the
  build is supervised (the HA add-on, where `panel_admin: true` restricts
  Ingress to HA admins), off in a plain build / Docker image; an explicit
  true/false still overrides. Opening the add-on through the Home Assistant
  panel now logs you straight in as admin — no setup, no login page — which
  also avoids the first-run `/setup` redirect. All the gates are unchanged
  (supervised + real RemoteAddr in `trusted_proxy_cidr` + `X-Ingress-Path`; a
  real token/session still wins), so it stays inert outside genuine Supervisor
  traffic (e.g. the CCU/RaspberryMatic add-on, which is not behind Ingress).
  Set `enabled: false` to opt out and use local/CCU login instead.

## [0.14.2]

### Fixed

- **Onboarding works behind HA Ingress.** Folding the server-rendered bootstrap
  surface (login / setup / about) onto the Ingress port in 0.14.0 exposed its
  absolute URLs to the Ingress prefix: form POSTs (e.g. `/setup/admin`) and
  links (`/app/`, `/login`) resolved against the Home Assistant origin instead
  of the add-on's Ingress path — so the setup wizard could not be submitted (you
  stayed stuck on it) and the footer `/app/` link reloaded the whole HA page
  inside the panel. The bootstrap pages now emit Ingress-prefix-aware URLs (a
  `<base href>` derived from `X-Ingress-Path` plus relative links), and every
  server-side redirect carries the prefix.

## [0.14.1]

### Fixed

- **First-run redirect no longer traps CCU/OIDC users on `/setup`.** 0.14.0
  redirected the SPA entrypoint to the setup wizard whenever no *local* admin
  existed — but in the HA add-on CCU-delegated login (ADR 0043) is on by
  default, so operators authenticate with their CCU account and never create a
  local admin. They landed on the "Create administrator account" wizard after
  updating. The redirect now fires only when there is genuinely no way to
  authenticate: no local user (YAML or DB) AND no CCU auth AND no OIDC.
- **Setup wizard step indicator showed `Step {current} of {total}` literally.**
  The server-rendered `t` template helper could not substitute placeholders. It
  now accepts `(name, value)` pairs and the wizard passes the step numbers, so
  it reads e.g. "Step 1 of 4".

## [0.14.0]

### Added

- **Opt-in Home Assistant Ingress auth passthrough (`north.rest.auth.ha_ingress`,
  ADR 0044).** When running as the supervised HA add-on, a request proxied by
  the HA Supervisor can be accepted as an authenticated admin without a local
  login. OFF by default; it only activates when all of these hold: the build is
  supervised, the request's real peer (`RemoteAddr`, never `X-Forwarded-For`)
  is inside `trusted_proxy_cidr` (default `172.30.32.0/23`), and the
  Supervisor's `X-Ingress-Path` header is present. A real Bearer token or
  session always wins (the passthrough is a fallback). Security contract: the
  add-on keeps `panel_admin: true`, so only HA admins reach Ingress. Audited as
  scheme `ingress`, subject `ha-ingress`.

### Changed

- **Single-port onboarding.** The server-rendered bootstrap surface (login,
  first-run `/setup` wizard, about, OIDC) is now served on the REST/SPA
  listener (`:8080`) instead of a separate `:8081` server, so the entire
  onboarding works through one port / HA Ingress. On first run (no admin user)
  the SPA entrypoint redirects to `/setup`. The HA add-on no longer exposes
  `8081/tcp` and pins `panel_admin: true`. **`north.rest.listen` is unchanged;
  `north.ui.listen` is deprecated** — it is ignored (with a startup warning);
  the UI shares the REST listener.

### Fixed

- **Missing config-field descriptions in the SPA.** The CCU-auth fields
  (`north.rest.auth.ccu.*`) rendered without a description because their
  `config.help.*` i18n keys were absent. Added labels + descriptions (en + de)
  for every CCU-auth and HA-Ingress field.

## [0.13.3]

### Fixed

- **The Home Assistant add-on image now publishes.** The `home-assistant/builder`
  action proved too fragile across the 0.13.0–0.13.2 attempts: it derives its
  builder-image tag from the action ref, so SHA pins and unpublished
  builder-image versions both 404'd, and once that was resolved the pinned
  version had dropped the `--all` flag the step relied on. Switched to building
  the add-on image with plain `docker/build-push-action` (buildx) — the same
  approach the working `go-daikin2mqtt` add-on uses. The add-on Dockerfile still
  COPYs the daemon binary out of the published release image; `provenance: false`
  keeps it a plain image the HA Supervisor can pull. amd64 only (matching the
  amd64-restricted daemon image).

## [0.13.2]

### Fixed

- **Attempted to publish the Home Assistant add-on image** (completed in 0.13.3).
  Pinned the `home-assistant/builder` action to `@2026.02.1` so its builder image
  resolves, but that version had removed the `--all` flag the step passed, so the
  build still aborted — superseded by dropping the builder action entirely in
  0.13.3. Daemon binaries, Docker image, and CCU/RaspberryMatic add-on were
  unaffected.

## [0.13.1]

### Fixed

- **Attempted to publish the Home Assistant add-on image** (completed in 0.13.2).
  The `home-assistant/builder` action was pinned by commit SHA; the action
  derives its builder-image tag from its own ref, so the SHA pin pulled a
  non-existent `amd64-builder:<sha>` ("manifest unknown") and the 0.13.0 release
  shipped without the HA add-on image. Re-pinned the action by version tag — but
  that version's builder image was itself unpublished, so the fix only landed in
  0.13.2. The 0.13.0 daemon binaries, Docker image, and CCU/RaspberryMatic
  add-on were unaffected.

## [0.13.0]

### Fixed

- **The SPA "CCU login" tab can now actually save.** The CCU-auth provider
  (`north.rest.auth.ccu`) was wired into the SPA — tab, editor, i18n — but the
  section was never registered in the backend's section registry, so a save
  was rejected at the allow-list gate with `API 400 …: north.rest.auth.ccu`
  ("Unknown section"). The section is now registered everywhere its OIDC
  sibling is (`configstore.AllSections`, `applySection`/`marshalSection`,
  `sectionTarget`, and the REST `validateSection`) and flagged
  restart-required. Registering it also lets the editor's longest-prefix guard
  attach the `north.rest.auth.ccu.*` fields to the "CCU login" tab only,
  instead of double-rendering them under the REST tab.
- **Tri-state config toggles no longer collapse their "unset" default.** The
  SPA section editor rendered every `*bool` field (e.g.
  `north.rest.auth.ccu.primary`, whose documented default is "CCU-primary")
  as a plain checkbox, so saving silently wrote `false` for a field the
  operator never touched — overriding the daemon's nil default. `*bool` fields
  now render as a "Default / On / Off" select that preserves the unset state
  (persisted as `null`, letting the daemon apply its own default). Concrete
  `true`/`false` values still round-trip unchanged.
- **Install mode (teach-in for new devices) now works.** Starting pairing from
  the SPA inbox failed with `API 502 /install-mode: adapter: not implemented in
  MVP`. Two gaps combined: the per-interface install-mode data points were
  never constructed in production (so `GET /install-mode/interfaces` was always
  empty and the SPA fell back to a CCU-wide toggle), and that CCU-wide toggle
  was an unimplemented stub. There is no CCU-wide install mode on a CCU — it is
  always per-interface — so one install-mode data point is now wired per
  pairing-capable radio (HmIP-RF, BidCos-RF, BidCos-Wired), each writing to its
  own interface backend. The SPA inbox now lists the radios and opens pairing on
  the selected one.

### Removed

- **The CCU-wide `GET`/`POST /install-mode` endpoints.** They modelled a
  CCU-wide pairing toggle that does not exist on the CCU (install mode is
  per-interface) and were never functional. Use `GET`/`POST
  /install-mode/interfaces` instead. API version bumped to `2.0.0`.

## [0.12.0] - 2026-06-24

### Added

- **CCU as an authentication provider (ADR 0043).** Logins are validated
  against the CCU's own user database (a transient `Session.login`) and the
  CCU `UserLevel` is mapped to a Loom role (8→admin, 2→operator, 1→viewer).
  The CCU is only contacted at login (the issued Loom session carries the
  rest). Configurable via `north.rest.auth.ccu` (also editable in the SPA's
  "CCU login" tab; restart-required) and surfaced as `auth.ccu.v1`:
  - **`enabled`** is tri-state — unset defaults to the build stamp, so the
    CCU add-on ships with CCU login **on** and a plain build keeps it off.
  - **`primary`** is tri-state — unset defaults to **true** (the CCU is the
    primary source, local users are the break-glass fallback); `false`
    flips to local-first. Break-glass holds in both orders because every CCU
    failure (wrong password **or** CCU down) falls through to the local
    store.
- **Config-UI parity push toward the legacy CCU WebUI.** Six gaps the SPA had
  against the CCU WebUI are now closed:
  - **Rooms & functions (Gewerke) management.** Create / rename / delete rooms
    and functions from a new Settings → "Groups" tab, on top of the existing
    device-assignment path. CCU-side via new ReGa scripts
    (`create_room`/`rename_room`/`delete_room` + function equivalents), exposed
    as `POST/PATCH/DELETE /rooms` and `/functions` (operator-gated, per-CCU).
  - **Favorites / start page.** Pin devices (and system variables) to a new
    Favorites view; persisted server-side per user via a generic
    `/me/preferences/{key}` store so they follow the operator across browsers.
  - **Direct-link sender side.** The link editor now renders the sender-side
    LINK paramset in addition to the receiver side, when the sender channel
    carries one.
  - **Self-service password change** (`PATCH /auth/me/password`) and a runtime
    **log-level override** UI (per-subsystem, admin-gated).
  - **Audit export.** The change-history view gains a date-range filter, server
    pagination over the full SQLite history, and CSV export
    (`GET /audit?format=csv`).
  - **Targeted teach-in.** Interface-selective install mode plus pairing a
    device by serial (`POST /devices/{addr}/install-mode`).
- **HTTPS for the daemon's own listener.** Set `north.rest.tls_cert_file` +
  `tls_key_file` to serve the REST API and SPA over TLS on the same port. The
  certificate hot-reloads on change or after an admin upload
  (`POST /admin/tls/certificate`) without a daemon restart.

## [0.11.3]

### Fixed

- **`HmIP-FWI` `CODE_ID` no longer drops the idle value `31` (#3238).** The CCU
  declares `MAX=21` for the fingerprint reader's `CODE_ID`, but the device
  reports `CODE_ID=31` in idle/standby (5-bit field, `31` = no active code). The
  too-low maximum dropped the idle value at ingestion, so the data point kept the
  last recognized code and never returned to `31`. A paramset patch now widens
  `CODE_ID` `MAX` to `31` for `HmIP-FWI` on channel 0 (`MIN` stays at `1`). This
  fixes the event-driven flow (device-reported codes); it is unrelated to the
  optimistic send/rollback path addressed in `0.11.2`. The paramset cache schema
  version was bumped to `2` so the corrected bounds are rebuilt from the CCU.

## [0.11.2]

### Fixed

- **Optimistic values now roll back immediately when a batched / timer write is
  rejected by the CCU, instead of lingering for the full 30 s optimistic-update
  timeout (#3238).** Two send paths staged an optimistic value but did not
  revert it when the wire call failed (e.g. an XML-RPC `RESPONSE_NAK` after all
  retries were exhausted): (1) the atomic `ON_TIME` + `STATE` switch turn-on
  (`turnOnWithTimer`) discarded the rollback closure returned by
  `ApplyOptimistic`, and (2) the `CallParameterCollector` rollback fired a no-op
  for any data point that had already been staged via `sendAndObserve` before
  being added to the collector (the re-entrant `ApplyOptimistic` burst-skipped
  and returned a no-op rollback). Both now roll back on send error with
  `reason=send_error`, matching the direct-send path and `Channel.Set`. The
  burst-skip still does not inflate `PendingSends`, so a single CCU echo settles
  the tracker without a spurious timeout rollback (#3049).

## [0.11.1]

### Fixed

- **`VirtualDevices` no longer report an implausible `0` after a CCU restart via
  the per-parameter VALUES fallback (#3228).** The `0.10.1` seeder rework (v2.5)
  fixed the bulk path, but on a cache-miss `runLoadValuesParamset` still issued a
  `GetParamset(VALUES)` fallback. A virtual heating group's `ACTUAL_TEMPERATURE`
  is aggregated by the CCU and has **no physical device** behind it, so that
  fallback can only ever return the CCU-internal default (`0`) — reported with
  `*_STATUS = NORMAL`, so the status cannot be used to reject it. Because
  `GetParamset`/`GetValue` cannot deliver a device-fresh value for an interface
  without a backing device, the VALUES fallback is now **skipped entirely for
  `VirtualDevices`**: the bulk seeder — which already gates these data points on a
  valid `LastTimestamp()` — is the single source of truth, and the data point
  stays unobserved (sentinel) until a real reading arrives via the event callback.
  Physical interfaces are unaffected: there `GetParamset` can read the device, so
  the fallback is retained.

## [0.11.0]

### Added

- **J5 — `unique_id` on the week-profile / schedule-channel-switch surfaces.**
  `WeekProfileResponse` now carries a top-level `unique_id` for the device-level
  week-profile sensor entity
  (`loom_week_profile_<device-addr>_week_profile`), and every
  `available_target_channels` (`TargetChannelSummary`) entry carries a
  `unique_id` for its schedule-channel-switch entity
  (`loom_schedule_channel_switch_<device-addr>_schedule_channel_lock_<channel_key>`).
  Both keys are built over the owning **device** address (not the channel
  address), `required`, and **bit-identical** to the keys a client otherwise
  synthesises — verified by two new routing-key golden-fixture cases that the Go
  golden test and the `make routing-key-parity` Go↔Python check both pin
  (21/21). This closes the last schedule-path `unique_id` gap from J3, so a
  client drops its own key recomputation (`canonical.py`) from the schedule
  path too. APIVersion → 1.21.0.

## [0.10.1]

Rework the `fetch_all_device_data.fn` ReGa bulk seeder (v2.5) to stop the
post-CCU-restart `0`/`0.0` placeholder at the source (reference issue #3228), and
revert the flawed `#149` interim fix.

### Fixed

- **Bulk seeder no longer drops legitimate zero readings; placeholders are kept
  out at the source.** The `#149` change skipped empty values via
  `if (vDPValue == "") { bHasValue = false }`. In ReGa an operation's type is
  determined by the left operand and an empty string coerces to `0`, so
  `vDPValue == ""` is also true for every numeric `0`/`0.0` — that skip therefore
  dropped *all* legitimate zero readings from the bulk result, not just
  not-yet-measured ones. The seeder (v2.5) now (a) gates `VirtualDevices` data
  points on a valid `LastTimestamp()` so heating groups that carry a `Timestamp()`
  but no real reading after a restart stay out of the bulk result entirely, and
  (b) coerces an empty value to `0` only when it is a genuine string script
  variable (`VarType() == 4`), preserving real numeric zeros. The north-bound
  `IsValid()` availability gating introduced in `#149` is unchanged and keeps
  placeholders out on the consumer side.

## [0.10.0]

Close the external-client backlog waves **J** (`unique_id`-ownership) and **K**
(CCU-domain derivation into the daemon, "dumber client") from
`docs/external-clients/asks.md`. Together they let the Loom path in
`py-openccu-loom-client` drop its `aiohomematic` runtime coupling. APIVersion →
1.20.0.

### Added

- **J1 — `unique_id` on every REST summary + the snapshot, guaranteed
  non-empty.** The canonical loom routing key (the `routingkey.CanonicalUniqueID`
  result, identical to the WS payloads) now rides `DataPointSummary`,
  `CustomDPSummary`, `ProgramSummary`, `SysvarSummary`, `CalculatedDPSummary`,
  `EventGroupSummary` and the nested snapshot data points. The owning central's
  serial suffix reaches the handlers through a new `SerialSuffix(central)` method
  on the `DeviceIndex` / `HubIndex` facades. The field is now `required` (REST +
  the two core WS payloads) and **guaranteed non-empty** by a serial-readiness
  gate: `WireHub`/`resolveCCUSerial` resolves the CCU serial — the central-id
  slot of every hub/internal/virtual-remote key — with a bounded retry before any
  device is loaded; if it cannot be resolved the bring-up gate re-waits, so a
  central never serves entities with an unresolved serial. A client can now drop
  its own key-recomputation fallback.
- **J2 — automatic Go↔Python routing-key parity check.** `script/routing_key_parity.py`
  (`make routing-key-parity`) runs aiohomematic's current `generate_unique_id` /
  `generate_channel_unique_id` over the shared golden-fixture inputs and fails on
  any mismatch. Combined with the existing Go golden test (Go == fixtures), this
  pins Python == fixtures ⇒ automatic cross-repo parity, closing the previous
  manually-synced-fixtures gap.
- **J3 — `unique_id` on calculated data points and event groups.** Calculated
  DPs carry the same canonical key as generic DPs; a new
  `event.Group.CanonicalUniqueID` keeps the event-group key convention in Go.
- **K3 — derived `update_status` on `DeviceSummary`.** A new
  `up_to_date | update_available | installing` field
  (`hmenum.DeriveDeviceUpdateStatus`) collapses the raw CCU firmware phases, so
  a client renders the update entity without carrying the phase-classification
  sets itself. The `DeviceUpdateStatus` enum is exported via `enums.json`.
- **K4 — hub pseudo-addresses as named constants + schema export.**
  `HubAddress` / `InstallModeAddress` / `ProgramAddress` / `SysvarAddress` are
  now named constants in `internal/routingkey` and ship in a `pseudo_addresses`
  block of `assets/schemas/enums.json`, so a wire client reads them from the
  daemon contract instead of `aiohomematic.const`.
- **K1 — primary-channel marker + climate vocabulary enum.**
  `ChannelSummary.is_custom_dp_primary` surfaces
  `device.Channel.IsCustomDPPrimaryChannel`, the daemon-derived "device primary
  channel" marker. New `ClimateMode` (`auto|heat|cool|off`) and `ClimateProfile`
  (`none|away|boost|comfort|eco|week_program_1..6`) enums in `pkg/hmenum`
  (exported into `enums.json`) publish the closed climate vocabulary for typed
  client dispatch; `climate.Mode` / `climate.Profile` are now aliases of them
  (single source). Custom-DP channel composition (`channels`) and the per-device
  available subset (`config.hvac_modes` / `preset_modes`) were already on the
  wire. The finer field→parameter composition map is a **deliberate non-goal**
  (`docs/parity/by_design.md` → `BD-North-CustomDPCompositionMap`): it would
  contradict the K2 state normalisation and leak the internal profile graph onto
  the wire without unblocking any client.

### Changed

- **WS `unique_id` is now always present.** `DataPointValueChangedPayload` and
  `CustomDataPointStateChangedPayload` dropped `omitempty` on `unique_id` — the
  daemon is the sole owner of the key, so clients consume it unconditionally (an
  empty string signals an unresolved central serial, not "field absent").

### Notes

- Asks **J2** (routing-key drift-guard contract test), **J4** (channel metadata
  in the nested snapshot) and **K2** (normalised typed Custom-DP state) were
  already satisfied in the codebase and are re-marked as such in `asks.md`; this
  release adds no code for them beyond `event.Group.CanonicalUniqueID` extending
  the J2 key-convention coverage.

## [0.9.1]

### Fixed

- **Complete the published contract for the 0.9.0 wire gaps (openapi.yaml
  drift).** D1–D3 shipped in the Go implementation and `wsapi.json` but their
  schemas were never added to `assets/openapi.yaml`, so generated client type
  packages (`openccu-loom-types`) could not see them — and the types
  regeneration broke outright on D1 (`gen_ws` could not resolve the broadcast
  payload). The daemon binary already emitted these fields at runtime; this
  release only corrects the published contract + schema digest.
  - **D1** — added the `HubSystemUpdateChangedPayload` schema (referenced by the
    `hub.system_update_changed` broadcast in `wsapi.json`).
  - **D2** — added `value_translations` (object) to `DataPointSummary`.
  - **D3** — added `functions` (array) to `ChannelSummary`.
  - Regenerated the contract `SchemaDigest`; APIVersion stays `1.19.0` (no
    surface change — these fields were already served, just undocumented).

## [0.9.0]

### Added

- **Close the post-`asks.md` north-bound wire gaps D1–D4** discovered during
  the `py-openccu-loom-client` integration. APIVersion → 1.19.0.
  - **D1 — `hub.<central>.system_update` WebSocket broadcast.** The sixth hub
    singleton now pushes on firmware-/system-update state changes, like the
    five existing hub topics (`alarm_messages`, `service_messages`, `inbox`,
    `metrics`, `connectivity`). Wired off the hub model's `Update.OnUpdate`
    hook with a `HubSystemUpdateChangedPayload{current_firmware,
    available_firmware, update_available, in_progress}`, so a client can drop
    its update-status poll loop.
  - **D2 — `value_translations` on data-point summaries.** ENUM data points in
    `GET .../data-points` now carry an optional `value_translations` map (raw
    `VALUE_LIST` entry → localised label, resolved in the request locale via
    the OCCU `parameter_values_<locale>` table). Only entries that actually
    translate are included; clients fall back to `value_list` for the rest.
    Mirrors aiohomematic's per-DP `value_translations`.
  - **D3 — `functions` on channel summaries.** `ChannelSummary` now serialises
    `functions[]` (the channel-level twin of `DeviceSummary.functions`), so
    clients can map "Gewerke" at channel granularity instead of folding up to
    the device.
  - **D4 — OpenAPI: document `SchemaField.default`.** The `GET /config/schema`
    handler already emitted a per-field `default`; the `SchemaField` schema now
    declares it (optional, `nullable`) so strict validators and generated types
    accept the real response.

### Changed

- **Light saturation is now HA-canonical 0..100 throughout `custom/light`
  (D5), matching aiohomematic and the documented `ColorHS` contract.**
  Previously `ColorLight` (and the `EffectLight` / `RGBW` paths that inherit
  its state) emitted the raw wire `0..1` SATURATION fraction into the
  HA-canonical `color.s` field, while `FixedColorLight` and the `combined`
  HS-colour DP already emitted `0..100` — an internal inconsistency that broke
  the nested `color:{h,s}` round-trip for external clients. `ColorLight.Color`
  / `SetColor`, `FixedColorToHS` / `HSToFixedColor`, the Matter
  saturation↔`CurrentSaturation` conversions and the north-bound `set_color`
  operation (saturation default now `100`) all speak `0..100`; the wire
  SATURATION DP is still written as the `0..1` fraction. **External clients
  that sent `set_color` saturation as `0..1` must switch to `0..100`.**

## [0.8.0]

### Added

- **Close the HA-client wire gaps G2/G4/G5/G6** (see
  `docs/parity/ha-client-wire-gaps.md`). These unblock the
  `openccu-loom-client` / Home-Assistant *Homematic(IP) Local* drop-in by
  serializing already-modelled data into the north-bound contract. APIVersion
  → 1.18.0.
  - **G2 — text-display option lists.** The text-display CDP state now carries
    `available_background_colors`, `available_text_colors`,
    `available_alignments`, `available_repetitions` and `available_intervals`
    alongside the existing icon/sound lists, so the notify entity can build its
    per-option pickers.
  - **G4 — hub singleton data points — `GET /api/v1/hub/data-points`.** A single
    aggregating endpoint returns the hub singletons (alarm/service messages,
    inbox, firmware update, metrics, per-interface connectivity and
    install-mode) per central, so a client hub coordinator can be built from one
    fetch (re-enabling its orphan-entity cleanup).
  - **G5 — per-device event groups —
    `GET /api/v1/devices/{addr}/channels/{no}/event-groups`.** Projects a
    channel's keypress/impulse/device-error event groups (`kind`, `event_types`,
    `parameters`, `available`, `last_triggered_event`) so the `event` platform
    gets its bootstrap entities.
  - **G6 — hub-singleton push topics.** New WebSocket broadcasts
    `hub.<central>.alarm_messages`, `.service_messages`, `.inbox`, `.metrics`
    and `.connectivity.<interface_id>` let the client drop its 30 s hub-refresh
    poll loop.

### Changed

- **The per-parameter `VALUES` fallback now loads the whole channel paramset in
  one `getParamset` call instead of one `getValue` per parameter.** The bulk
  `fetch_all_device_data` seed only ships data points that already carry a
  non-zero value, so the fallback runs for every not-yet-measured parameter;
  batching the channel's `VALUES` paramset warms every still-unloaded sibling at
  once. The singleflight key stays per-parameter and sibling fills are gated on
  not-yet-observed, so a bulk read never clobbers a restored / already-known
  value (restore-first / reference #3228). See `docs/parity/by_design.md`
  (BD-CCU-ValuesBulkParamsetLoad). No API change.

### Notes

- Verification of the original gap catalogue reclassified three items (now
  documented in `docs/parity/ha-client-wire-gaps.md`): **G1** is an HS-colour
  shape mismatch fixable client-side (colour-temp/effect read-back already
  works), **G3** (sysvar `extended` marker) is already implemented end-to-end,
  and **G7** (generic `set_on_time`) is reachable through the existing generic
  value route. No daemon change was required for these.

## [0.7.1]

### Added

- **REST endpoints for surgical config reload — `POST
  /api/v1/devices/{addr}/reload` and `POST
  /api/v1/devices/{addr}/channels/{channel}/reload`.** These expose the
  existing per-device and per-channel config reload over REST (previously the
  `config.reload_device_config` / `config.reload_channel_config` commands were
  WebSocket-only). Closes a parity gap for the REST-only consumers (the Python
  client and the Home-Assistant loom backend), which could otherwise only
  trigger the coarse global `POST /devices/refresh`. APIVersion → 1.17.0.

## [0.7.0]

### Added

- **Device-action services — parity with the Home-Assistant integration's
  service surface.** A batch of operator actions that previously had to be
  driven by hand via raw parameter writes are now first-class commands. The
  device-type actions are surfaced through the existing `cdp.invoke` WebSocket
  command (mapping new operation strings onto the already-present custom
  data-point domain methods):
  - **Climate away-mode** — `enable_away_by_calendar` (away until an end
    timestamp), `enable_away_by_duration` (away for N hours), and
    `disable_away`.
  - **On-time** — `set_on_time` for lights, switches and (irrigation) valves
    ("turn on for a duration", reverting automatically).
  - **Cover** — `set_combined` to set blind position and slat tilt atomically.
  - **Siren** — `turn_on` (with acoustic/optical selection and duration) and
    `stop`.
  - **Text display** — `send_text`, `clear_text` and `commit` for HmIP-WRCD.
- **Channel-level config reload — `config.reload_channel_config` /
  `ccu.reload_channel_config`.** Re-pulls the VALUES/MASTER/LINK paramset
  descriptions and MASTER values for a single channel and re-materialises its
  data points, the surgical counterpart to `reload_device_config`. WS-only,
  mirroring the reference.
- **Central links — `central.create_links` / `central.remove_links` /
  `central.links_status`** (WS), wrapping the existing central-links domain;
  the REST `/devices/{address}/central-links` surface already existed.
- **Session recording — `recording.start` / `recording.stop` /
  `recording.status`** (WS) to drive the built-in RPC session recorder across
  every central (multi-CCU-safe).
- **Schedule copy — `schedules.copy` / `schedules.climate.copy_profile`** (WS,
  plus `POST .../schedules/copy` and `POST .../week_profile/copy`): copy a whole
  device schedule between devices, or one climate profile between
  channels/profile slots.
- **Force a system-variable refresh — `sysvars.fetch`** (WS, plus
  `POST /sysvars/fetch`): re-pull all system variables from the CCU on demand.

### Notes

- These services bring OpenCCU-Loom's command surface in line with the
  homematicip_local HA-service set; the underlying behaviour is ported 1:1 from
  the reference. Services that are pure Home-Assistant glue, or that are already
  covered by the generic value/paramset surface, were intentionally not added.

## [0.6.0]

### Added

- **Device-definition export — produce pydevccu / godevccu fixtures straight
  from a live CCU.** A new `GET /api/v1/devices/{addr}/export-definition`
  endpoint (plus the `devices.export_definition` WS command and an
  `hmcli export-def` subcommand) fetches a device's raw description and the
  descriptions of all its channels and their non-LINK paramsets directly off
  the CCU, anonymises every address behind a single random `VCU` id, and
  returns a zip containing `device_descriptions/{model}.json` and
  `paramset_descriptions/{model}.json`. The JSON members are **byte-for-byte
  identical** to aiohomematic's `export_device_definition` (a new
  `internal/orderedjson` package reproduces orjson's member-order-preserving
  output, including its float repr), so the archive drops straight into
  pydevccu / godevccu as a device fixture. To preserve the CCU's wire member
  order the export reads descriptions over a new order-preserving RPC path
  (`InterfaceClient.CallOrdered`) on the XML-RPC and BIN-RPC transports —
  descriptions never travel over JSON-RPC, so that transport is intentionally
  not wired (see `docs/parity/by_design.md`).

- **Persistent login sessions — a daemon restart no longer logs everyone out.**
  Browser auth sessions are now SQLite-backed via a save-through cache: the
  in-memory store stays the hot path for speed, each issued/revoked session is
  best-effort mirrored to a new `auth_sessions` table, and the store hydrates
  active sessions from disk on boot. A background sweep (and lazy eviction on
  lookup) purges expired rows. A DB hiccup at login still lets the login
  succeed — only that one session's cross-restart durability is lost.
  Dynamically created API tokens were already SQLite-backed; the in-memory
  YAML-seeded Basic user store is unchanged. See ADR 0041.

- **Reverse-proxy support for the CCU add-on's "Open Config UI" button via
  `north.rest.public_url`.** Behind a TLS-terminating reverse proxy (Traefik,
  nginx, …) the add-on landing page previously linked the button at
  `http://<host>:8080/app/` — a direct host:port heuristic that the public
  side cannot reach (the proxy routes 443, not 8080, and forces `http`). Set
  `north.rest.public_url` (e.g. `https://loom.example.de`) and the daemon
  writes the resolved Config-UI URL to a hint file in its data dir that
  `config.cgi` links at instead (`<public_url>/app/`). Empty (the default)
  keeps the existing heuristic, which stays correct for a LAN-direct install.
  The field is editable in the SPA and is restart-required.

### Changed

- **Consistent operating concept across the whole Config UI (SPA).** A UX audit
  found the screens had drifted apart because there was no shared vocabulary for
  the recurring states, so every view hand-rolled its own. New shared
  `LoadingState` / `EmptyState` / `ErrorState` components (plus a `Spinner`) now
  back every list and detail screen, so loading, empty and error surfaces look
  and behave the same everywhere (the error surface always shows a localized
  "Error: …" with an optional retry). Action results (save / delete / create /
  run / restore) now consistently use toast notifications instead of a mix of
  toasts and easy-to-miss inline header banners; every destructive action is
  guarded by the shared confirm dialog (including the previously-unguarded
  program "Run"). Several real dark-mode bugs are fixed — `Input`/`Select`
  rendered typed text black-on-dark, and a few widgets used hardcoded colours
  that didn't invert — and raw `<button>`/`<select>`/`<input>` elements were
  replaced with the design-system primitives. Accessibility was tightened
  (tab strips expose `role="tab"`/`aria-selected`, a skip-to-content link, and
  per-route document titles), and two stray hardcoded German strings were
  localized. The daemon restart that previously could fire silently from the
  app-wide banner now surfaces its result.
- **Devices now appear with their names on a cold boot — the daemon waits for
  the CCU to be ready instead of racing it.** When OpenCCU-Loom co-boots with a
  (re)booting CCU, the backend answers `listDevices` / `Device.listAllDetail`
  with http 503 for ~a minute while ReGaHss warms up. Previously the device
  load and the name load (a separate JSON-RPC path) warmed up at different
  times, so devices could appear without their CCU-assigned names until a
  restart. Each central's southbound bring-up is now gated on the CCU's own
  readiness endpoint (`GET /ise/checkrega.cgi` returning `OK` — the marker the
  OCCU WebUI boot page itself polls): names load first, then devices, once,
  against a ready CCU, so devices are created already named. The daemon's
  north-bound surface (REST/SPA/health) comes up immediately and shows a
  "waiting for CCU" state per central while it waits (which never trips
  `/health` to 503). The wait is indefinite — a slow CCU is never abandoned
  into a half-loaded state. The same gate guards mid-life reconnects after a
  CCU reboot. This homogeneous gate replaces the previous partial-load
  background retry; only a thin retry for residual per-interface RPC lag
  remains.

### Fixed

- **Saving a config section that carries a secret (e.g. `north.rest` with
  its `public_url`) no longer fails.** Sections whose schema contains a
  *complex* secret field — `north.rest.auth.users` / `auth.tokens` are
  `map[string]string` — could not be saved. With no HTTP-basic users
  configured, the section load returns `north.rest` without `auth.users`, so
  the editor represented that absent map as the empty string `""`. Saving then
  either aborted silently (the pre-validation ran `JSON.parse("")`, which threw
  before the request was sent — Save did nothing and the typed value vanished
  on navigating away) or, once it reached the request, was rejected with
  `400 … cannot unmarshal string into … auth.users of type map[string]string`.
  MQTT was unaffected because it has no complex secret field. Fixed on both
  sides: the SPA no longer parses or round-trips a placeholder for a
  secret-class or empty complex field — it sends `null` instead of a mistyped
  string — and the API reconciles every secret-field placeholder (the empty
  `""` or the masked `***` sentinel) back to the operator's current real value
  before validating and persisting a section. So the edited field saves,
  existing secrets are preserved, a genuinely changed secret still persists,
  and a round-tripped placeholder can no longer 400 or overwrite a credential.
  A genuine malformed-JSON value now raises a toast instead of failing quietly.
- **Config-save behaviour is now homogeneous across every Settings section.**
  Three inconsistencies are resolved so that saving any field behaves the same
  way it already did for MQTT — persist to the DB, update the per-field source
  dot, and (for restart-required fields) surface in the app-wide restart-pending
  banner and the "Changed settings" overview, never an immediate restart:
  - **Source dots now reflect the real origin of each field.** The effective-
    config endpoint attributed the DB tier by section name only
    (`north.mqtt`), while the SPA's source pill keys on the full field path
    (`north.mqtt.enabled`), so every DB-backed field rendered as "default".
    The daemon now attributes every field path owned by a persisted section to
    the DB tier (honouring the longest-prefix rule so `north.rest.auth.oidc.*`
    is credited to the OIDC section, not REST), while bootstrap- and
    env-sourced fields keep their own attribution.
  - **Saving a Matter (or any restart-required) field no longer pops an
    immediate "restart daemon" modal.** The per-save restart prompt was driven
    section-wide, so saving an unrelated field (e.g. the Matter `node_label`)
    in a section that happens to contain a restart-required field forced the
    modal — unlike MQTT, which only updated the banner. The per-save modal is
    removed; a restart-required change is signalled solely by the persistent
    restart-pending banner and the Changed-settings overview, and the operator
    triggers a restart deliberately from that banner or the System tab. Saving
    never restarts the daemon.
- **Devices now appear even when the CCU backend is not yet ready at daemon
  start.** When OpenCCU-Loom starts alongside a (re)booting CCU — e.g. as a
  co-located add-on — the backend answers `listDevices` with http 503
  ("internal backend exception") for the first minute or so while its services
  warm up. The boot-time device-load previously gave up after a few quick
  retries and left the interface empty until an unrelated recovery cycle
  happened to fire — or indefinitely, if the CCU's ping stayed responsive while
  `listDevices` was still 503. The per-interface device-load (ingest + callback
  init) is now retried in the background with backoff until the CCU answers,
  mirroring the existing hub-side retry, so devices populate on their own
  without a daemon restart.
- **The "waiting for CCU to become ready" health entry no longer lingers after
  a successful boot.** The readiness-gated startup records a transient
  `startup.<central>` component while it waits for the CCU; it was never
  cleared once the central came up, so its last sample went stale and decayed
  to UNKNOWN, pinning the overall health verdict at "unknown" (e.g. 66 %) even
  though the central and its interfaces were healthy. The component is now
  removed as soon as the bring-up succeeds.
- **`/health` no longer returns a transient 503 while a slow CCU boots.**
  During the gated-startup wait a central has no interface clients registered
  yet, which the health heartbeat read as the critical `central` component
  being unhealthy → ServiceAvailability → 503 until the CCU finished booting.
  Zero registered clients is now treated as a "starting" state (the central
  stays healthy); a genuine outage still leaves clients registered-but-
  disconnected and reports unhealthy.

### Security

- **The per-section config API no longer hands cleartext secrets to the
  browser.** `GET /api/v1/config` already masks secret-class fields to `***`,
  but the per-section editor load (`GET /api/v1/config/sections/<section>`)
  returned the stored value with secrets *opened* (the section store decrypts
  on read) — so an admin opening, e.g., the REST section received the real MQTT
  password / OIDC client secret / basic-auth hashes in cleartext. The
  per-section GET now masks secrets the same way as the snapshot; the SPA
  round-trips the `***` sentinel and the save path restores the real value, so
  masking does not break saving. Additionally, the basic-auth credential maps
  `north.rest.auth.users` / `auth.tokens` — managed by their own Users/Tokens
  admin panels — are no longer rendered as (meaningless, single-password-input)
  fields in the generic section editor.

## [0.5.1]

### Fixed

- **Two OpenCCU-Loom daemons against the same CCU no longer drive each
  other's `/health` to `503`.** The XML-RPC ping embedded only the bare
  interface name in its `caller_id` (`HmIP-RF#<token>`), and the PONG-ingest
  hook correlated on that bare name. Because the CCU broadcasts every PONG to
  all registered clients, a co-located daemon's PONGs carried the same bare
  prefix, passed the correlation guard, matched no pending ping, and piled up
  as "unknown" mismatches — degrading, then (after the flap-damp escalation)
  failing the only interface, which tripped the "every interface down → 503"
  rule. The ping now keys its `caller_id` on the full wire-boundary triple
  `<instance>-<central>-<interface>` and the hook matches the echoed prefix
  against the client's own triple, so a foreign daemon's PONGs are rejected
  instead of counted. Mirrors the reference
  `caller_id = f"{interface_id}#{token}"` and its `v_interface_id ==
  interface_id` guard.
- **A ping/pong correlation mismatch alone can no longer make `/health`
  return `503`.** Ping/pong mismatches are now recorded on a separate
  `ping_pong/<interfaceID>` quality component instead of the interface's
  liveness entry, so correlation noise can at most degrade service
  availability (HTTP 200) — never escalate the interface to unhealthy and map
  to 503. The signal stays visible in diagnostics and no longer skews the
  primary-client-healthy verdict. Mirrors the reference's distinct
  `ping_pong_mismatch_{interface_id}` issue model.
- **Sensors no longer publish a spurious `0` after a CCU restart.** The
  `fetch_all_device_data.fn` ReGa bulk-load script coerced an **empty**
  (not-yet-measured) numeric value into `"0"`. After a restart a data point such
  as `ACTUAL_TEMPERATURE` can already carry a timestamp but no real reading yet;
  the script then emitted `0`, published as a confirmed value (e.g. `0 °C`, or
  `0`/closed for a cover's `LEVEL`). The script now **skips** empty values, so
  the data point stays unset until a real measurement arrives. This is the
  actual fix for upstream issue #3228 — the `#3228` DEBUG log confirmed the
  source is the seed coercion (the CCU pushes no `STATUS = UNKNOWN`; every
  `*_STATUS` event is `NORMAL` and a post-reconnect `getValue` returns
  `Fault -5`), not the measurement status.
- **North-bound `available` is now gated on the full data-point validity,
  mirroring the reference `is_valid`.** REST, WebSocket and the MQTT runtime
  (`VALUES`) plane now report a reading as available only when it is valid:
  refreshed (a value has been observed), its paired `<X>_STATUS` is acceptable,
  its value type matches, and it is within range. An `OVERFLOW`/`UNDERFLOW`
  status or an as-yet-unobserved data point therefore publishes as unavailable
  (with a `null` value) instead of as a confirmed reading. STATUS validity keeps
  reference parity — `NORMAL` and `UNKNOWN` are both valid for every parameter
  (no measured-vs-control discriminator), so a control actuator such as `LEVEL`
  reporting `UNKNOWN` during the init-phase grace period stays available
  (upstream #2630). MQTT device-level reachability and the `MASTER`/`CALCULATED`
  planes are unchanged. See `docs/parity/by_design.md`
  (BD-CCU-StatusUncertainViaTracker).

## [0.5.0]

### Added

- **Opt-in measurement history for SPA charts (no external stack
  required).** A daemon running without Home Assistant can now record a
  time-series of numeric sensor values and chart it in the SPA. The
  recorder subscribes to live wire value changes and persists them to a
  dedicated `history.db` (its own WAL, separate from the config/session
  store); only genuine live observations are recorded — boot-time
  pseudo-values, cache replays, and source-only flips are filtered out by
  provenance (`ValueSource`), so a real `0` is kept but a restart spike is
  not. A new `GET /api/v1/history` endpoint returns a server-side-bucketed
  (avg/min/max/count) series sized for charting. For users who already run
  Grafana/InfluxDB, an opt-in push exporter forwards each sample via
  InfluxDB line protocol (no client dependency; token sourced from the
  environment). Everything is off by default and configured under
  `persistence.history` (DB-tier, SPA-editable). See
  [ADR 0040](docs/adr/0040-measurement-history.md) and SPECIFICATION §4.6.
- **Nine new MCP read tools** project domain data that previously had no
  MCP surface: `list_programs`, `list_sysvars`, `list_service_messages`,
  `list_alarm_messages`, `list_inbox`, `get_system_info` (hub aggregates,
  each `central_name`-scoped), plus `list_rooms`, `list_functions`, and
  `list_channels` (device topology). `list_channels` closes a real gap —
  agents can now discover channel addresses before calling `read_paramset`
  instead of guessing `:n`. All are reads (no new write surface, no new
  config, no new capability token); the MCP catalogue grows from 9 to 18
  tools. Tool names follow a documented verb/vocabulary taxonomy now
  pinned by a contract test. See
  [ADR 0025](docs/adr/0025-mcp-northbound-adapter.md) and
  [the MCP guide](docs/external-clients/mcp.md).
- **`GET /programs/{id}` single-program fetch.** Returns one
  `ProgramSummary` by id (`404` when unknown), mirroring the existing
  `GET /sysvars/{name}` shape. Like that endpoint it resolves the central
  by id across CCUs and only requires `?central=` to disambiguate an id
  shared by multiple centrals. Clients that previously fetched the full
  `GET /programs` list and filtered locally can drop that workaround.
- **Device-type icons in the device list — real images proxied from the
  CCU.** Cards led with a bare reachability dot, leaving 140+ tiles
  visually identical. They now show the device's icon with the
  reachability state as a corner dot. A new `GET /devices/{addr}/icon`
  resolves the device to its central and proxies the real eQ-3 image the
  CCU serves under `/config/img/devices/250/<file>` (cached, since icons
  are static; unauthenticated like `/health`). When the CCU has no icon
  for a model or is offline, the card falls back to a representative type
  glyph, so it always shows something.
- **Persistent "restart required" banner + a changed-settings overview
  with per-field revert.** Saving a config change that needs a restart
  gave only a one-shot modal. A new `GET /system/restart-pending`
  (persisted vs. running boot config over the restart-required field set)
  drives an app-wide banner that stays until the change is reverted or the
  daemon restarts; it links to Settings and offers an inline restart where
  a supervisor is detected. A new "Changed settings" overview
  (`GET /system/config-changes`) lists exactly the fields that differ from
  the running boot config — what was edited this session, not what differs
  from the built-in default — so a clean start shows nothing and reverting
  an edit empties the list. Each entry is revertible on its own via
  `DELETE /config/fields/{path}` (removes just that leaf, pruning the
  section row when empty), the per-field counterpart to the whole-section
  reset. The overview is a standalone tab at the end of the settings nav,
  shown only while there are changes.
- **Grid/list toggle for the device list, with sticky search.** The list
  can switch between the multi-column card grid and a single-column list
  (durable preference); the search term, filters and sort now survive
  opening a device and navigating back instead of resetting each time.

### Changed

- **Clearer grouping across the config UIs.** As the number of settings
  grew, the editors had gone flat. The daemon Settings sidebar now buckets
  its tabs into five collapsible top-level categories (General & System,
  Bridges, CCUs & Connectivity, Security & Access, Advanced) instead of one
  long list. Within a section, fields are split into labelled subgroups
  (e.g. Authentication / Rate Limiting / WebSocket / Tracing under
  *API & WebSocket*; Commissioning / CASE / Attestation under *Matter*),
  derived from the config path with a count badge per group. The device
  channel-config editor (MASTER paramset) gets the same header treatment and
  its curated group titles (Temperature, Timing, Boost, …) are now localized
  (de/en) instead of hard-coded English; easymode-metadata groups keep their
  archive label. Frontend-only and additive — no API or config changes.
- **Clearer device value presentation in the SPA.** Enum status values
  now localize (a door contact reads "Geschlossen", not "Closed"; a dimmer
  "Unbekannt", not "Unknown"). Momentary event channels (a remote's
  PRESS_SHORT/LONG) render as events with when they last fired instead of a
  raw `false`. The measurement-history tab is fully localized and its
  states are distinct — "recording off" explains itself and links to the
  setting (rather than naming a YAML key), separate from "no data in this
  range". Long config pages keep Save reachable via sticky action bars, and
  the weekly-schedule fallback line names the base temperature so it no
  longer looks inconsistent with the period temperatures in the heatmap.
  A further round of UX-audit polish: generic data-point labels localize
  client-side (STATE → "Status"); the device-overview tile grid drops to
  two columns for one- or two-tile devices so it no longer wastes a third
  of the row; the top-bar connectivity badge is labelled as the live-update
  (WebSocket) stream rather than a bare red dot; and the maintenance stripe
  replaces double negatives ("Duty-Cycle blockiert: Nein") with status
  words (OK / Blockiert / Schwach). The device list also uses the full
  width.

### Fixed

- **Change-history view was always empty.** The audit recorder persisted
  every config change to SQLite, but the read path served only an
  in-memory ring buffer that starts empty on each daemon start — so the
  history showed nothing after a restart and never surfaced seeded config.
  The buffer is now hydrated from the persisted store on boot (preserving
  original ordering and timestamps).

## [0.4.0]

### Changed

- **Optional pagination on hub list endpoints.** `GET /programs`,
  `GET /sysvars`, `GET /alarm-messages`, and `GET /service-messages` now
  accept optional `page` / `per_page` query parameters (same semantics as
  `GET /devices`). The response body remains a flat JSON array in all cases
  so existing clients are unaffected; a new `X-Total-Count` response header
  carries the unfiltered item count for cursor-less pagination. The OpenAPI
  spec is updated with the optional parameters and the header.
- **Stricter JSON decoding for four diagnostic/visibility endpoints.**
  `POST /diagnostics/capture`, `PUT /system/startup-capture`,
  `PUT /diagnostics/log-levels/{path}`, and `PUT /visibility/unignore` now
  reject unknown fields in their request bodies (previously silently ignored).
  The shared `DecodeJSON` helper (which already enabled `DisallowUnknownFields`)
  is used at all four sites.

### Fixed

- **`GET /sysvars/{name}` no longer requires `?central=` on single-CCU
  deployments.** The handler now uses name-based lookup: when `?central=` is
  absent it scans all centrals and routes to the unique owner; only genuine
  ambiguity (same name on >1 central) requires the explicit parameter.

### Security

- **In-memory user passwords are bcrypt-hashed.** Users seeded from the YAML
  `auth.users` map and via the HTMX first-run setup page are now hashed with
  bcrypt (cost 12, matching the SQLite user store) before storage instead of
  being held verbatim; `MemoryUserStore` verifies hashed records with bcrypt
  and still accepts legacy plaintext records through a constant-time fallback.
  Operators may seed a pre-computed bcrypt hash and it is used as-is.
- **Brute-force speed-bump on the HTMX login.** The pre-auth `POST /login`
  form is now rate-limited per client IP (burst 5, ~1 request/second refill)
  on the UI listener — a surface the per-identity REST limiter cannot cover
  (it keys on a resolved identity and runs on the REST listener). Throttled
  requests receive a `Retry-After` header and the generic login error, so the
  limiter neither slows a legitimate operator nor reveals its presence.
- **Per-identity rate limit on the WebSocket command channel.** Once a WS
  connection is upgraded the REST per-request limiter no longer applies, so a
  single authenticated session could fan out paramset writes / ReGa executions
  unbounded. The command router now throttles each identity (burst 60, ~20/s
  refill, idle-evicted bucket map); a throttled command returns
  `code: rate_limited`.
- **Plaintext-secret fallback is now visible on `/health` and as a metric.**
  When no master key can be resolved the daemon stores config secrets in
  plaintext (the ADR 0027 resilient fallback) — previously surfaced only as a
  single boot warning. It now reports a degraded `config.secrets` component on
  `/health` (which collapses to "degraded", not a 503, so liveness stays green)
  and a `config_secrets_plaintext` gauge (1 = plaintext, 0 = encrypted) so an
  operator dashboard catches it without scraping logs.

### Fixed

- **Device list no longer truncates at 200 devices.** The SPA's device store
  now fetches all pages on refresh: it reads the `total` field from the first
  `GET /devices` response and issues additional requests (page size 200, capped
  at 100 pages) until every device is loaded. Installations with more than 200
  devices previously saw a silently incomplete list.

- **Clean shutdown of recovery and MQTT-command work.** The connection-recovery
  coordinator spawned its per-interface recovery runs on a detached background
  context with no tracking, so `Stop()` could return while a multi-minute
  recovery pipeline was still running against the central; the runs now execute
  under a cancellable context tracked by a `WaitGroup`, and `Stop()` cancels and
  drains them. MQTT command handlers built their per-command context from
  `context.Background()`; they now derive it from the daemon-lifetime context
  (wired so it survives a broker hot-swap), so an in-flight CCU write is
  cancelled on shutdown instead of lingering to its ack timeout.
- **Bounded change-history and values-cache growth.** The `audit_log` table had
  no retention and grew without bound; rows older than 90 days are now purged
  opportunistically (every 256 inserts), no scheduler required. Removing a
  device now also evicts its rows from the persistent values cache — previously
  an unpaired device left its cached rows behind indefinitely.
- **REST upstream failures no longer report `code: internal`.** 35 handler
  paths that return HTTP 502 for a failed CCU/upstream call tagged the
  problem+json body with `code: internal` (which signals a daemon bug). They
  now use `code: upstream_unavailable`, so API/SPA clients switching on `code`
  can distinguish a transient upstream outage from an internal error. The 502
  status and the specific error titles are unchanged.
- **Retry-cancellation metric no longer double-counts.** `CancelledRetries`
  was incremented twice per cancelled chain — once at the cancelling call site
  (supersede / `CancelKey` / `CancelDevice` / `CancelInterface`) and again when
  the chain observed its closed cancel channel. It is now counted once, so the
  metric matches the number of chains actually cancelled.
- **Circuit-recovery waiter no longer drops other waiters.** The retrier's
  recovery waiter wired its wake-up hook with the breaker's *replace* setter
  instead of the *append* one, so a second waiter on the same breaker silently
  evicted the first (leaving its blocked retries unwoken until their deadline).
  It now appends, matching the documented "piggy-back, never replace" intent.

### Added

- **MQTT MASTER-paramset writes.** The documented
  `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` topic now writes the
  MASTER paramset via the same `Channel.Set` path as the REST paramset endpoint.
  Previously the `master` bucket was silently dropped. The `calculated` bucket
  remains read-only and is dropped with a debug log.
- **CCU system-update panel** (Settings → System). Shows each CCU's firmware
  state (installed → available) and, for admins, an **Install** button that
  triggers the CCU's own firmware update (`POST /system/update/install`) with
  a reboot confirmation and live progress polling. The REST/WS API already
  supported this; it is now reachable from the web UI.

### Changed

- **Configurable MQTT retain-cleanup window (`north.mqtt.retain_cleanup_window_ms`).**
  The snapshot window used by `RunRetainCleanupOnce` and
  `RunDiscoveryOrphanCleanupOnce` was hard-coded at 2 seconds. Operators on
  high-latency brokers or large retained-message stores can now raise this value
  (valid range: 500–30 000 ms). Zero or absent falls back to the existing 2 s
  default so behaviour is unchanged for deployments that do not set the key.

### Fixed

- **Panic-safe circuit-breaker state listeners.** State-change callbacks fired
  with a bare `go cb(from, to)` in `refreshLocked`; a panicking listener
  silently killed its goroutine. Callbacks are now wrapped in a
  `safeFire` helper that `recover()`s and logs the panic at error level, so the
  breaker continues transitioning normally and remaining listeners still run.
- **Bounded self-reload concurrency in callback handlers.** A coerce-failure
  flood from the CCU could spawn an unbounded number of concurrent
  `LoadValue` goroutines against the radio. Self-reloads are now gated by a
  buffered semaphore (capacity 16); excess reloads are dropped with a debug log
  instead of queueing unbounded work.
- **Bridge declared-map pruned on device removal.** When the CCU sends a
  `deleteDevices` callback the MQTT bridge now removes the corresponding entries
  from its internal declared map, so the orphan-cleanup dedup gate does not
  suppress subsequent evictions of those topics.

- **Responsive / iPhone pass across the config UI (Svelte SPA).** Every
  route and the heavy editor components were reworked so the content — not
  just the app shell — is usable on a phone:
  - Shared foundations: `viewport-fit=cover` + safe-area-inset helpers so
    the notch / home indicator never clip the sidebar, header, toasts or
    dialogs; a reusable table→cards reflow (`table-reflow` + `data-label`,
    table on desktop, stacked cards on phones); touch-sized primitives
    (`Button` / `Input` / `Select` / `Switch`), with `text-base sm:text-sm`
    on inputs to suppress iOS Safari focus auto-zoom.
  - Wide data tables (audit log, backups, firmware, users, API tokens,
    diagnostics recordings) reflow to cards below `sm`.
  - Non-wrapping toolbars and fixed-width inputs (device-list filter bar,
    device-detail rename, logs / inbox / sysvars / section editor, Matter
    exposures) now wrap or go full-width on phones.
  - Settings: the fixed vertical tab sidebar becomes a horizontal scroll
    strip on phones.
  - Schedule editors: the fixed-width timeline visualisations are now fluid,
    and the period / lock / astro rows regroup so they no longer overflow.
  - Device-control tiles: actuator buttons, colour chips, sliders and number
    steppers raised to ≥40–44px touch targets.
- **Full localisation (de/en) of the SPA.** Every remaining hardcoded
  string — DeviceList, Login, the device-control tiles (climate, cover,
  light, siren, valve, text-display), the schedule editor, the Matter
  screens, and assorted labels / placeholders / aria-labels — now resolves
  through the in-app de/en catalogue (~190 new keys). Technical enums
  (roles, CCU data types, log levels) and the language-picker names stay
  literal by design.
- More table→cards reflows (profile preview, Matter fabrics) and touch-
  target fixes (text-display tile, sidebar footer icons, channel picker);
  the keyboard-shortcut button is now hidden on touch devices
  (`pointer-coarse`).
- `theme-color` gained a dark-mode variant.

### Fixed

- Toast container no longer overflows the right edge on narrow (≤390px)
  viewports.
- Replaced the remaining native `confirm()` / `prompt()` dialogs in the SPA
  (device delete / rename / firmware, set-room, sysvar / token / user
  actions, daemon restart, link / import) with the app's styled confirm
  modal and inline editors.
- Removed the ineffective dynamic import of the audit-log route (it is also
  statically imported by the device-detail history tab), clearing the
  build-time `INEFFECTIVE_DYNAMIC_IMPORT` warning.
- Post-recovery hub-metadata reload (system-update, sysvars, programs — all
  over ReGa) is now **best-effort**: a transient ReGa failure (an overloaded
  CCU, or a firewall/IPS dropping bursty HTTP) no longer fails the whole
  `data_loading` recovery stage, so an interface's already-enumerated devices
  stay visible instead of vanishing until a manual restart. Each refresh is
  reattempted by the periodic hub jobs, so a miss self-heals.
- **CCU system-update progress is now monitored** — parity with aiohomematic
  `install()` / `_monitor_update_progress`. Triggering a CCU update via
  `POST /system/update/install` now snapshots the firmware version and spawns
  a bounded monitor (poll every 30 s, up to 30 min) that clears the
  `in_progress` flag once the CCU finishes installing and reboots. Previously
  `in_progress` was set on trigger but never auto-cleared (the ported
  `MonitorProgress` was unwired), so the status — and the new system-update
  panel — stayed stuck on "installing".

## [0.3.0] — 2026-06-14

### Added

- **Per-central behaviour toggles (`centrals[].behavior`).** Nine operator
  toggles mirroring the reference stack's config knobs, all per-central and
  runtime-editable:
  - `light_last_brightness` (default true) — restore last brightness on a
    plain light turn-on, or turn on at full.
  - `use_group_channel_for_cover_state` (default true) — report cover
    position from the group channel or the cover's own channel.
  - `enable_sysvar_scan` / `enable_program_scan` (default true) — gate the
    hub system-variable / program scan entirely.
  - `include_internal_sysvars` (default true) / `include_internal_programs`
    (default false) — daemon-side filter for CCU-internal hub entities, so
    MQTT and REST agree.
  - `sysvar_markers` / `program_markers` (default empty) — restrict the hub
    scan to entities whose CCU description starts with one of the
    `DescriptionMarker` tokens (HAHM, HX, INTERNAL, MQTT); program
    descriptions are now fetched via ReGa when program markers are set.
  - `sysvar_scan_interval` (default 0 = 5 min) — override the periodic
    sysvar-refresh cadence.
  - `enable_device_firmware_check` (default true) — gate the per-device
    firmware-update entity surface. Defaults true (a deliberate divergence
    from the reference stack's false default; see `docs/parity/by_design.md`)
    so 0.2.0's firmware-update entities are preserved on upgrade.
  - `delay_new_device_creation` (default false) — defer ingest of a
    newly-paired device until it is accepted from the inbox.

  The block is editable end-to-end: YAML, the SQLite-backed central store
  (`behavior_json`), the REST V2 central API (documented on the
  `CentralBehavior` schema in `assets/openapi.yaml`), and the SPA central
  editor. `api_version` 1.7.0 → 1.8.0 (additive).

### Changed

- **SPA is smartphone-friendly.** The navigation sidebar now behaves
  responsively: on `<md` (phones) it is an off-canvas drawer opened by a
  header burger and dismissed by a backdrop tap or a nav-item tap, and the
  content pane is full-width (the fixed-width left padding only applies from
  `md` upward, where the bar is permanently docked). The mobile drawer always
  renders the labelled (expanded) nav regardless of the desktop collapse
  preference. The CCU edit form's field pairs collapse from two columns to a
  single column on narrow screens.
- **mDNS advertisement enriched for client auto-discovery.** The
  `_openccu-loom._tcp` TXT bundle now also carries `instance=<label>`
  (the friendly daemon name for a client's daemon picker) and
  `centrals=<count>` (a pre-auth hint of how many CCUs the daemon
  serves). Host/IP and port already come from the A/AAAA + SRV
  records; CCU names/serials are read from `GET /api/v1/system/ccu`
  after auth (not advertised in TXT). Lets `homematicip_local` /
  `openccu-loom-client` discover and select a daemon without manual
  host/instance entry. See ADR 0021.

## [0.2.0] — 2026-06-14

### Added

- **Contract schema digest on `GET /api/v1/info`.** The new
  `schema_digest` field identifies the exact contract state
  (openapi.yaml, wsapi.json, enums/types schemas) the binary was built
  from; generated client type packages carry the same value, so clients
  can verify type/daemon parity at connect time. `api_version` is now
  guarded in CI: contract-asset changes without a version bump fail the
  PR (breaking OpenAPI diffs require a major bump), and releases
  dispatch a regeneration event to the openccu-loom-types repo.
  See ADR 0028. `api_version` bumped to 1.1.0 (additive).

- **Matter NodeLabel suffixes share the entity display-name resolution.**
  Measurement sub-endpoints previously embedded the raw parameter key in
  their `BridgedDeviceBasicInformation.NodeLabel`
  (`"Wohnzimmer Kanal 1 (TEMPERATURE)"`). The assembler now routes the
  suffix through the same primitives as the MQTT discovery builder and
  the REST data-point handler (`device.TranslatedParameterLabel` →
  `naming.EntityDisplayName`), bound to the daemon locale
  (`locale` config key): translated parameters render their OCCU label
  (`"… (Temperatur)"`), untranslated ones fall back to the title-cased
  parameter (`"… (Temperature)"`), and "primary" parameters drop the
  suffix entirely — matching how MQTT/REST collapse the entity name to
  the device name. All three north-bound surfaces now resolve
  per-parameter display names from the same source of truth.

- **Home Assistant add-on.** OpenCCU-Loom can now be installed as a Home
  Assistant add-on, a third distribution channel alongside the Docker image
  and the CCU/RaspberryMatic add-on. The repository itself doubles as a HA
  add-on repository (add `https://github.com/SukramJ/openccu-loom` under
  *Settings → Add-ons → Add-on Store → Repositories*). The add-on is built on
  the official HA base image (s6-overlay supervises the daemon, so the Config
  UI's **Restart** action works in-container; bashio maps the `log_level`
  option), runs with `host_network` (so per-central callbacks reach the
  daemon), persists state in `/data`, and exposes the Config UI both via
  **Ingress** (sidebar panel) and the direct port `:8080`. One image is
  published per arch (`ghcr.io/sukramj/openccu-loom-ha-<arch>`, amd64 /
  aarch64 / armv7). Sources live in `packaging/ha-addon/`; the release build
  is toggled by `BUILD_HA_ADDON`. Delivers the channel anticipated in
  [SPECIFICATION.md](SPECIFICATION.md) Q9.
- **CCU / RaspberryMatic add-on packaging.** OpenCCU-Loom can now ship as
  a native CCU add-on alongside the Docker image. The release attaches
  `openccu-loom-ccu-<version>.tar.gz` (installable via the CCU's
  *Additional software* page); a single tarball bundles the amd64, arm64,
  and armv7 builds and the `update_script` selects the right one per
  `uname -m`, covering CCU3 and every RaspberryMatic flavour (32-bit Pi,
  64-bit Pi, x86-64 OVA / generic). The add-on installs an `rc.d` service
  with monit supervision and wires *Settings* / *Update* entries into the
  CCU add-on page; the daemon stays UI-configured, with state under
  `/usr/local/addons/openccu-loom/var`. Sources live in
  `packaging/ccu-addon/`, packaged by `script/build_ccu_addon.sh`
  (`make ccu-addon`). Activates the CCU/RaspberryMatic channel anticipated
  in [ADR 0012](docs/adr/0012-matter-pure-go-implementation.md).
- **`OPENCCU_LOOM_CALLBACK_PUBLIC_HOST`** env override for
  `callback.public_host` — there was an env knob for the callback *bind*
  host but none for the *advertised* host.
- **REST data-point summaries carry `translated_name` + `label_omitted`.**
  `GET /api/v1/devices/{addr}/cdps` (and the snapshot / values-batch
  surfaces) now expose the same per-entity display name the MQTT discovery
  builder emits — both resolve through a single shared primitive
  (`naming.EntityDisplayName`), so a REST drop-in and the MQTT plane spawn
  Home Assistant entities with identical names. `label_omitted` mirrors the
  "primary parameter" marker (HA collapses the entity name to the device
  name alone; MQTT emits `name: null`).

- **Per-interface install-mode sensor + button on MQTT discovery.**
  Install/pairing mode now surfaces as one remaining-seconds `sensor`
  AND one activation `button` per interface (`install_mode_hmip`,
  `install_mode_bidcos`, plus their `-button` companions) — matching the
  reference stack — replacing the single central-wide aggregate sensor.
  The button publishes to
  `<base>/<central>/hub/install_mode/<iface>/set`; the command
  subscriber translates the HA press token into an install-mode
  activation on that interface (default 60s, or a numeric override).
  Per-interface countdown state rides
  `<base>/<central>/hub/install_mode/<iface>`.

- **Virtual-remote press buttons on MQTT discovery.** Virtual remotes
  (HM-RCV-50 / HMW-RCV-50 / HmIP-RCV-50) now expose two clickable HA
  `button` entities per channel (`press_short`, `press_long`, disabled
  by default) next to the per-channel keypress `event` entity —
  matching the reference stack's per-channel surface. The command
  subscriber maps HA's `payload_press` token ("PRESS") to the boolean
  `true` the write-only ACTION parameters expect, which also makes the
  existing RESET_MOTION / RESET_PRESENCE buttons actually trigger.

### Changed

- **REST `parameter_label` is now always ready to render.** The field
  carried the locale-aware channel-typed translation *or empty*, leaving
  the fallback to each client — and the SPA rendered raw parameter keys
  (`RSSI_DEVICE`) in tiles, readouts, and the channel status badge when
  no translation existed. The server now resolves the title-cased
  fallback itself (`Rssi Device`) via the shared naming primitive, on
  both `DataPointSummary.parameter_label` and the Matter exposure
  candidates' `parameter_label`; the SPA renders the field verbatim
  through a single `dpLabel()` helper (its client-side title-casing
  copy is gone) and its API types gained `translated_name` /
  `label_omitted`. `assets/openapi.yaml` documents the field contract.
- **Log download offers larger sizes.** The diagnostics log viewer's download
  selector now offers 2000 and 5000 lines in addition to 100 / 200 / 500 /
  1000. The backend already served any `limit` up to the live-log ring
  capacity (5000); only the UI choices were capped at 1000.
- **CCU add-on Settings page is branded.** Clicking *Settings* for the
  OpenCCU-Loom CCU / RaspberryMatic add-on now shows a small card with the
  OpenCCU-Loom logo and an *Open Config UI* button (into the SPA on port
  8080), mirroring how ccu-jack presents its logo — instead of an immediate
  blank redirect.
- **Reference config files renamed** `config.example.yaml` →
  `example.config.yaml` and `config.example.full.yaml` →
  `example.config.full.yaml`. Required because a Home Assistant add-on
  repository is scanned recursively for `config.{yaml,yml,json}`, and the
  old names matched that glob (the Supervisor would have tried to parse them
  as add-ons). Update any local references; the file contents are unchanged.
- **Dependency refresh.** `golang.org/x/tools` → v0.46.0 (and transitive
  `golang.org/x/mod` → v0.37.0); SPA `tailwindcss` / `@tailwindcss/vite`
  → 4.3.1 and `@lucide/svelte` → 1.18.0; docs `pymdown-extensions`
  floor → 10.21.3.

### Fixed

- **Multi-CCU: client health no longer collapses same-named interfaces.**
  The aggregated health view deduplicated components by bare name, so
  two CCUs both running e.g. `HmIP-RF` surfaced as a single entry and
  the diagnostics "client health" panel showed only one CCU's
  interfaces (which one depended on sample timing). Components from a
  central's tracker are now scoped as `<central>/<component>`;
  `ClientDetail`/`ClientScore` route scoped names to the owning
  central's tracker, bare names keep the legacy first-match lookup.

- **`GET /api/v1/devices/{addr}/cdps` no longer panics on a half-formed light
  channel.** `*light.Light` relied on the method promoted from its embedded
  `*generic.Float` for `Category()`. On a "half-formed" channel — one whose
  LEVEL parameter has not materialised yet, so `Float` is nil — the
  autogenerated forwarder dispatched to `(*DataPoint[float64]).Category` on a
  nil receiver and panicked, surfacing as a `500 Internal error` that aborted
  the Home Assistant integration's device bootstrap. `Light` now defines an
  explicit nil-safe `Category()` returning `Undefined` when `Float` is nil,
  mirroring the existing `cover.Cover.Category` guard. (Climate/siren use named
  rather than embedded `*generic.Float` fields, and the valve wrappers return
  nil from their constructors when the DP is absent, so they are not exposed to
  the same hazard.)

- **WebSocket Origin check no longer blocks non-browser clients.** With CSRF
  enabled (the default), the `/api/v1/events` handler rejected any handshake
  without an `Origin` header (`403 websocket origin required`) — which broke
  headless API-token clients such as the Home Assistant integration's
  `openccu-loom-client`, since non-browser clients legitimately omit `Origin`.
  CSRF is a browser-only attack vector and browsers always attach an `Origin`
  to WS handshakes, so a missing `Origin` cannot be a forged cross-site
  request. The handler now allows handshakes with no `Origin` through and only
  validates the value when one is actually present, preserving cross-site
  protection for genuine browser connections.

- **Hub wiring now recovers from a transient boot-time failure.** `WireHub`
  ran exactly once at boot; if the CCU's ReGa was not yet reachable during the
  daemon's startup window it failed, leaving that central's entire hub surface
  (programs / sysvars / inbox / service+alarm messages) **and** the
  `central.refresh_client_data` safety net dead until a manual restart —
  observed live as a central logging `refresh_client_data: LoadAndRefreshData­
  PointData not wired` every tick with zero hub activity. A failed boot-time
  WireHub now schedules a background retry (5 s→60 s backoff, bounded by the
  daemon lifecycle) that re-establishes the hub once the CCU answers and wires
  the refresh handler. The retry re-applies the hub mutators through new
  mutex-guarded setters (`Hub.SetMutator`, `Update.SetFirmwareUpdater`,
  `Reconciler.SetConnect`), so it does not race the running daemon.
- **Central trapped in FAILED after connectivity returns (permanent `/health`
  503).** The central state machine permitted `FAILED → RECOVERING` / `STOPPED`
  only. When every interface reconnected *outside* an active recovery pipeline
  (the clients' own reconnect path, `in_recovery=false`),
  `evaluate_central_state` computed `RUNNING`/`DEGRADED` and the transition was
  silently rejected — so the central stayed in `FAILED` indefinitely even
  though all interfaces were connected: `/health` returned 503 and every
  heartbeat logged a futile `failed→running`. `FAILED` is now recoverable
  (`→ RUNNING` / `→ DEGRADED` added; only `STOPPED` is terminal), mirroring the
  client state machine.
- **Lost event under concurrent publish (event bus dispatcher handoff race).**
  `Publish` released the `dispatch` lock via `defer` *after* `flushDeferred`
  observed the deferred queue empty. A concurrent `Publish` whose `TryLock`
  failed in that window enqueued its event to `deferred` but never re-checked,
  so the event sat undrained until some future publish — effectively dropped
  if none came. Surfaced as an intermittent macOS-CI failure
  (`HandlerStat.Matches=999, want 1000`). The dispatcher now releases
  `dispatch` inside `flushDeferred` while holding `mu`, and the slow path
  attempts the take-over under the same `mu`, making release and re-acquisition
  mutually exclusive — so a concurrently enqueued event is always drained.
- **Endless reconnect loop on quiet CCUs (~every 180 s).** Inbound CCU
  callbacks never refreshed the per-interface callback-liveness timestamp —
  it was stamped only on reconnect. On a CCU with little spontaneous device
  traffic, `IsCallbackAlive` therefore went stale exactly `callbackFreshness`
  (180 s) after each reconnect, the `check_connection` watchdog declared the
  channel dead, and a full recovery fired — re-stamping the timestamp and
  restarting the 180 s clock, forever. Affected every interface on every
  central (local and remote alike). `CallbackHandlers.Event` now stamps
  liveness for every inbound callback, before the device-existence guard.
- **PONG callbacks never correlated (ping/pong pending pile-up).** The CCU
  echoes a ping's caller_id back as a `PONG` event on the `CENTRAL`
  pseudo-address. Because that address is not a mirrored device,
  `CallbackHandlers.Event`'s device-existence guard dropped the PONG before
  it reached the ping-pong tracker, so pending PINGs grew unbounded to their
  per-interface cap (100) and health stayed permanently *degraded* (and
  `/health` returned 503). PONG is now routed to the tracker before the
  device guard, closing the round-trip.
- **Foreign / liveness PONGs filed as "unknown" mismatches.** The CCU
  broadcasts `PONG` events to *every* registered logic-layer client, so on a
  shared CCU the daemon also receives other instances' PONGs (e.g.
  `Otto-HmIP-RF#<ts>`) on its own interface, plus the bare-name liveness
  probe's tokenless PONGs. These were recorded as unmatched *unknown*
  mismatches, decaying interface health to degraded/unhealthy. (The reconnect
  loop above had masked this by clearing the tracker every ~180 s.) The
  PONG-ingest hook now correlates a PONG only when its caller_id carries a
  `#` token *and* the embedded prefix equals this client's own ping prefix
  (the bare interface name) — mirroring the reference
  `v_interface_id == interface_id` guard. Verified live against a CCU shared
  with other Homematic instances: pending and unknown both stay at 0.
- **Callback host is resolved per-central (multi-CCU).** The host the
  daemon advertised in `init()` for CCU push events was computed once
  globally — from `callback.public_host` or a UDP egress probe against
  the *first* central — and reused for every CCU. On a daemon serving a
  local CCU (reachable at `127.0.0.1`) and an external CCU (reachable at
  the daemon's LAN IP) one of them always got an unreachable callback
  address: no push events, "central heartbeat degraded". The advertised
  host (XML-RPC and BIN-RPC/CUxD) is now detected per central as the
  egress interface toward *that* CCU, so each gets a reachable address;
  `callback.public_host`, when set, still overrides all centrals for NAT
  setups.
- **Enabling HA discovery at runtime now takes effect without a daemon
  restart.** Toggling `north.mqtt.ha_discovery` (or any MQTT setting)
  triggered a hot MQTT swap that rebuilt the bridge from scratch — with
  an empty Discovery cache and slot state — but nothing re-seeded it:
  the supervisor's snapshot `OnConnect` hook fires *during* the swap's
  bridge build, before the new bridge is installed into the shared
  wiring, so it re-published onto the outgoing bridge. The new bridge
  stayed empty until a full daemon restart re-ran the boot-time snapshot.
  The reload handler now re-runs `PublishInitialSnapshot` *after* the
  swap completes (when the shared wiring already points at the new
  bridge), so discovery + availability + per-DP slot state are re-seeded
  exactly as a restart would, for every successful enable-swap.

- **Channel-group switch state now reaches WS subscribers.** Switching a
  channel-group switch CDP (HMIP-PS/PSM/PSMCO — `STATE@3`/`STATE@4`/`STATE@5`)
  left the HA switch entity snapping back to off: the daemon never delivered a
  matching `custom_data_point.state_changed`. Two defects compounded on the
  WS CDP-state path in `eventbridge.go`:
  1. `customDPStatePayload` matched a `State() map[string]any` shape that no
     shipping CDP implements (every CDP exposes the typed `payload.Source`
     `State()`), so the push silently never fired for any CDP. It now reads the
     canonical `payload.Source` contract and JSON-round-trips the typed state
     into the wire map (`{is_on: true}`), identical to the `GET …/cdps`
     snapshot.
  2. The event used the bare parameter (`STATE`) as its name, but the cdps
     REST/WS surface disambiguates channel-group CDPs to `PARAM@<channel>`
     (`STATE@3`). The push now carries `custom.WireName(...)`, so the client's
     `(address, name)` keyed CDP receives it. The reference stack re-renders
     each custom DP on its own member events; this keeps the state topic
     aligned with the catalogue entry.
- **CDP invoke/get accept percent-encoded wire names.** A conformant client
  that percent-encodes the `{name}` path segment (`STATE%403`) previously hit
  a 502 ("data point STATE%403 not found"); the handler now URL-decodes the
  segment via `url.PathUnescape` on both the invoke and get paths, while a
  literal `@` keeps working.

- **Connection-latency aggregated to a single hub sensor on MQTT
  discovery.** Latency previously published one `sensor` per interface
  (`latency_<central>_<iface>`); the reference stack exposes ONE
  central-wide `connection-latency` sensor fed from the aggregated
  ping/pong metric. The per-interface latency discovery and state are
  removed; a single `connection_latency` sensor now publishes on
  `<base>/<central>/system/latency`, sourced from the
  `connection_latency_ms` metric aggregate. Stale per-interface latency
  discovery configs are auto-evicted by the discovery-orphan cleanup
  pass on the next boot; the old retained per-interface state topics
  (`…/system/latency/<iface>`) are left empty/orphaned (no HA entity
  subscribes) and are not matched by the legacy retain-cleanup patterns.

- **Text-display (HmIP-WRCD) now publishes only a `notify` entity on MQTT
  discovery.** The aggregate path emitted a surplus `text` entity
  alongside the `notify` companion, which HA rendered with a colliding
  `_2` suffix. The reference stack maps a TEXT_DISPLAY custom-DP onto a
  `notify` entity ALONE; the aggregate `text` entity is now suppressed so
  the display surfaces as a single `notify` entity. The stale `text`
  discovery config is auto-evicted by the discovery-orphan cleanup pass.

- **Multi-central hub unique_id collision on MQTT discovery.** The hub
  publisher built its own discovery builder and never saw the
  per-central HubInfo registered on the bridge, so every central's hub
  entities (sysvars, programs, alarm/service messages, inbox,
  install-mode, connectivity, metrics, update) collided on serial-less
  unique_ids (`loom__alarm_messages`) and HA silently dropped all but
  one CCU's hub plane. The publisher now shares the bridge's builder,
  hub discovery skips publishing until the CCU serial is known (never
  an empty slot), and the daemon re-runs the publisher after stamping
  HubInfo post-hydration.

- **Sysvar HA typing keys on the extended-sysvar marker.** Component
  selection for sysvar discovery used writability, rendering nearly
  every ReGa variable as a writable switch/number/select. It now
  mirrors the reference stack: only extended sysvars (ReGa-description
  marker) surface as switch / select / number / text; everything else
  is a read-only sensor or binary_sensor (ALARM keeps the `problem`
  device class).

- **DataPointUsage verdict gates per-parameter MQTT discovery.**
  `no_create` / `ignored` data points and the `ce_primary` /
  `ce_secondary` constituents of a channel's custom-DP aggregate no
  longer spawn duplicate generic entities next to the aggregate
  (climate / switch / cover / light …). `ce_visible` extras (HmIP-BWTH
  HUMIDITY, ACTUAL_TEMPERATURE) still pass.

- **Action categories no longer surface as HA entities.**
  `action_number` (ON_TIME, RAMP_TIME, DURATION_VALUE) mirrors the
  reference stack's empty ActionNumber whitelist; plain `action`
  parameters (COMBINED_PARAMETER, RAMP_STOP) have no HA platform there
  either. Both stay writable through the per-DP command topics and
  custom-DP service methods. Write-only enum parameters
  (`action_select`) keep their select surface and are now relegated to
  HA's Configuration section.

- **ENUM tokens are lower-cased toward HA.** Enum sensor and select
  discovery now lower-cases `options` and pipes the state through
  `| lower` (the reference stack renders translatable lowercase tokens
  like `closed`, `auto_mode`); selects map the chosen option back to
  the uppercase CCU token via `command_template` on write. Hub sysvar
  enum labels stay verbatim, matching the reference.

- **Channel-group switch state now reaches WS subscribers.** Switching a
  channel-group switch CDP (HMIP-PS/PSM/PSMCO — `STATE@3`/`STATE@4`/`STATE@5`)
  left the HA switch entity snapping back to off: the daemon never delivered a
  matching `custom_data_point.state_changed`. Two defects compounded on the
  WS CDP-state path in `eventbridge.go`:
  1. `customDPStatePayload` matched a `State() map[string]any` shape that no
     shipping CDP implements (every CDP exposes the typed `payload.Source`
     `State()`), so the push silently never fired for any CDP. It now reads the
     canonical `payload.Source` contract and JSON-round-trips the typed state
     into the wire map (`{is_on: true}`), identical to the `GET …/cdps`
     snapshot.
  2. The event used the bare parameter (`STATE`) as its name, but the cdps
     REST/WS surface disambiguates channel-group CDPs to `PARAM@<channel>`
     (`STATE@3`). The push now carries `custom.WireName(...)`, so the client's
     `(address, name)` keyed CDP receives it. The reference stack re-renders
     each custom DP on its own member events; this keeps the state topic
     aligned with the catalogue entry.
- **CDP invoke/get accept percent-encoded wire names.** A conformant client
  that percent-encodes the `{name}` path segment (`STATE%403`) previously hit
  a 502 ("data point STATE%403 not found"); the handler now URL-decodes the
  segment via `url.PathUnescape` on both the invoke and get paths, while a
  literal `@` keeps working.

- **Connection-latency aggregated to a single hub sensor on MQTT
  discovery.** Latency previously published one `sensor` per interface
  (`latency_<central>_<iface>`); the reference stack exposes ONE
  central-wide `connection-latency` sensor fed from the aggregated
  ping/pong metric. The per-interface latency discovery and state are
  removed; a single `connection_latency` sensor now publishes on
  `<base>/<central>/system/latency`, sourced from the
  `connection_latency_ms` metric aggregate. Stale per-interface latency
  discovery configs are auto-evicted by the discovery-orphan cleanup
  pass on the next boot; the old retained per-interface state topics
  (`…/system/latency/<iface>`) are left empty/orphaned (no HA entity
  subscribes) and are not matched by the legacy retain-cleanup patterns.

- **Text-display (HmIP-WRCD) now publishes only a `notify` entity on MQTT
  discovery.** The aggregate path emitted a surplus `text` entity
  alongside the `notify` companion, which HA rendered with a colliding
  `_2` suffix. The reference stack maps a TEXT_DISPLAY custom-DP onto a
  `notify` entity ALONE; the aggregate `text` entity is now suppressed so
  the display surfaces as a single `notify` entity. The stale `text`
  discovery config is auto-evicted by the discovery-orphan cleanup pass.

- **Multi-central hub unique_id collision on MQTT discovery.** The hub
  publisher built its own discovery builder and never saw the
  per-central HubInfo registered on the bridge, so every central's hub
  entities (sysvars, programs, alarm/service messages, inbox,
  install-mode, connectivity, metrics, update) collided on serial-less
  unique_ids (`loom__alarm_messages`) and HA silently dropped all but
  one CCU's hub plane. The publisher now shares the bridge's builder,
  hub discovery skips publishing until the CCU serial is known (never
  an empty slot), and the daemon re-runs the publisher after stamping
  HubInfo post-hydration.

- **Sysvar HA typing keys on the extended-sysvar marker.** Component
  selection for sysvar discovery used writability, rendering nearly
  every ReGa variable as a writable switch/number/select. It now
  mirrors the reference stack: only extended sysvars (ReGa-description
  marker) surface as switch / select / number / text; everything else
  is a read-only sensor or binary_sensor (ALARM keeps the `problem`
  device class).

- **DataPointUsage verdict gates per-parameter MQTT discovery.**
  `no_create` / `ignored` data points and the `ce_primary` /
  `ce_secondary` constituents of a channel's custom-DP aggregate no
  longer spawn duplicate generic entities next to the aggregate
  (climate / switch / cover / light …). `ce_visible` extras (HmIP-BWTH
  HUMIDITY, ACTUAL_TEMPERATURE) still pass.

- **Action categories no longer surface as HA entities.**
  `action_number` (ON_TIME, RAMP_TIME, DURATION_VALUE) mirrors the
  reference stack's empty ActionNumber whitelist; plain `action`
  parameters (COMBINED_PARAMETER, RAMP_STOP) have no HA platform there
  either. Both stay writable through the per-DP command topics and
  custom-DP service methods. Write-only enum parameters
  (`action_select`) keep their select surface and are now relegated to
  HA's Configuration section.

- **ENUM tokens are lower-cased toward HA.** Enum sensor and select
  discovery now lower-cases `options` and pipes the state through
  `| lower` (the reference stack renders translatable lowercase tokens
  like `closed`, `auto_mode`); selects map the chosen option back to
  the uppercase CCU token via `command_template` on write. Hub sysvar
  enum labels stay verbatim, matching the reference.

## [0.1.0] — Initial Release

First public release of **OpenCCU-Loom**, a standalone Go daemon that
bridges Homematic CCUs to MQTT, a REST + WebSocket API, a web Config UI,
and a Matter bridge. A Go port of the `aiohomematic` family that adds the
standalone-daemon surface on top.

### Core

- **Multi-CCU from day one** — one daemon, many CCUs; every coordinator,
  adapter, and store is `central_name`-scoped (ADR 0002).
- **Hexagonal architecture** (ports & adapters) with an internal typed,
  priority-aware event bus for cross-domain communication.
- **Single static binary** (`CGO_ENABLED=0`, no CGo) + multi-arch Docker
  images (linux/amd64, arm64, armv7).
- **Pure-Go SQLite** (`modernc.org/sqlite`) + filesystem persistence;
  goose-managed migrations.

### South-bound (CCU)

- All three transports: **XML-RPC, BIN-RPC, JSON-RPC**, plus HTTP and raw
  TCP callback servers (shared across all centrals, dynamic-port aware).
- Every MVP interface — HmIP-RF, BidCos-RF, BidCos-Wired, HmIP-Wired,
  VirtualDevices, and CUxD (BIN-RPC) — supports **push callbacks**; no
  polling-only code path.
- Reliability layer per `(central, interface)`: circuit breaker, retry,
  throttle, coalescer, ping/pong.
- 139 generated device profiles with hand-written custom data-point types;
  ReGa script runner.
- **Homegear backend support** — system variables load and periodically
  refresh over the XML-RPC `getAllSystemVariables` method (each variable's
  type inferred from its value, since Homegear ships only name + value) and
  write back via `setSystemVariable`, bringing Homegear to system-variable
  parity with the reference stack. Programs, rooms, and functions stay
  empty on Homegear by design (no ReGa engine / metadata RPC).

### North-bound (bridges)

- **MQTT** — Home Assistant Discovery **and** raw topic planes in parallel;
  MQTT config applies without a daemon restart. Discovery topics are scoped
  to each device's own CCU, so on a multi-CCU daemon every device's state,
  availability, command, and `json_attributes` topics route to the central
  the device actually lives on. Availability tracks device *reachability*
  (`UNREACH` / `STICKY_UNREACH` via `Device.Available()`): a reachable
  device is published `online` at boot even before its data points report,
  and every registered data point — including not-yet-observed ones —
  publishes an explicit `{"value":null,"available":true}` slot state (HA
  value templates render an unobserved point as `unknown` rather than
  `"None"`). The full boot snapshot — per-device availability plus every
  data point's slot state — is republished on every broker (re)connect, so
  a broker restart or transient TCP reset never leaves entities stuck
  `unavailable`.
- **REST + WebSocket API** — ~80 REST endpoints (`assets/openapi.yaml`) and
  85 WebSocket commands. Value-bearing WebSocket push payloads
  (`datapoint.value_changed`, `custom_data_point.state_changed`,
  `hub.sysvar_changed`, `hub.program_executed`,
  `datapoint.optimistic_rolled_back`, `device.trigger`) carry the canonical
  loom-namespaced `unique_id` (`loom_<routing-key>`) that external clients
  use as the Home Assistant entity key.
- **MCP server (Model Context Protocol)** — a north-bound adapter
  (`internal/north/mcp/`) exposing the daemon to LLM agents as tools over a
  Streamable-HTTP transport, mounted on the REST listener behind the same
  auth chain. Disabled by default (`North.MCP.Enabled`) and read-only even
  when enabled — write tools are registered only when `North.MCP.AllowWrites`
  is also set, and a write tool that touches a device refuses to act on one
  the named central does not own. Read tools: `list_centrals`,
  `list_devices`, `get_device`, `read_paramset`, `get_health`,
  `list_audit`; write tools: `set_datapoint`, `write_paramset`,
  `trigger_program`. Each tool also gates on its own dependency, so a
  partial wiring never exposes a half-functional tool. The `mcp.v1` /
  `mcp.write.v1` capability tokens surface the posture via `GET /info`.
  Built on the official `modelcontextprotocol/go-sdk` (ADR 0025).
- **Config UI** — a Svelte 5 SPA (Tailwind 4, embedded via `go:embed`) as
  the primary surface, with a minimal HTMX bootstrap surface for pre-auth
  flows (login, first-run setup, OIDC callback) and SPA-down diagnosis. The
  SPA includes an **MCP** tab (wired through the config schema and the
  section store) to toggle `enabled` / `allow_writes` and set the mount
  `path` — flagged restart-required — without editing YAML or env.
- **Matter bridge** — native-Go, default off, operator opt-in; a semantic
  port of matter.js HEAD.

### Auth & security

- Basic / Session / OIDC / API-Token authentication with role gating
  (admin-only mutations); CSRF protection for cookie-session flows.
- Audit ledger for sensitive operations.

### Diagnostics & observability

- Unified health tracker (per-central + daemon-global), Prometheus
  metrics, tracing helpers, incident journal.
- **Live log viewer** (`#/logs`, admin-only): always-on ring buffer with an
  SSE tail (`tail -f`, resume via `Last-Event-ID`/`?since=`), aggregated
  (≥ warn, deduplicated) vs. detail views, level dropdown, and download of
  the last N records.
- **Diagnose & Aufzeichnen hub**: RAM-buffered debug-log capture and an
  **RPC session recorder** (XML / JSON / BIN-RPC traffic for deterministic
  golden replay) with per-CCU scope, duration limit + safety cap,
  optional anonymisation, restart-survival, and `map`/`golden` export.
- Composite diagnostics dump and runtime per-subsystem log-level overrides.

### Internationalisation

- German + English catalogues across UI and server-rendered surfaces.
- A curated translation overlay supplies device-model labels the upstream
  catalogue omits — e.g. HmIP-DLP ("Türschlossantrieb - pro" / "Door Lock
  Drive - pro") and HmIP-UDI-SMI55 ("Universal Dimmeraufsatz -
  Bewegungsmelder" / "Universal Dimming Control Element - motion
  detector") — so their MQTT discovery payload carries a readable
  `model_id` instead of falling back to the raw wire type.

### Quality & parity

- Cross-stack model-snapshot drift gate
  (`script/model_snapshot_drift_check.py`) with an explicit env-override
  table (`OPENCCU_LOOM_DRIFT_GENERIC` / `_CHANNEL` / `_CALC`), a printed
  TOTAL line, and fail-closed behaviour on any drift bucket without a
  baseline.
