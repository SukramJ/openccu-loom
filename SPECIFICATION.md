<p align="center">
  <img src="assets/logo/wordmark.svg" alt="OpenCCU-Loom" height="56">
</p>

# OpenCCU-Loom — Specification

**Status**: Living reference — last refresh 2026-05-05.
**Scope**: Goals, constraints, and resolved decisions for OpenCCU-Loom 0.1.0+.

## What this document is — and what it is not

This specification deliberately stays at the **architecture-and-rationale**
layer. It records what the project is, what it deliberately does and
does not do, and the constraints that bind every implementation
decision. It does **not** describe implementation details — those
drift, and a hand-maintained implementation spec drifts faster than
anyone can correct it.

For implementation truth, consult the authoritative sources:

| Concern | Source of truth |
|---|---|
| Domain model, package layout, internals | Go source under `internal/`, `pkg/` |
| Architecture decisions of consequence | `docs/adr/000N-*.md` |
| REST API contract | `assets/openapi.yaml` (CI-validated, runtime-validated by daemon) |
| WebSocket command contract | `assets/wsapi.json` |
| MQTT topic + payload structure | ADR 0011 (`mqtt-topic-and-payload-architecture`) |
| Configuration knobs | `example.config.yaml` (annotated, loaded by daemon) |
| Coding conventions, AI-assistant guide, repo norms | `CLAUDE.md` |
| Build, packaging, release | `Makefile`, `.goreleaser.yaml`, `Dockerfile`, `CONTRIBUTING.md` |
| Test strategy | `CLAUDE.md`, `docs/testplan.md` |
| Release history | `CHANGELOG.md` |
| Recent architecture audit + risk follow-ups | `docs/audit/architecture-review-2026-05-05.md` |
| Cross-stack parity status against aiohomematic | `docs/parity/by_design.md` + the cross-stack model-snapshot pipeline (`script/model_snapshot_diff.py`) |

