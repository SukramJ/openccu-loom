# ADR 0020 — External-client wire contract

- **Status**: accepted
- **Date**: 2026-05-24
- **Related**:
  [ADR 0009 — service / method / command topics](./0009-service-method-command-topics.md),
  [ADR 0011 — MQTT topic and payload architecture](./0011-mqtt-topic-and-payload-architecture.md),
  [`assets/openapi.yaml`](../../assets/openapi.yaml),
  [`assets/wsapi.json`](../../assets/wsapi.json),
  [`docs/external-clients/topic-hierarchy.md`](../external-clients/topic-hierarchy.md),
  [`docs/external-clients/asks.md`](../external-clients/asks.md)

## Decision

OpenCCU-Loom maintains a stable, machine-readable contract for
external wire clients (Python, TypeScript, Rust, … — any process
that talks to the daemon over the north-bound REST + WebSocket
surface). The contract is owned by three artefacts with non-overlapping
responsibilities:

1. **`assets/wsapi.json`** owns every WebSocket frame the daemon
   emits — both commands and broadcasts. The catalogue lists each
   broadcast with its `topic` pattern and a `payload` reference
   naming a schema in OpenAPI. A root-level `envelope` field
   documents the frame shape (`{topic, type, ts, payload}`).
2. **`assets/openapi.yaml`** owns every JSON schema referenced by
   either REST endpoints or WebSocket broadcasts. Push-payload
   structs live under `components.schemas` next to REST DTOs;
   `wsapi.json` references them by name via the broadcast entry's
   `payload` field.
3. **`docs/external-clients/topic-hierarchy.md`** is the normative
   human-readable reference for topic namespaces, wildcard
   semantics, and the per-event topic table. It cross-links to the
   two machine-readable artefacts above.

The contract surfaces an explicit version through `GET /api/v1/info`
in two fields: `api_version` (semver, evolves independently of the
daemon build version) and `capabilities` (string set; clients gate
features on capability presence rather than on `api_version` alone).

The daemon's role hierarchy (`viewer ⊂ operator ⊂ admin`) is
expressed in OpenAPI via a dedicated `openIdConnect` security scheme
whose scope vocabulary mirrors `Identity.role`. Per-operation
`security:` blocks declare the minimum role required; `basicAuth`
and `bearerAuth` schemes remain available alongside (OpenAPI 3.x
cannot natively express scopes on non-OAuth schemes — the role
constraint is still enforced server-side regardless of the scheme
used).

`pkg/hmapi` (the in-process Go embedding facade) is **out of scope**
for this contract. Wire clients consume the REST/WS surface; Go
processes that embed the daemon as a library consume `pkg/hmapi`.
The two surfaces evolve independently.

## Context

The first concrete consumer of the contract is `py-openccu-loom-client`
— the Python SDK that will replace `aiohomematic` inside the Home
Assistant component `homematicip_local`. Before this ADR the wire
surface had several gaps that forced the SDK author to read Go
source:

- `wsapi.json` listed 85 commands but only 6 of 12 broadcasts (the
  Matter ones). The six **core** broadcasts the daemon already emits
  — `datapoint.value_changed`, `custom_data_point.state_changed`,
  `central.state_changed`, `system.status_changed`, plus the two
  hub broadcasts surfaced for the first time in this PR
  (`hub.sysvar_changed`, `hub.program_executed`) — were not in the
  catalogue.
- Push payload structs existed as Go types in
  `internal/north/rest/ws/payloads.go` but had no
  `components.schemas` entries. Codegen tools could not see them.
- Topic patterns (`device.{addr}.channels.{no}.data_points.{param}`,
  `central.{name}.state`, …) were documented only as Go string
  builders and one inline comment in `payloads.go`.
- The `Problem` schema's `type` URIs were not enumerated. Clients
  had to grep `internal/north/rest/problem/problem.go` to know
  which problem kinds to map to typed exceptions.
- Per-endpoint role requirements (`viewer/operator/admin`) lived in
  `cmd/openccu-loom/daemon.go` as `RequireOperator` / `RequireAdmin`
  middleware wraps but were nowhere visible in the OpenAPI document.
