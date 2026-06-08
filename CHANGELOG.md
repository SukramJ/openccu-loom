# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
