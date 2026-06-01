# Daemon-side optimizations to simplify the aiohomematic drop-in

Tracks the daemon-side changes that let `py-openccu-loom-client`
replace `aiohomematic` as the backend for the `homematicip_local` Home
Assistant integration with the least client-side logic.

**Context.** The single largest blocker in the client is the
*categorized data-point model* — the `unique_id` / `category` /
`data_point_type` / `registered` bookkeeping that HA spawns entities
from (`query_facade.get_data_points(data_point_type=…)`). The compat
adapter currently stubs that surface with `NotImplementedError`.

## Decision: Strategy B (category on the wire)

There were two ways to build the categorized model:

- **Strategy A** — the client re-derives categories from raw paramsets
  (a copy of ~15 KLOC of aiohomematic model logic). Optimizes
  *aiohomematic*; guarantees drift between the two stacks.
- **Strategy B** — the daemon ships the category on the wire. The
  daemon **already computes it internally**: `pkg/hmenum/datapoint.go`
  defines 31 `DataPointCategory` constants plus the authoritative
  `CategoryToType` map (`DataPointCategory` → `DataPointType`), and the
  MQTT discovery layer already consumes the category for component
  routing (`internal/north/mqtt/discovery.go`). The client then works
  declaratively off a single source of truth.

**Strategy B is the chosen path.** It reuses what the daemon already
knows, keeps a single categorization implementation, and removes
almost all client-side categorization. The tasks below pursue B and
are ordered by leverage. This direction is governed by the external
wire contract in `docs/adr/0020-external-client-wire-contract.md`.

---

## P1 — Expose category + functional type on generic DP payloads

The biggest single lever. `CustomDPSummary` already carries `category`
(`internal/north/rest/handlers/custom_data_points.go:60`); the generic
DP surface does not.

### Field naming — avoid the `type` collision

`DataPointSummary` **already** carries:

- `operations` (`{read,write,event}`) —
  `internal/north/rest/handlers/devices.go:170`. **No work needed; the
  earlier task list wrongly listed this as missing.**
- `type` — the **CCU descriptor TYPE** (`BOOL` / `INTEGER` / `FLOAT` /
  `ENUM` / …), `devices.go:177`. This is the wire primitive type, *not*
  the HA functional type.

The functional type the client needs is the `CategoryToType` output
(`light` / `cover` / `climate` / `sensor` / …). Because `type` is
taken, the new field **must** be named `data_point_type` — reusing
`type` would silently overload an already-published field and break
the SPA's widget-primitive resolver.

### Tasks

- [ ] **Add `category` + `data_point_type` to `DataPointSummary`** —
      `internal/north/rest/handlers/devices.go:145`. Source `category`
      from the DP's internal `Category()`; derive `data_point_type` via
      `hmenum.CategoryToType[category]` (`pkg/hmenum/datapoint.go:76`).
      Mirror the JSON-tag style of the existing `CustomDPSummary.category`
      field. Update `assets/openapi.yaml` (the `DataPointSummary`
      schema) in the same change.
- [ ] **Add `category` + `data_point_type` to
      `DataPointValueChangedPayload`** —
      `internal/north/rest/ws/payloads.go:33`. A value-changed event
      should be classifiable without a prior catalogue lookup, so a
      client that reconnects mid-stream can route it immediately.

      *Trade-off — make this opt-in.* Category and functional type are
      quasi-static DP properties; stamping them onto every value-changed
      event is redundant wire weight on the highest-frequency message on
      a busy CCU. The client can cache both from the snapshot catalogue
      and only needs them inline for the mid-stream-reconnect case.
      Gate the extra fields behind a subscription flag (e.g. a
      `classify` option on the WS subscribe frame) rather than always
      emitting them, and document the redundancy-vs-lookup choice on the
      frame schema. Default off.
- [ ] **Regenerate the schemas into `openccu-loom-types`** and bump the
      version pin the client depends on (currently `>=0.1.3,<0.2`).
- [ ] **Add a contract test** asserting every generic DP in a fixture
      snapshot carries a non-empty `category` and a `data_point_type`
      that round-trips through the published schema and matches
      `hmenum.CategoryToType`.

*Impact:* removes the client's need to port aiohomematic's
categorization model — the `NotImplementedError` entity-spawn block
becomes a declarative read.

## P1 — Stable, versioned enum catalogue

