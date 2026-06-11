# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
