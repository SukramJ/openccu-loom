# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **WebSocket payloads carry the canonical `unique_id`.** The
  value-bearing push payloads (`datapoint.value_changed`,
  `custom_data_point.state_changed`, `hub.sysvar_changed`,
  `hub.program_executed`, `datapoint.optimistic_rolled_back`,
  `device.trigger`) now include an optional `unique_id` field — the
  loom-namespaced routing key (`loom_<routing-key>`) external clients
  use as the Home Assistant entity key. Clients consume it directly
  instead of rebuilding it from the raw fields; the field is omitted
  when the producer cannot resolve it, so the change is
  backward-compatible. See
  `docs/external-clients/ha-unique-id-migration.md`.

### Added

- **MCP server (Model Context Protocol).** A new north-bound adapter
  (`internal/north/mcp/`) exposes the daemon to LLM agents as tools over
  a Streamable-HTTP transport, mounted on the REST listener behind the
  same auth chain. Disabled by default (`North.MCP.Enabled`) and
  read-only even when enabled — write tools are registered only when
  `North.MCP.AllowWrites` is also set, and the write tools that touch a
  device refuse to act on one the named central does not own. Read
  tools: `list_centrals`, `list_devices`, `get_device`, `read_paramset`,
  `get_health`, `list_audit`. Write tools: `set_datapoint`,
  `write_paramset`, `trigger_program`. Each tool also gates on its own
  dependency, so a partial wiring never exposes a half-functional tool.
  The `mcp.v1` / `mcp.write.v1` capability tokens surface the posture via
  `GET /info`. Built on the official `modelcontextprotocol/go-sdk`. See
  [ADR 0025](docs/adr/0025-mcp-northbound-adapter.md).

- **System variables now work on Homegear backends.** Homegear is
  XML-RPC-only, so the JSON-RPC hub bootstrap could not populate its
  sysvar list and `/api/v1/sysvars` stayed empty. The daemon now loads
  and periodically refreshes Homegear system variables over the XML-RPC
  `getAllSystemVariables` method (inferring each variable's type from
  its value, since Homegear ships only name + value), and writes values
  back via `setSystemVariable` — bringing Homegear to parity with the
  reference stack's Homegear support. Programs, rooms, and functions
  remain empty on Homegear by design (no ReGa engine / metadata RPC),
  matching that stack.

- **Device-model labels for HmIP-DLP and HmIP-UDI-SMI55.** These two
  devices ship icons and parameter help but no device-model label in
  the upstream translation catalogue, so their MQTT discovery payload
  omitted `model_id` (HA fell back to the cryptic wire type). The
  curated translation overlay now supplies the German and English
  labels — "Türschlossantrieb - pro" / "Door Lock Drive - pro" and
  "Universal Dimmeraufsatz - Bewegungsmelder" / "Universal Dimming
  Control Element - motion detector".

### Fixed

- **MQTT entities no longer show as `unavailable` in Home Assistant.**
  Availability now tracks device *reachability* (`UNREACH` /
  `STICKY_UNREACH` via `Device.Available()`) rather than "has a value
  been observed yet". A reachable device is published `online` at boot
  even before its data points report, and every registered data point —
  including not-yet-observed ones — publishes an explicit
  `{"value":null,"available":true}` slot state instead of an empty
  eviction payload. Under the discovery payload's
  `availability_mode: all` this is what kept entities stuck on
  `unavailable`. The HA value templates gained a `value_json.value is
  not none` guard so an unobserved data point renders as `unknown`
  rather than the literal `"None"` (or a misleading multiplied `0.0`).

- **MQTT raw-plane snapshot is republished on every broker reconnect.**
  The boot-time `PublishInitialSnapshot` (per-device availability + every
  data point's slot state) only ran once, after CCU hydration — and that
  publish raced the MQTT connection. When the broker reset or the initial
  connect completed only after hydration (observed live: a
  `connection reset by peer` landing exactly as a multi-central
  hydration finished), the whole snapshot hit a not-yet-connected client
  and was dropped. `OnConnect` re-announced `bridge/status` and
  re-published the HA-Discovery configs but **not** the raw plane those
  configs reference, so the availability + slot-state topics were never
  restored until a live CCU value change trickled in. Combined with the
  sensors' `expire_after: 3600`, HA then expired the entities to
  `unavailable`. The snapshot now runs on every broker (re)connect
  (alongside the existing `AnnounceOnline` / `RepublishDiscovery` hooks),
  so the raw plane is restored after any disconnect or broker restart.

- **Model-snapshot drift gate honours its documented overrides.**
  `script/model_snapshot_drift_check.py` derived its env-override keys
  via a fragile string transform that did not match the documented
  `OPENCCU_LOOM_DRIFT_GENERIC` / `_CHANNEL` / `_CALC` names, so three
  of the four baseline overrides silently had no effect. The keys are
  now an explicit table. The script also prints a TOTAL line and now
  fails on any drift bucket it has no baseline for (previously such
  buckets slipped through unguarded), and its stale `parity_audit.md`
  references were re-pointed at `docs/parity/by_design.md`.

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

### North-bound (bridges)

- **MQTT** — Home Assistant Discovery **and** raw topic planes in parallel;
  MQTT config applies without a daemon restart.
- **REST + WebSocket API** — ~80 REST endpoints (`assets/openapi.yaml`) and
  85 WebSocket commands.
- **Config UI** — a Svelte 5 SPA (Tailwind 4, embedded via `go:embed`) as
  the primary surface, with a minimal HTMX bootstrap surface for pre-auth
  flows (login, first-run setup, OIDC callback) and SPA-down diagnosis.
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
