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

- [x] **Add `category` + `data_point_type` to `DataPointSummary`** —
      `internal/north/rest/handlers/devices.go:145`. Source `category`
      from the DP's internal `Category()`; derive `data_point_type` via
      `hmenum.CategoryToType[category]` (`pkg/hmenum/datapoint.go:76`).
      Mirror the JSON-tag style of the existing `CustomDPSummary.category`
      field. Update `assets/openapi.yaml` (the `DataPointSummary`
      schema) in the same change.
- [x] **Add `category` + `data_point_type` to
      `DataPointValueChangedPayload`** —
      `internal/north/rest/ws/payloads.go`. A value-changed event
      should be classifiable without a prior catalogue lookup, so a
      client that reconnects mid-stream can route it immediately.

      *Trade-off — opt-in (implemented).* Category and functional type
      are quasi-static DP properties; stamping them onto every
      value-changed event is redundant wire weight on the highest-frequency
      message on a busy CCU. The fields are gated behind a `classify`
      flag on the WS subscribe frame; the per-client write pump strips
      them otherwise. Default off.
- [ ] **Regenerate the schemas into `openccu-loom-types`** and bump the
      version pin the client depends on (currently `>=0.1.3,<0.2`).
      *Out of this repo* — `openccu-loom-types` is a sister repo
      (`SukramJ/openccu-loom-types-py`, see asks.md §C3). On the daemon
      side the source artefact `assets/schemas/enums.json` is regenerated
      (`make export-schemas`) and pinned by the drift-detector test below.
- [x] **Add a contract test** asserting every generic DP carries a
      non-empty `category` and a `data_point_type` that matches
      `hmenum.CategoryToType` — `internal/north/rest/handlers`
      (`toDataPointSummary` tests) plus the enum drift-detector below.

*Impact:* removes the client's need to port aiohomematic's
categorization model — the `NotImplementedError` entity-spawn block
becomes a declarative read.

## P1 — Stable, versioned enum catalogue

`homematicip_local` imports `aiohomematic.const.DataPointCategory` and
filters on it directly, so the daemon's enum must map 1:1 and stay
stable. Once `DataPointCategory` / `DataPointType` are on the wire,
**any change to a category value is a breaking wire change.**

- [x] **Verify `DataPointCategory` / `DataPointType` parity** between
      `assets/schemas/enums.json` and the values HA filters on. Done by
      regenerating `enums.json` (`make export-schemas`), which cleared
      pre-existing drift in unrelated enums; the two target enums were
      already in parity.
- [x] **Add a drift-detector contract test** —
      `tests/contract/enum_catalogue_parity_test.go`
      (`TestEnumCatalogueMatchesGoConstants`) AST-walks
      `pkg/hmenum/datapoint.go` and asserts `assets/schemas/enums.json`
      enumerates exactly the Go constants for both `DataPointCategory`
      and `DataPointType` — neither side may add, drop, or rename a value
      without the other.
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

- [x] **Extend `/snapshot`** to optionally include channels +
      data-points. Both surfaces implemented (`snapshot.go`):
  - NDJSON: `kind: "channel"` (stamped with `device_address`) and
    `kind: "data_point"` (stamped with `channel_address`) lines join the
    existing `emit()` loop.
  - JSON: opt-in `?include=channels[,data_points]` (alias `data-points`;
    `data_points` implies `channels`) nests the entities under a new
    `device_channels` field, parallel to the unchanged flat `devices`
    list. Anonymisation tokenises nested channel names.
- [ ] **Confirm the client can consume it in one pass** — *out of this
      repo* (client-side `bootstrap()` / `on_replay_lost`).
- [x] **Benchmark** the server-side build cost of each snapshot shape —
      `tests/bench/snapshot_bench_test.go` (`BenchmarkSnapshotFlat` /
      `…IncludeChannels` / `…IncludeDataPoints` / `…NDJSONDataPoints`) on
      a 200×6×8 fleet. The full nested call (~9.6 k DPs) builds in one
      pass where the legacy flow issued 1 + N + N×C requests. Round-trip
      elimination is the operational win; the bench guards against the
      one-shot build becoming pathologically expensive.

## P3 — Close the two deferred broadcasts

Both events fired only on the **internal** event bus; they are now
re-published over the WebSocket adapter.

- [x] **Broadcast `OptimisticRollback`.** Resolved in favour of the
      daemon owning the rollback semantics: `OptimisticRollbackSubscriber`
      (`internal/north/rest/ws/optimistic_rollback.go`) forwards
      `DataPointOptimisticRolledBackEvent` to WS subscribers as
      `datapoint.optimistic_rolled_back`, riding the same per-DP topic as
      `datapoint.value_changed` (subscribers route by envelope `type`).
      The client can retire its `set_value`-failure synthesis.
- [x] **Emit device trigger / keypress events.** `DeviceTriggerSubscriber`
      (`internal/north/rest/ws/device_trigger.go`) forwards
      `hmevent.DeviceTriggerEvent` to WS subscribers as `device.trigger`
      on `device.<addr>.channels.<no>.trigger`. The client's
      `DeviceTriggerEvent` compat class can now bind.

---

## Out of scope (daemon already satisfies it)

Per `docs/external-clients/asks.md`, the wire contract is otherwise
complete: WS resume cursor (ADR-0022), capability handshake on `/info`,
problem+json type URIs, bulk value read, atomic paramset put,
auth-token provisioning, sysvar/program broadcasts, structured
diagnostics, mDNS discovery. These need no further work for the
drop-in.
