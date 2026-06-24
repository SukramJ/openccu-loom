# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