If a topic is not in the table above and not in this document, the
code is the answer.

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Goals & Non-Goals](#2-goals--non-goals)
3. [Technology Stack & Constraints](#3-technology-stack--constraints)
4. [High-Level Architecture](#4-high-level-architecture)
5. [Enumerations & Wire Identities](#5-enumerations--wire-identities)
6. [Matter Bridge](#6-matter-bridge)
7. [Resolved Decisions & Risk Register](#7-resolved-decisions--risk-register)

---

## 1. Executive Summary

`OpenCCU-Loom` is a Go implementation of the core functionality
provided by the Python library `aiohomematic`. While `aiohomematic`
is primarily a library powering the Home Assistant integration
*Homematic(IP) Local*, `OpenCCU-Loom` is designed as a **standalone
daemon** that speaks to the Homematic CCU (XML-RPC, BIN-RPC,
JSON-RPC) and exposes Homematic devices through modern north-bound
interfaces:

- **MQTT** (Home Assistant Discovery format + raw topic plane in
  parallel)
- **REST + WebSocket** API
- **Web-based Config UI** — Svelte 5 SPA (primary). A minimal
  HTMX bootstrap surface (login, first-run setup, OIDC callback,
  server-rendered /health and /about) covers what the SPA cannot
  reach: pre-auth flows and SPA-down diagnosis.
- **Matter Bridge**

Feature parity with `aiohomematic` on the **south-bound** side (CCU
connectivity, all interfaces, all device profiles, paramset caching,
custom device types, hub entities, schedules) is a hard requirement.
The **north-bound** side intentionally deviates — there is no Home
Assistant integration shim.

The south-bound XML-RPC, BIN-RPC, and JSON-RPC transports are native
Go implementations inside this repository. They are not derived from
any third-party Homematic transport library — see ADR 0001 for the
licensing rationale.

Deployment target is a single static binary plus a Docker image
(`linux/amd64`, `linux/arm64`, `linux/arm/v7`).

**Project name**: `openccu-loom`
**Go module**: `github.com/SukramJ/openccu-loom`
**License**: MIT (source); embedded openccu-data artifacts retain the
eQ-3 HomeMatic Software License — see ADR 0003.
**Min Go version**: 1.26.
**Versioning**: SemVer (`vX.Y.Z`), releases via `goreleaser`.

---

## 2. Goals & Non-Goals

### 2.1 Goals

1. **Full protocol parity** with aiohomematic for all CCU interfaces:
   HmIP-RF (serves both HmIP-RF and HmIP-Wired devices), BidCos-RF,
   BidCos-Wired, VirtualDevices, CUxD.
2. **Full device-profile parity** at MVP cut: every
   `DeviceProfileRegistry` entry from aiohomematic is represented.
   Profiles are auto-generated from the Python source via
   `script/generate_profiles.py`; the parity contract test enforces
   the registry against the pinned aiohomematic version.
3. **Same reliability envelope**: circuit breaker, command throttle
   (RF duty cycle), command retry with exponential backoff, request
   coalescing, ping/pong health tracking, connection recovery
   stages — all present and validated by contract tests.
4. **Stable public API contract** for north-bound consumers: REST
   under `/api/v1` (described by `assets/openapi.yaml`, validated at
   request time by the daemon), MQTT topics (described in ADR 0011),
   WebSocket commands (described in `assets/wsapi.json`).
5. **Single static binary** for primary platforms. **No CGo
   dependencies** in the default build. SQLite via
   `modernc.org/sqlite` (pure Go).
6. **Operable daemon**: structured logging, Prometheus metrics,
   `/debug/pprof`, health endpoints, graceful shutdown, hot-reload
   for non-structural settings.
7. **Multi-CCU from day one**: a single daemon serves multiple CCUs
   simultaneously, each scoped in MQTT topics, REST paths, and
   metric labels (see ADR 0002).
8. **Well-tested**: contract tests guard protocol/capability
   invariants, golden-file tests replay recorded CCU sessions,
   integration tests exercise an in-process `godevccu` simulator
   (no Python toolchain needed).

### 2.2 Non-Goals

- **No Home Assistant integration**. An HA user can still consume
  the MQTT bridge (HA Discovery), but that path is indirect.
- **No on-disk artifact compatibility** with aiohomematic (caches,
  sessions). OpenCCU-Loom maintains its own SQLite + filesystem state
  layout; an operator runs it alongside or instead of aiohomematic
  without sharing on-disk data.
- **No Python compatibility layer**. The two projects coexist as
  independent implementations of the same wire contract.
- **No full Matter certification** — the Matter bridge is a useful
  partial implementation, not a certified product.
- **No cloud features** (remote access, multi-tenant, OTA).
  OpenCCU-Loom is a LAN service.
- **No CCU-Jack / pull-only path** in 0.1.0. Every interface
  supports push callbacks; there is no JSON-RPC-only mode.
- **No Homegear depth-parity** in 0.1.0. The backend abstraction
  exists and a basic `HomegearBackend` works, but full
  programs/rooms/functions parity is a post-0.1.0 milestone.

### 2.3 Success Criteria (MVP = 0.1.0)

- OpenCCU-Loom connects to a real CCU3/RaspberryMatic and to
  `godevccu` without configuration drift.
- Multi-CCU: a single daemon connects to ≥ 2 CCUs simultaneously,
  each with its own interface set, each scoped in topics and REST
  paths.
- MQTT bridge publishes state on **both** the raw plane
  (`<base>/<interface>/...`) and the HA Discovery plane; HA
  auto-discovers entities.
- REST + WebSocket API covers list/get/set device values, execute
  programs, read/write sysvars, stream events.
- Config UI (Svelte SPA) displays devices, channels, data points,
  programs, sysvars; supports setting values; shows connection
  health. Default locale `de`, `en` switchable.
- Setup Wizard (first run): admin user, CCU connection(s), UI
  language + theme.
- Reconnects automatically after CCU restart, XML-RPC socket drop,
  MQTT broker outage.
- All contract tests pass against CCU3 + godevccu.
- Backup: `openccu-loom backup create` produces a consistent
  tarball; FS-level backup of `state-dir` also works.

**Explicitly out of scope for 0.1.0 MVP**: HA Add-on packaging,
RaspberryMatic Add-on packaging, deep Homegear parity. (Both add-on
channels landed post-0.1.0 — the CCU/RaspberryMatic add-on under
`packaging/ccu-addon/` (Q10) and the Home Assistant add-on under
`packaging/ha-addon/` (Q9). Deep Homegear parity remains out of scope.)

---

## 3. Technology Stack & Constraints

### 3.1 Hard constraints

These are **non-negotiable**. Anything that requires changing one of
them needs an ADR.

- **Language**: Go 1.26+. No support for older toolchains.
- **CGo OFF.** `CGO_ENABLED=0` at all times. If CGo seems necessary
  (crypto, native SQLite, Matter SDK), open an ADR first.
- **License of dependencies**: MIT / Apache-2.0 / BSD only. GPL /
  LGPL / MPL / AGPL pull copyleft obligations — discuss before
  adding. The embedded openccu-data archives are non-commercial
  eQ-3 content and are handled by a separate aggregation path
  (ADR 0003).
- **License of source**: MIT. Every Go file starts with the
  SPDX-License-Identifier header (CLAUDE.md enforces).
- **Pure-Go SQLite**: `modernc.org/sqlite`. Switching to a CGo
  driver requires an ADR.
- **Multi-CCU correctness**: every coordinator, adapter, and store
  is multi-CCU-safe. No hard-coded "the single CentralUnit". See
  ADR 0002.

### 3.2 Coding conventions

Detailed conventions live in `CLAUDE.md` (gofumpt, golangci-lint
config, error-wrapping rules, context propagation, no `interface{}`
without justification, package-level state forbidden, etc.). The
points worth pinning at the spec level:

- **Interfaces in the consumer package**, not the producer
  (standard Go advice). The single exception: cross-cutting
  protocol contracts that multiple packages must implement live in
  `pkg/interfaces`.
- **Every I/O method takes `ctx context.Context` as its first
  argument.** No exceptions.
- **No `panic` from library code.** Tests + `main()` may panic.
- **Errors carry domain context** via `pkg/hmerr` types and
  `errors.Is` / `errors.As` semantics.
- **Generics are fine and expected** (e.g. typed `EventBus`, typed
  property accessors).

### 3.3 Reusable subset

The reusable core lives in `/pkg` (typed events, enums, error
types, primitive types, DI contracts, log + property helpers).
Third-party consumers of this package surface are a secondary
audience; the project is first a daemon. Anything in `internal/` is
not part of the public Go API.

---

## 4. High-Level Architecture

### 4.1 Architecture style

OpenCCU-Loom follows **Hexagonal / Ports & Adapters** for outside-
world boundaries plus an **internal in-process Event Bus** for
cross-domain communication inside the core.

```
┌─────────────────────────────────────────────────────────────────┐
│                         OUTSIDE WORLD                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │  MQTT    │  │  REST /  │  │  Browser │  │       CCU        │ │
│  │  Broker  │  │  WebSock │  │  (SPA)   │  │  (XML/BIN/JSON)  │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
│        ▲            ▲             ▲                   ▲         │
└────────┼────────────┼─────────────┼───────────────────┼─────────┘
         │            │             │                   │
┌────────┼────────────┼─────────────┼───────────────────┼─────────┐
│   ┌────▼────────────▼─────────────▼───┐        ┌──────▼──────┐  │
│   │        NORTHBOUND ADAPTERS        │        │  SOUTHBOUND │  │
│   │  (driving ports)                  │        │  ADAPTER    │  │
│   │  • MQTT Bridge                    │        │  (driven    │  │
│   │  • REST + WebSocket               │        │   port)     │  │
│   │  • UI (Svelte SPA, HTMX bootstrap)│        │             │  │
│   └───────────────┬───────────────────┘        └──────┬──────┘  │
│                   │                                    │        │
│                   │           DOMAIN CORE              │        │
│                   │    (no network / no storage I/O)   │        │
│                   │                                    │        │
│       ┌───────────▼────────────────────────────────────▼──────┐ │
│       │                       central                          │ │
│       │   CentralUnit + Coordinators + Registries + Scheduler  │ │
│       │   + State Machines + RPC Callback Servers (XML+BIN)    │ │
│       │                          ▲▲▲                           │ │
│       │                   internal EventBus                    │ │
│       │                          ▼▼▼                           │ │
│       │   ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌──────────┐ │ │
│       │   │ model   │  │ client  │  │ store   │  │  health  │ │ │
│       │   │ domain  │  │ reliab. │  │ caches  │  │  tracker │ │ │
│       │   └─────────┘  └─────────┘  └─────────┘  └──────────┘ │ │
│       └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

- **Northbound adapters**: driving side. They call into the domain
  through narrow per-handler interfaces (e.g. `DeviceIndex`,
  `HubIndex`, `DataPointWriter`). No northbound adapter touches
  SQLite stores or coordinators directly.
- **Southbound adapter**: driven side. The `client` package
  implements ports defined by the domain to talk to the CCU.
- **Domain core**: pure Go. Receives `context.Context` for
  cancellation. Cross-domain communication via the typed
  `EventBus`.
- **EventBus**: in-process, typed, generic, priority-aware. Does
  **not** cross process boundaries. Re-entrant publishes from a
  handler are deferred to preserve causal order; the buffer is
  unbounded but exposes a high-water gauge for operators.

### 4.2 Multi-CCU is structural, not optional

A single daemon process holds a `CentralRegistry` containing one
`CentralUnit` per configured CCU. Each `CentralUnit` owns its own
EventBus, coordinators, callback routes, metrics labels, and
audit/incident streams. There is no shared mutable singleton.

Northbound adapters are wired with the registry, not with a single
central — a multi-CCU deployment scopes by `central_name` in topics,
REST paths, and slog records. See ADR 0002.

### 4.3 Callback servers

Two cross-central listeners run regardless of the configured
interface set:

- **XML-RPC over HTTP** on `:8120` (default). Routes by URL path
  `/RPC2/<central_name>` with a regex allowlist (`^[A-Za-z0-9_-]+$`)
  as a hard gate against path-traversal misrouting.
- **BIN-RPC over raw TCP** on `:8129` (default). Routes by
  `interface_id` carried in the BIN-RPC envelope.

Both listeners support **fixed**, **OS-assigned** (`port: 0`), or
**range** (`port_range: [lo, hi]`) port modes. The **effective**
port (after OS assignment) is re-advertised to the CCU on every
`init()` call and every reconnect — a daemon restart that picks a
different ephemeral port is transparent to the CCU.

CUxD is a first-class push-capable interface in 0.1.0 — OpenCCU-Loom
runs its own native BIN-RPC stack and a BIN-RPC callback server.
There is no MQTT workaround and no polling fallback. This is a
deliberate divergence from aiohomematic.

### 4.4 Reliability layering (orthogonal)

The southbound `client` composes five orthogonal reliability
primitives, each with its own configuration:

```
InterfaceClient
  ├─ Circuit Breaker     (per transport)
  ├─ Retrier             (exponential backoff + jitter, recovery-aware)
  ├─ Throttle            (3-pool: read / write / control;
  │                       per-priority queue with bounded depth)
  ├─ Coalescer           (deduplicates concurrent calls to the same key)
  └─ PingPong tracker    (callback-channel health)
```

Reliability defaults are centralised in `pkg/hmreliability/` and
locked down by `TestRecordedReliabilityDefaults`, a CI-enforced
drift detector against the aiohomematic counterparts. Changing a
default requires updating both the constant and the snapshot test
in the same commit.

### 4.5 Persistence

- **SQLite** (`modernc.org/sqlite`, pure Go) for durable state
  (sessions, paramset cache, devices, incidents, audit). Pragmas:
  WAL for file-backed databases, `synchronous=NORMAL`,
  `busy_timeout=5000`, explicit connection pool sizing.
- **In-memory caches** for hot paths: dynamic data cache,
  visibility filter, paramset patches, master/link profile,
  device-details. Each cache uses `sync.RWMutex` + atomic counters
  to keep reads on the read-lock fast path.
- **Audit log** uses a durable sink with a bounded queue and a
  typed `ErrAuditOverflow` error for backpressure — silent drops
  are not allowed (the SPEC says "append-only").

### 4.6 Measurement history

A user who runs OpenCCU-Loom **without** Home Assistant has no
recorder to capture a time-series of sensor values. To serve that
audience without forcing an external stack, the daemon ships an
**opt-in, embedded measurement history** (default off). The driving
use case is charts in the Svelte SPA over the short-to-medium term;
long-term archival is served by an opt-in push exporter, not by the
embedded store. See **[ADR 0040](./docs/adr/0040-measurement-history.md)**
for the full design.

- **Capture point**: a recorder subscribes to
  `DataPointValueChangedEvent` on each central's EventBus, filters to
  numeric `VALUES` parameters, buffers, and batch-flushes — the same
  non-blocking flusher shape as the VALUES cache (§4.5).
- **Provenance guard**: only genuine *live* wire observations are
  recorded. Boot-time pseudo-values — a freshly created DP's zero
  default, a value replayed from the VALUES cache, or a source-only
  freshness flip — are rejected via the `hmenum.ValueSource`
  lifecycle (ADR 0019), **not** by filtering on the value, so a real
  `0` is kept. The sample timestamp is the wire-reception time, never
  the boot wall-clock.
- **Storage**: a dedicated `history.db` (its own WAL + migration
  series), separate from the config/session DB so an append-heavy
  writer never contends with config writes. Retention runs on the
  scheduler; rollup downsampling is a later additive step.
- **Surface**: a REST history endpoint with server-side bucketing
  feeds SPA charts; an opt-in `MeasurementExporter` seam (modelled on
  the span exporter, ADR 0037) ships a lean InfluxDB line-protocol
  implementation for users who already run Grafana/Influx.
- **Configuration**: one DB-tier `persistence.history` section,
  editable through the SPA like `persistence.values_cache` and
  `north.mqtt`. Export credentials are secrets (ADR 0027).

---

## 5. Enumerations & Wire Identities

All enums live under `pkg/hmenum`. The string values are emitted
verbatim matching aiohomematic for **wire compatibility** — this is
non-negotiable.

```go
// pkg/hmenum/interface.go
type Interface string

const (
    InterfaceBidCosRF       Interface = "BidCos-RF"
    InterfaceBidCosWired    Interface = "BidCos-Wired"
    InterfaceHmIPRF         Interface = "HmIP-RF"
    InterfaceVirtualDevices Interface = "VirtualDevices"
    InterfaceCUxD           Interface = "CUxD"
    InterfaceGroups         Interface = "Groups"
)
```

`HmIP-Wired` is deliberately absent — the CCU exposes a single
HmIP-RF XML-RPC service that hosts both RF and Wired devices. The
Wired flavour is a `ProductGroup` (`ProductGroupHmIPW`), derived
from the device model-name prefix via `hmenum.ProductGroupForModel`.
See ADR 0023.

Code-level reference for the full enum set lives in
`pkg/hmenum/`. The point of this section is to pin **the
constraints**:

1. **Wire-format identity** with aiohomematic. The string values
   may not be renamed, reformatted, or aliased — every CCU dialect
   we support speaks the upstream strings. Contract tests enforce
   this.
2. **`CommandPriority.Critical = 0`** is a deliberate design
   choice mirroring aiohomematic. **Never** check `if priority !=
   0` as "is set" — use the typed comparison
   `if priority == hmenum.CommandPriorityCritical`. This is a
   common Go gotcha; the contract tests catch the most common
   mistakes.
3. **Bitmask enums** (`Operations`, `Flag`) carry helper methods
   (`IsReadable()`, `IsWritable()`, `IsService()`, `IsInternal()`).
   Bitmask comparisons via `&` are not allowed in callers — go
   through the helper.
4. **Classification sets** (e.g. `XMLRPCInterfaces`,
   `BINRPCInterfaces`, `InterfacesSupportingFirmwareUpdates`,
   `LinkableInterfaces`) drive runtime dispatch decisions and are
   guarded by contract tests so a refactor of one set cannot break
   downstream classification.

### 5.1 Diverging classification sets vs aiohomematic

| Set | aiohomematic | OpenCCU-Loom 0.1.0 | Reason |
|---|---|---|---|
| `INTERFACES_REQUIRING_JSON_RPC_CLIENT` | `{CUxD, CCU-Jack}` | `{}` (empty) | OpenCCU-Loom uses native BIN-RPC for CUxD; CCU-Jack is dropped (no pull-only path). |
| `INTERFACES_SUPPORTING_RPC_CALLBACK` | XML-RPC interfaces only | + CUxD | OpenCCU-Loom runs its own BIN-RPC callback server. |
| `DEFAULT_INTERFACES_REQUIRING_PERIODIC_REFRESH` | `{CUxD, CCU-Jack}` | `{}` (empty) | Every interface in 0.1.0 supports push. |

These differences exist because OpenCCU-Loom is push-native; the
contract tests in `tests/contract/` codify each one.

---

## 6. Matter Bridge

**Status (2026-05-06)**: native-Go Matter is **end-to-end functional
through full chip-tool commissioning** — all 19 commissioning stages
clear with `chip-tool --bypass-attestation-verifier true`, including
a `Secure Pairing Success` after AddNOC. Vendor-supplied DAC/PAI/CD
via the config file flip on production validation. Implementation
form, prioritised cluster subset, DP→cluster mapping, and effort
estimates live in
**[ADR 0012](docs/adr/0012-matter-pure-go-implementation.md)**.
Bring-up bug lessons (7 structural bugs from the live chip-tool
smoke runs, waves 7–12) are recorded in
**[ADR 0013](docs/adr/0013-matter-commissioning-bring-up.md)**. The
matter packages under `internal/north/matter/` and the
`internal/north/matter/bridge/` runtime package are all populated
and tested (including the Subscribe-with-events foundation, the
GenericSwitch / Schedules / AdministratorCommissioning clusters, and
the REST API); the daemon wires the bridge behind
`cfg.North.Matter.Enabled` (default off; operator opt-in via config).

### 6.1 What's shipped

- **Substrate**: TLV codec, MRP (counter / window / retransmit /
  ack-tracker), UDP transport, message framing (Spec §4.4 + §4.12).
- **Secure Channel**: AES-CCM-128, Spake2+ (PASE) verifier with
  Pake1/2/3 + PBKDFParamRequest/Response wire codec, Sigma (CASE)
  responder, channel session.Encrypt/Decrypt with replay-window.
- **Interaction Model**: `Read` / `Write` / `Invoke` / `Subscribe`
  (initial-report) / `TimedRequest` all fully wired in
  `internal/north/matter/bridge/receive.go` →
  `endpoint.TopologyDispatcher` → cluster servers → `bridge/reply.go`.
- **Cluster servers**: 12 generic measurement servers in
  `internal/north/matter/cluster/measurement/` (Temperature,
  Humidity, Illuminance, Pressure, BooleanState, OccupancySensing,
  CO2, PM2.5, PM10, PowerSource, ElectricalPower, ElectricalEnergy)
  + 12 bridge-core clusters (Descriptor, Binding, BasicInformation,
  BridgedDeviceBasicInformation, GeneralCommissioning,
  NetworkCommissioning, GeneralDiagnostics, DiagnosticLogs,
  OTASoftwareUpdateRequestor, PowerSource, OperationalCredentials,
  GroupKeyManagement) + 4 P0 application-cluster wire codecs (OnOff,
  LevelControl, WindowCovering, Thermostat) + 2 P1 wire codecs
  (ColorControl, DoorLock).
- **Custom-DP projections**: switch / light (incl. ColorTemp /
  Color / RGBW) / cover (Cover, Blind, Garage) / climate / lock /
  siren (incl. SmokeSiren) all implement
  `interfaces.MatterEndpointSource` + `MatterClusterServer`.
  `switchdev.Switch` accepts optional Power/Energy sources via
  `AttachPowerSource` / `AttachEnergySource`.
- **mDNS**: operational (`_matter._tcp`) + commissionable
  (`_matterc._udp`) records via the mdns advertiser; the bridge
  publishes the operational record at Start.
- **Per-Exchange-PASE**: `NewPaseAdapterWithFactory` allocates a
  fresh verifier per Pake1; `PaseHandlerProvider` allows concurrent
  PASE dispatch.
- **Operational session manager**: `OpenFromPase` + `OpenFromSigma`
  register sessions in the bridge's `SessionLookup` so subsequent
  encrypted IM traffic dispatches without further wiring.

### 6.2 Architecture (unchanged from ADR 0012)

**Architecture principle: rich model, dumb bridge.** Same pattern as
ADR 0011 (MQTT) and ADR 0010 (HA Discovery): the projection from a
DataPoint to a Matter endpoint / cluster lives on the DataPoint
itself, not in `internal/north/matter/`. The bridge owns the Matter
wire format (TLV codec, MRP framing, Secure Channel, IM dispatcher);
the model owns *what each DP means in Matter terms* via interface
methods declared in `internal/model/{custom,generic,calculated}/.../matter.go`.
The interface contracts (`EndpointSource`, `ClusterServer`,
`MeasurementClass`, `MeasurementSource`) live in
`pkg/interfaces/matter.go`. ADR 0012 §"Source surface" is the
specification of those types.

### 6.3 Forward-compat

The domain model carries enough semantics that the Matter bridge ships
as **additive declarations on existing model types**, not as a refactor
of `central`, `client`, or `model`:

- Every Custom DP (climate, cover, light, lock, switch, siren)
  already exposes the semantic methods (`IsOn`, `Brightness`,
  `Position`, `TiltPosition`, `Setpoint`, `Mode`, `IsLocked`,
  `Status`) that the Matter cluster projections will reference. No
  new state fields are required — each package adds a `matter.go`
  implementing `EndpointSource` / `ClusterServer` against the
  *existing* surface.
- Generic DP routing rides on the same `ParameterClass` classifier
  already used by `internal/payload` for MQTT — the Matter
  `MeasurementClass` is computed once at materialisation from the
  same input, so a temperature-Sensor and a humidity-Sensor are not
  matched by name in the bridge.
- Calculated DPs declare their projection next to the formula in
  `internal/model/calculated/matter.go`; adding a new calculated
  sensor post-0.1.0 is a model-package change, not a bridge change.
- `DeviceCreatedEvent` / `DeviceRemovedEvent` give the endpoint
  assembler in `internal/north/matter/endpoint/` the trigger to
  build / tear down bridged endpoints; it does so by walking the
  model and asking each DP for its projection, not by hard-coding
  type switches.

The original "bridge subscribes to events and translates" framing
(pre-ADR-0012) is replaced by the rich-model framing above. ADR 0012
§"Custom / Generic / Calculated DP ↔ Matter cluster mapping table"
is the complete specification — every Custom / Generic / Calculated
DP currently in the model is accounted for, and no model-layer
*structural* refactor is required.

### 6.4 MCP north-bound bridge

Matter is one of several north-bound adapters over the same domain
core (alongside REST, WebSocket, and MQTT). A further bridge — an
**MCP server** that exposes the domain to LLM agents as tools over a
Streamable-HTTP transport — ships in `internal/north/mcp/`, default-off
and read-only by default. It follows the same "rich model, dumb
adapter" principle as the Matter bridge and the MQTT plane: each tool
projects the same domain the REST surface serves, scoped per central.

The adapter mounts on the REST listener at `North.MCP.Path` (default
`/mcp`) behind the same auth chain, gated by `North.MCP.Enabled`. Read
tools (`list_centrals`, `list_devices`, `get_device`, `read_paramset`,
`get_health`, `list_audit`) are always registered; write tools
(`set_datapoint`, `write_paramset`, `trigger_program`) only when
`North.MCP.AllowWrites` is also set, and the device-touching writes
refuse to act on a device the named central does not own (ADR 0002).
Each tool additionally gates on its own dependency. The `mcp.v1` /
`mcp.write.v1` capability tokens surface the posture through `GET /info`.
A `list_incidents` tool is deliberately omitted for now — the daemon's
incident source is still a stub; it lands when real incidents do.

- **[ADR 0025](docs/adr/0025-mcp-northbound-adapter.md)** — the
  production MCP adapter: tool / resource shapes, multi-CCU scoping,
  the two-switch (`Enabled` / `AllowWrites`) read-only-default
  posture, capability handshake, and auth reuse.
- **[ADR 0026](docs/adr/0026-mcp-dev-mode.md)** — a separate,
  build-tag-gated (`dev_mcp`) dev-mode introspection surface
  (EventBus tap, reliability state, cache dumps, godevccu control)
  that never compiles into release artefacts.

---

## 7. Resolved Decisions & Risk Register

### 7.1 Resolved decisions

These were settled in the project's first three planning rounds and
have not changed since. Anything that overrides one of them needs
an ADR.

| # | Question | Resolution |
|---|---|---|
| Q1 | Default UI locale | `de` default, `en` via `Accept-Language` |
| Q2 | CSS / UI framework | Svelte 5 SPA primary (Tailwind 4 via Vite); HTMX bootstrap surface uses a tiny hand-rolled CSS for login/setup/health/about only |
| Q3 | MQTT QoS defaults | Sensible defaults (0 state, 1 discovery / commands), per-interface and per-category overridable in YAML |
| Q4 | Matter implementation form | Pure-Go, hand-rolled (ADR 0012). On-network commissioning only in 0.1.0. No CGo, no Node.js / Rust sidecar. |
| Q5 | Backup strategy | CLI subcommand + FS-level parallel (both supported) |
| Q6 | Raw MQTT plane | Yes — raw and HA Discovery planes emitted in parallel |
| Q7 | Transport licensing | Native Go XML-RPC / BIN-RPC / JSON-RPC; project licensed MIT (ADR 0001) |
| Q8 | Setup wizard scope | Admin user + CCU connection + language/theme (no MQTT in wizard) |
| Q9 | HA Add-on packaging | Delivered post-0.1.0 — Home Assistant add-on (amd64/aarch64/armv7) built on the HA base image (s6-overlay + bashio), with Ingress (sidebar panel) + direct port, packaged from `packaging/ha-addon/`; the repo doubles as a HA add-on repository (root `repository.yaml`). Release build toggled by `BUILD_HA_ADDON` |
| Q10 | RaspberryMatic Add-on | Delivered post-0.1.0 — CCU/RaspberryMatic add-on (amd64/arm64/armv7) packaged from `packaging/ccu-addon/` and attached to each release (ADR 0012 channel) |
| Q11 | Multi-CCU | Supported from 0.1.0 (ADR 0002) |
| Q12 | Hot-reload | Logging (level, format) and CORS via file-watcher; **entire `north.mqtt` section is hot-swappable** (broker URL, credentials, topic base, discovery toggles) — applied automatically on file-watcher pickup or on demand via `POST /admin/mqtt/reload`. Structural CCU/Callback/REST listen changes still need restart |
| Q13 | CUxD transport | Native BIN-RPC + BIN-RPC callback server. No MQTT workaround. |
| Q14 | OpenAPI default | Validation **on** by default in 0.1.0; the daemon refuses requests that don't match `assets/openapi.yaml`. Spec is authoritative for the REST surface. |
| Q15 | Audit durability | `audit.NewDurableSink` with bounded queue + typed `ErrAuditOverflow` is the default. Silent drops are not allowed. |

### 7.2 Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Device-profile edge cases differ between aiohomematic and OpenCCU-Loom | Medium | High | Generated parity tests block drift; golden-file replay of real-device sessions; cross-stack model snapshot diff (0 drift required at release) |
| XML-RPC callback race on startup | Medium | Medium | Shared callback server starts before any `initProxy()`; contract test |
| SQLite corruption under power loss | Low | High | WAL mode + `synchronous=NORMAL`; `PRAGMA integrity_check` at startup; documented backup guidance |
| Pure-Go SQLite performance regression | Low | Medium | Benchmark in dev; build-tag fallback path documented but unused |
| Auto-generated profile registry brittle across aiohomematic versions | Medium | Medium | Pinned aiohomematic version in generator; re-generate + diff review on bump |
| Multi-CCU shared callback listener collisions | Medium | Medium | Path-allowlist regex on XML-RPC routes; `interface_id` routing on BIN-RPC; multi-CCU contract tests |
| BIN-RPC listener behind NAT / container | Medium | High | `rpc_callback.host` is mandatory if any CUxD interface is enabled; startup-time validator fails fast |
| BIN-RPC framing drift from CUxD wire format | Low | Medium | Fuzz tests for the codec; integration tests against real CUxD |
| Setup-wizard YAML writer destroys user comments | Medium | Low | Comment-preserving writer; round-trip contract test |
| MQTT broker restart drops retained discovery | Medium | Medium | On reconnect, republish all discovery + availability + state; `mqtt_discovery_state` dirty flag |
| EventBus handler recursion fills the deferred buffer | Low | High | Buffer is unbounded by design (concurrent goroutine publishes share the same path); a `DeferredHighWater` gauge surfaces the depth + an alert threshold writes one slog.Error per process when crossed. Operators alert on the gauge. |
| Audit overflow under burst load | Low | Medium | `audit.NewDurableSink` returns typed `ErrAuditOverflow`; `audit.dropped` health gauge surfaces the overflow rate |
| Spec drift between `assets/openapi.yaml` and code | Medium | Medium | Validator middleware enforces the spec at request time; coverage contract test asserts every router route exists in the spec |
| Reliability constants drift vs aiohomematic | Medium | Medium | `pkg/hmreliability/` central registry; `TestRecordedReliabilityDefaults` snapshot pinned to upstream values |

---

## 8. Observability

The daemon ships a five-pillar observability surface so an agent (or
human operator) can reconstruct any incident without further user
interaction. Full design rationale: [ADR
0017](./docs/adr/0017-logging-and-diagnostics.md).

1. **Trace-propagating structured logs** — every `slog.Record`
   carries `trace_id` (32-hex W3C), `span_id` (16-hex), and
   `parent_span_id` alongside the existing `request_id` / `operation` /
   `central_name` / `interface_id` / `device_address` fields. The REST
   middleware accepts an inbound `traceparent` header and echoes one
   on every response.
2. **Redaction at the handler boundary** — `pkg/hmlog.RedactingHandler`
   masks attribute values whose keys match a built-in allowlist
   (`password`, `*token*`, `*secret*`, `cookie`, `session_id`, …).
   Nested `slog.Group` attributes are recursed.
3. **Per-subsystem level registry** — `pkg/hmlog.LevelRegistry`
   stores TTL-bounded overrides keyed by dot-separated logger paths.
   Configurable statically via `logging.overrides` in
   `config.yaml`; mutable at runtime via
   `PUT /api/v1/diagnostics/log-levels/{path}`. Every change lands
   in the audit ledger.
4. **Integration diagnostics dump** —
   `GET /api/v1/diagnostics?anonymize=0|1` returns a single JSON
   envelope (build metadata, health snapshot + score, interfaces,
   recent incidents, system-status ring, current overrides).
   Anonymisation hashes device-address-shaped values to a stable
   `anon:<12-hex>` token by default.
5. **RAM-buffered capture sessions** — `internal/diagnostics.Manager`
   records every log record into a 64 MiB ring (`hmlog.CaptureSink`)
   for the requested window (max 30 min, max one parallel capture).
   Stop produces a `tar.gz` containing the ndjson stream plus
   `capture.meta.json`. Archives expire 24 h after Stop; ≤ 5
   rotating archives kept.

The SPA's `Diagnostics` view orchestrates all five from one panel,
with a Diagnose-Dump-Download button, a Logging tab (override list +
apply form), and a Capture tab (start / stop / list with download
link).

### 8.1 Health parity with aiohomematic

The per-interface detail surface ports `aiohomematic.central.health.
ConnectionHealth` 1:1 (see [ADR 0018](./docs/adr/0018-health-parity-with-aiohomematic.md)):

- `Tracker.RecordRequest(name, success)` drives
  `LastSuccessfulRequest` / `LastFailedRequest` /
  `ConsecutiveFailures`. Wired into the `TransportObserver` slot via
  `internal/client/observer.NewHealth` — both XML-RPC interfaces and
  the JSON-RPC hub session funnel through it. Semantic CCU faults
  (`Unknown Parameter`, validation rejections) are filtered the same
  way the circuit breaker does, so a healthy interface stays healthy
  when something polls a write-only data point.
- `Tracker.SetRecoveryFlag` toggles `InRecovery` on
  `RecoveryStarted` / `Completed` / `Failed` events;
  `ResetReconnects` clears the attempt counter on every successful
  `ClientStateChanged → Connected`.
- `Tracker.ClientScore(name)` computes the 40 % state + 30 % circuit
  + 30 % recent-activity weighted score aiohomematic exposes as
  `health_score`. The circuit pillar walks the per-component sample
  history backwards to find the most recent `breaker …` annotation,
  so an interleaved `event-received` sample cannot mask an open
  breaker.
- `Tracker.PrimaryClientHealthy()` defaults to a `HmIP-RF`
  substring match; operators on BidCos-RF-only installations
  override the pick via the per-central `primary_interface`
  config key. The daemon calls
  `Tracker.SetPrimaryInterface(cc.PrimaryInterface)` at boot
  when the key is non-empty.
- `Tracker.CentralScore(central)` aggregates over every component
  whose name carries the central as a substring — current wiring
  prefixes interface ids with the central name, so multi-CCU
  setups get a per-CCU score for free.

Coverage producers in place:

- **South-bound transports** — per-interface event-bus subscription
  (`WireHealth`) feeds `RecordRequest` for every XML-RPC / BIN-RPC /
  JSON-RPC call.
- **Central heartbeat** — `Tracker.Record("central", …)` at boot;
  the heartbeat job keeps the sample fresh.
- **SQLite store** — `internal/store/sqlite.StartHealthProbe`
  (30 s cadence, two-sample escalation on transient errors).
- **MQTT broker** — `internal/north/mqtt.StartHealthProbe`
  (30 s cadence; healthy when the broker session is up + the
  bridge has acknowledged at least one publish since reconnect).
- **Matter bridge** — `internal/north/matter/bridge.StartHealthProbe`
  (30 s cadence; healthy when the UDP listener is bound).
- **Scheduler + REST/WS** — surfaced via gauges
  (`scheduler.jobs`, `scheduler.failures`, `rest.5xx`,
  `rest.4xx`, `rest.requests_total`, `ws.subscribers`) rather
  than per-request `RecordRequest`. The gauges feed the
  Diagnostics dump + the Client-Health card directly.

---

## Glossary

- **CCU** — Central Control Unit (Homematic CCU3, RaspberryMatic).
- **CUxD** — Custom-Universal-Extension-Driver, an HM extension
  daemon that exposes additional device classes via BIN-RPC.
- **DataPointKey** — `(interface_id, channel_address, paramset_key,
  parameter)` — the canonical identity of a value-bearing endpoint.
- **DeviceProfile** — a set of canonical data points (e.g. light,
  cover, climate) abstracting a Homematic device's parameters.
- **godevccu** — pure-Go in-process CCU simulator used for
  integration tests; eliminates the Python dependency
  (`pydevccu`) at test time.
- **Hexagonal architecture** — domain core kept free of I/O;
  adapters at the boundary translate to the outside world.
- **Hub data point** — a non-device value such as a system variable
  or program — exposed by the CCU but not tied to a physical
  device.
- **openccu-data** — the upstream metadata bundle (translations,
  easymodes, profiles); embedded under the eQ-3 license,
  aggregated per ADR 0003.
- **Paramset** — a named group of parameters for a channel:
  `MASTER` (config), `VALUES` (runtime), `LINK` (peer-config),
  `SERVICE` (service messages), and the synthetic OpenCCU-Loom
  groups `CALCULATED`, `COMBINED`, `DUMMY`.

---

*This document is intentionally short. If you need more detail, the
table at the top points to the authoritative source.*