- `GET /info` returned only the daemon build version; there was no
  contract-version axis a client could pin against, and no capability
  list it could feature-detect on.

The backlog at
[`docs/external-clients/asks.md`](../external-clients/asks.md)
catalogues each gap with source-line citations. This ADR records
the architectural decisions that close the foundation gaps and
explicitly defers the rest.

## Consequences

### What ships with this ADR (Welle 1 + 2)

- **A1+A2+C2** — wsapi.json carries 12 broadcast entries (6 core, 6
  Matter); core broadcasts reference `components.schemas` payloads in
  openapi.yaml via the new `payload` field. The WS frame envelope is
  documented at `wsapi.json` root.
- **A3** — full topic hierarchy reference at
  `docs/external-clients/topic-hierarchy.md`, including wildcard
  semantics, subscription operations, reserved namespaces, and the
  catalogue of broadcasts the daemon does **not** yet emit.
- **A4** — `components.schemas.Problem.type` and `components.schemas.Problem.code`
  are enumerated against the canonical list in
  `internal/north/rest/problem/problem.go` (11 entries).
- **B3** — `GET /info` surfaces `api_version` (`1.0.0`) and
  `capabilities` (always-on: `rest.v1`, `ws.broadcasts.v1`,
  `errors.problem_details.v1`; conditional: `mqtt.discovery.v1`,
  `matter.bridge.v1`, `auth.oidc.v1`).
- **E3** — `openIdConnect` security scheme defines the role-scope
  vocabulary; ~58 operations carry per-operation `security:` blocks
  expressing the minimum role (viewer-level operations inherit the
  top-level default).
- **G1** — `SysvarChangedEvent` and `ProgramExecutedEvent` are
  plumbed from the central event bus to the WebSocket hub via the
  new `internal/north/rest/ws/hub_events.go` subscriber, mirroring
  the existing `SystemStatusSubscriber` shape. Annonciation in
  wsapi.json + payload schemas in openapi.yaml.

### What is explicitly deferred to follow-up PRs

Each deferral has a corresponding entry in `asks.md`:

| Ask | Reason for deferral |
|---|---|
| **B1** — sequence / replay semantics on the WS envelope | Architectural feature requiring buffered events + GC policy + possibly persistent storage; one feature, one PR. |
| **B2** — `kind: "initial" \| "change" \| "refresh"` discriminator on push payloads | Touches every payload emitter and every consumer; standalone PR for a single sweep. |
| **C1** — JSON-Schema export of `pkg/hmenum` + `pkg/hmtypes` | New codegen tool + Makefile target + release asset publishing. |
| **C3** — `openccu-loom-types` PyPI sister-repo | Out-of-repo work (separate `SukramJ/openccu-loom-types-py`). |
| **C4** — three-way snapshot diff (aiohomematic ↔ loom-go ↔ loom-py-client) | Requires `py-openccu-loom-client` to exist first. |
| **D1** — bulk-value read REST endpoint | New endpoint + handler + filter semantics. |
| **D2** — `paramset.put_atomic` WS command + atomicity guarantees | Server-side semantic change in the paramset write path. |
| **E1** — mDNS service advertisement | Runtime feature touching the daemon lifecycle. |
| **E2** — `POST /auth/tokens` (token-create endpoint) or documented CLI path | Auth-surface design decision. |
| **F1** — `GET /system/ccu` enriched CCU metadata | New endpoint + data plumbing from the central registry. |
| **G2** — `GET /config` exposes optional-settings / program-markers / sysvar-markers | Configuration surface enrichment. |
| **H1** — streaming `/snapshot` (NDJSON or cursor pagination) | Replaces a self-DoS endpoint; touches handler + serialiser. |
| **H2** — structured `GET /diagnostics` JSON view | Schema design decision, but the tendency (see asks.md) is in favour. |
| **Matter broadcast payload schemas** | The 6 Matter broadcasts in wsapi.json carry no `payload` reference yet; openapi.yaml schemas are TBD pending Matter-surface stabilisation. |