`homematicip_local` imports `aiohomematic.const.DataPointCategory` and
filters on it directly, so the daemon's enum must map 1:1 and stay
stable. Once `DataPointCategory` / `DataPointType` are on the wire,
**any change to a category value is a breaking wire change.**

- [ ] **Verify `DataPointCategory` / `DataPointType` parity** between
      `assets/schemas/enums.json` (the published catalogue:
      `DataPointCategory` at the `enums.json` entry, `DataPointType`
      following it) and the values HA filters on (light, cover, climate,
      lock, siren, valve, switch, sensor, binary_sensor, button, plus
      the hub/event/schedule variants).
- [ ] **Add a drift-detector contract test** that asserts
      `assets/schemas/enums.json` enumerates exactly the Go constants in
      `pkg/hmenum/datapoint.go` (both `DataPointCategory` and
      `DataPointType`) — neither side may add, drop, or rename a value
      without the other. This is the standing guard that keeps the wire
      contract honest; model it on the existing parity guards under
      `tests/contract/`.
- [x] **Document the enum as part of the external-client contract** — a
      change to a category value is a breaking wire change; called out
      in `docs/external-clients/asks.md` (§C1). ADR 0020 is immutable
      once landed, so the breaking-change axis lives in the mutable
      contract docs, not in the ADR body.

## P2 — Nested snapshot to kill the N×M bootstrap

Bootstrap is currently N×M REST calls: `/snapshot`
(`internal/north/rest/handlers/snapshot.go`) returns only flat
`DeviceSummary` entries (`channels_count` but no nested channels);
channels and data-points are fetched per device / per channel. The
NDJSON variant already exists (`application/x-ndjson`, the H1 ask) and
emits one `{"kind": …, "data": …}` line per entity
(`writeSnapshotNDJSON`, `snapshot.go:128`) — currently the kinds
`meta` / `interface` / `device` / `room` / `function` / `program` /
`sysvar`. It does **not** yet emit channels or data-points.

- [ ] **Extend `/snapshot`** to optionally include channels +
      data-points. Two non-exclusive surfaces:
  - NDJSON: add `kind: "channel"` and `kind: "data_point"` lines to the
    existing `emit()` loop (`snapshot.go:140-157`). This is the
    low-risk path — it slots straight into the established
    `{kind, data}` stream shape.
  - JSON: add an opt-in `?include=channels,data_points` query parameter
    that nests the same entities into the `DeviceSummary` envelope for
    clients that prefer a single document over a stream.
- [ ] **Confirm the client can consume it in one pass** — it already
      has the hook in `bootstrap()` and the `on_replay_lost`
      re-bootstrap path is built for it.
- [ ] **Benchmark** cold-start call count / wall-time before & after on
      a large CCU (target: a single stream instead of N×M).

## P3 — Close the two deferred broadcasts (when HA needs them)

Both events already fire on the **internal** event bus but are not
re-published over any north-bound adapter (WS / REST / MQTT). Defer
until HA actually consumes them.

- [ ] **Broadcast `OptimisticRollback`.** The internal
      `DataPointOptimisticRolledBackEvent` exists
      (`pkg/hmevent/catalogue.go:239`) and is published on the internal
      bus (`internal/central/adapter/device_pipeline.go`), but no
      north-bound adapter forwards it; the client synthesizes it from
      `set_value` failures in the meantime. This is a genuine design
      decision, not just plumbing: decide whether the daemon's
      `lastSentLevel`/TTL model should own and emit the rollback
      semantics, or whether the client-side synthesis stays
      authoritative. Resolve that before wiring the broadcast.
- [ ] **Emit device trigger / keypress events.** Config exists
      (`internal/configui/link_param_metadata.go`, SHORT_/LONG_ groups)
      and `hmevent.DeviceTriggerEvent` is published internally
      (`internal/central/coordinators/event.go:218`), but no north-bound
      adapter broadcasts it. `homematicip_local` consumes
      `get_event_groups(event_type=…)` + `DeviceTriggerEvent` for real —
      the client's `DeviceTriggerEvent` compat class is ready to bind
      the moment the daemon ships the broadcast.

---

## Out of scope (daemon already satisfies it)

Per `docs/external-clients/asks.md`, the wire contract is otherwise
complete: WS resume cursor (ADR-0022), capability handshake on `/info`,
problem+json type URIs, bulk value read, atomic paramset put,
auth-token provisioning, sysvar/program broadcasts, structured
diagnostics, mDNS discovery. These need no further work for the
drop-in.