The deferrals are catalogued so that future maintainers (and the
client SDK author) can see the open items at a glance without
mining commit history.

### Naming consistencies introduced

- WebSocket broadcast `name` field in `wsapi.json` corresponds to the
  envelope's `type` field for core broadcasts. Matter broadcasts
  currently set `type` to the trailing segment after `matter.`
  (e.g. `type: "exposable_changed"` on topic `matter.exposable_changed`)
  — see `internal/north/rest/handlers/matter_events.go:typeFromTopic`.
  This asymmetry is preserved here; future PR may align Matter to
  the full-name convention.

### What clients can now rely on

- The set of currently-emitted broadcasts is fully enumerated in
  `wsapi.json` (filter `commands[?(@.kind=="broadcast")]`).
- Each push payload has a JSON schema retrievable via the entry's
  `payload` field name.
- Topic patterns are documented with placeholder syntax and
  matching semantics.
- Each REST operation either carries a `security:` block declaring
  its minimum role (operator / admin), inherits the top-level
  default (viewer), or is `security: []` (public — login flow,
  health, info, OIDC handshake).
- `api_version` + `capabilities` give clients a stable feature-detection
  contract independent of daemon build version.
- The `Problem.type` enum is the closed set of error kinds the
  daemon emits; the `X-Problem-Code` response header carries the
  short code for header-only inspection.

### What clients should still expect to change

- B1 sequencing will, when implemented, add a `seq` field to the WS
  envelope. Clients should tolerate unknown envelope fields.
- B2 kind classification will, when implemented, add a `kind` field
  to push payloads. Clients should tolerate unknown payload fields.
- Streaming `/snapshot` (H1) will introduce an `application/x-ndjson`
  alternate response shape; the current full-JSON shape stays as a
  convenience.

## Alternatives considered

### A. `x-required-role` extension instead of OIDC scopes

Earlier draft of `asks.md` flagged this as the "Extension-Friedhof"
approach. Rejected because external OpenAPI codegen tools generally
ignore `x-` extensions, so the role information would not flow into
the generated SDK. OIDC scopes are recognised by every conformant
generator (even when the actual auth scheme used is bearer / basic
— the scope value is still emitted as documentation).

### B. Separate `wsapi-events.json` for broadcasts

Considered for symmetry with the existing `wsapi.json` ↔
`openapi.yaml` split. Rejected because broadcasts and commands share
the same envelope and the same client wire path; splitting them
across two files creates two sources of truth where one suffices.
`wsapi.json` owns *all* WS frames; OpenAPI owns *all* JSON schemas.

### C. Embed Python types directly under `pkg/`

Considered for monorepo convenience (analogous to how Go consumers
get types via `pkg/hmapi`). Rejected — `pkg/` is a Go-tooling
convention. Python CI in a Go-only repo would complicate
`make lint`, `make test`, `goreleaser`. PyPI sister-repo (asks.md
C3) is the right hosting form; CI triggers couple versions, not the
monorepo.

### D. Bundle B1 (replay semantics) into this PR

Considered because the asks.md TL;DR places it second behind A1-A3.
Rejected because B1 is a single feature (buffered events + replay
protocol + GC policy) that deserves its own design discussion,
ADR, and review pass. Bundling it would force this PR into a
multi-week review.

## Migration impact

No breaking wire change. The contract additions are purely additive:

- Existing clients that don't read `api_version` / `capabilities` /
  per-operation `security:` continue to work.
- Existing WS subscribers continue to receive the events they
  always did; the new `hub.sysvar_changed` and `hub.program_executed`
  broadcasts are emitted to topics no current subscriber filters on.
- The OpenAPI top-level `security:` already included `bearerAuth` /
  `basicAuth`; adding `openIdConnect` as a third alternative does not
  change the requirements for clients using either of the first two.

Operators see no behaviour change. The role enforcement that the new
`security:` blocks document was already in place via the router's
`pr.With(op)` / `pr.With(admin)` middleware wraps — the OpenAPI
annotation is documentation catching up with reality.
