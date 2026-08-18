# HA drop-in: central identity & scoping — daemon side resolved

!!! info "Who this page is for"
    Integrators building the Home Assistant drop-in client and daemon
    maintainers who own the identity contract. Operators do not need this
    page. Background: [Multi-CCU](../user/multi-ccu.md).

**Status:** Daemon side resolved — every daemon-side decision (P1–P6) is
taken (see the per-section *Resolution* notes below and
[ADR-0024](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0024-instance-and-ccu-identity.md)). The optional
daemon enhancement **P6** (carry `unique_id` on the value-bearing push
payloads) has landed. One follow-up remains and it is **client-side**:
the one-time `unique_id` registry migration in the `homematicip_local` /
`py-openccu-loom-client` repos (see Summary).
**Audience:** openccu-loom daemon maintainers
**Related:** [`topic-hierarchy.md`](./topic-hierarchy.md),
[ADR-0002 (multi-CCU first-class)](https://github.com/SukramJ/openccu-loom/blob/main/docs/adr/0002-multi-ccu-first-class.md)

## Context

`py-openccu-loom-client` is the drop-in replacement for `aiohomematic`
inside the Home Assistant `homematicip_local` component. The component
already wires it as an alternate backend (`BACKEND_LOOM` →
`LoomCentralConfig` → `LoomCentralAdapter`).

The hard constraint for a drop-in is **entity-identity stability**: HA
stores each entity under a `unique_id` in its registry. If the loom
backend produces different `unique_id`s than aiohomematic did, every
entity loses its history, customisations and area assignment on cutover.

While reconciling the client we hit a cluster of problems that are
**the daemon's to decide**, not the client's. This doc states them.

## The identifiers in play

There are (at least) **four** distinct "central" identifiers. Conflating
any two breaks either identity or scoping.

| Identifier | Owner | Value example | Role |
|---|---|---|---|
| `entry_id[-10:]` | Home Assistant config entry | `a1b2c3d4e5` | aiohomematic's `CentralConfig.central_id` → **the `unique_id` prefix already in HA's registry** |
| CCU `serial` | the CCU | `3014F711A0001234` | the HA config-entry's HA-`unique_id`; the stable real-world CCU identity |
| `central_name` | daemon config (`CentralRow.name`) | `home` | the daemon's per-CCU scoping discriminator; equals `payload.central` and `SystemCCUEntry.name` |
| daemon HA routing key in `internal/routingkey` | daemon | `loom_<serial[-10:]>_…` | the daemon's own loom-namespaced unique-id (MQTT discovery), mirrored from the shared contract — see P5 (this superseded the deleted `naming.go` generator) |

Confirmed in code:
- HA prefix: `homematicip_local/.../control_unit.py` — `central_id = self.entry_id[-10:]` (CCU path); the **loom path does not pass `central_id` at all**.
- `payload.central` ← `CentralName`: `internal/model/generic/payload.go:71` (`Central: d.CentralName`).
- `central_name` is the `CentralRow.name` ("Daemon-local identifier; must be unique", `openapi.yaml` `CentralRow`).

**Key consequence:** the daemon's `central` is for **scoping**, never for
HA key identity — those stay separate. The HA registry `unique_id` is
owned by the client and built from the daemon-supplied raw fields
(`address`, `parameter`, `category`, hub `name`) via the shared routing-key
contract. **Resolved direction:** the drop-in migrates HA `unique_id`s to
the loom/serial scheme (P5, [`ha-unique-id-migration.md`](./ha-unique-id-migration.md)),
whose prefix is the CCU **serial** — which the client knows from its own
config entry (`entry.unique_id == serial`) — so the daemon never supplies
a prefix and the old `entry_id`-based `central_id` injection is unnecessary.

---

## P1 — token/connection does NOT scope to a CCU (clarified, for the record)

Per ADR-0002, **one authentication realm covers all centrals**; users and
tokens live at the daemon top level. So a single authenticated connection
(host + port + token) can see **every** central the daemon manages.
Scoping is therefore **per-request / per-subscription via `central_name`**,
not per-connection.

This is fine as a model, but it means an external client must actively
scope — which surfaces P2 and P3.

## P2 — there is no central-scoped *device* REST surface for external clients

ADR-0002 says REST paths are scoped as `/api/v1/centrals/<name>/devices/...`
with the unscoped `/api/v1/devices/...` redirecting to the single central
"when exactly one is configured".

But the shipped external `openapi.yaml` exposes only:
- unscoped `/devices`, `/devices/{addr}/...`, `/snapshot`, `/devices/values:batch`
  (no `central` path segment, no `central` query parameter), and
- `/centrals` + `/centrals/{name}` — **central-admin only** (`CentralRow`
  CRUD), not a scoped device/snapshot tree.

So today the device/snapshot/value endpoints the client relies on are the
**single-central convenience surface**. On a daemon with ≥2 centrals an
external client (and thus a second `homematicip_local` entry) has **no way
to fetch one CCU's device tree over REST**.

**Resolution (taken):** option 2 — a `central` query parameter on the
collection endpoints. The per-address endpoints (`/devices/{addr}/...`)
already work multi-CCU because device addresses are globally unique and
the handler resolves the owning central per address. The gap was only
*server-side* filtering of the collection endpoints, so:

- `GET /devices?central=<name>` filters the list by exact central name.
- `GET /snapshot?central=<name>` scopes the device tree (devices,
  channels) and the hub entities (programs, sysvars) by central; rooms
  and functions are not central-tagged in the model and stay fleet-wide.
- `POST /devices/values:batch` is already per-address, so it needs no
  central parameter.

`central` is matched exactly against the canonical central name
(`== SystemCCUEntry.name == payload.central`, P4). Both parameters are
documented in `assets/openapi.yaml`. This mirrors the existing
`?central=` filter on the audit endpoint and the WS filter-by-payload
model (P3); the heavier scoped-route tree (option 1) stays available as
a future step if a fully path-scoped surface is ever required.

## P3 — WS device events are only scopable by payload, not by topic

`topic-hierarchy.md` §"Multi-central addressing": `central.{name}.*` and
`hub.{name}.*` embed the central name, but `device.*` /
`device.{addr}.*` topics do **not**. So a multi-central client cannot
subscribe to "only my CCU's device value changes" by topic — it must
subscribe broadly and **filter every `datapoint.value_changed` /
`custom_data_point.state_changed` by the payload `central` field**.

This is internally consistent (every payload carries `central`) and is
arguably acceptable, but it should be stated normatively as the contract
for client authors, and it pairs with P2: REST single-central + WS
filter-by-payload is a workable single-central story but an incomplete
multi-central one.

**Resolution (taken):** documented normatively in
[`topic-hierarchy.md` → "device events scope by payload, not by topic"](./topic-hierarchy.md):
`device.*` topics carry no central segment, so a multi-central client
subscribes broadly and filters every device event by the payload
`central` field; hub/lifecycle/status events may still be scoped by
topic (`hub.{central}.*`, etc.). `central` is the canonical central
name (ADR-0024), resolved from a `serial` via `GET /system/ccu`.

## P4 — `serial → central_name` resolution is not first-class

A `homematicip_local` entry identifies its CCU by **serial** (its HA
`unique_id`). To scope (P2/P3) it needs the **`central_name`**.
`SystemCCUEntry` carries both `serial` and `name`, so a client *can*
resolve serial → name by listing `/system/ccu` — **iff** the daemon
guarantees `SystemCCUEntry.name == CentralRow.name == payload.central`.

**Resolution (taken):** the equality is now a **normative** statement in
`assets/openapi.yaml` on both `SystemCCUEntry` (schema description +
`name` property) and `CentralRow.name`:

    SystemCCUEntry.name == CentralRow.name == payload.central

A client resolves its CCU by `serial` via `GET /system/ccu`, reads the
matching entry's `name`, and scopes all per-central requests /
subscriptions by it — without assuming `instance_name == central_name`.
The `TestOpenAPISpecIsValid` contract test keeps the spec well-formed.

## P5 — a third, *divergent* `unique_id` implementation lives in the daemon

`internal/model/device/naming.go:GenerateUniqueID(centralID, address, parameter, prefix)`
("used by MQTT entity-ID generation and REST definition export to produce
stable HA-compatible IDs") is a **third independent reimplementation** of
the routing-key algorithm, alongside:
- `aiohomematic/model/support.py:generate_unique_id` (XML-RPC backend), and
- `aiohomematic_contract.generate_unique_id` (the shared contract the
  Python client now uses).

Its rules **differ** from the contract:
- it prefixes **every `VCU*` address** (plus `BidCoS-*`, `HmIP-RCV-1`)
  with the `centralID` — the contract/aiohomematic prefix *only*
  hub/internal (`INT000*`)/virtual-remote addresses, **not** normal `VCU`
  devices;
- it special-cases the roots `Sysvar` / `Programs` / `InstallMode`
  (capitalised, plural) vs the contract's `sysvar` / `program` /
  `install_mode` / `hub`.

So a `VCU1234567:1` STATE data point gets `…vcu1234567_1_state` from the
contract but `{central}_vcu1234567_1_state` from `naming.go`. If MQTT-
discovered entities and WS-client entities are ever expected to share a
`unique_id` in the same HA instance, **they will not match**.

**Decision needed:** either
1. make `naming.go` track `aiohomematic-contract` (consume its
   `*_golden.json` fixtures in a Go test so drift fails CI — the contract
   is explicitly designed for exactly this multi-backend case), or
2. document that the MQTT/definition-export ID namespace is **deliberately
   distinct** from the aiohomematic HA routing key, and drop the
   "HA-compatible" wording in the `naming.go` doc comment to avoid the
   implication that they match.

**Resolution (taken):** option 2. The daemon's three id namespaces are
deliberately distinct and are now catalogued in
[`by_design.md` → BD-Identity-RoutingKeyNamespaces](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md):
the MQTT-discovery `unique_id` stays daemon-namespaced and pinned (changing
it would orphan existing MQTT entities); the daemon's internal data-point
identity stays opaque to clients; and the cross-backend HA routing key is now mirrored
on the Go side in `internal/routingkey` (`GenerateUniqueID`,
`GenerateChannelUniqueID`, `HubSlug`), locked bit-for-bit against the shared
golden fixtures by a contract test under `tests/contract/`. The legacy
`internal/model/device/naming.go:GenerateUniqueID` generator (which carried
the misleading "HA-compatible" wording) has since been **deleted**;
consumers use `internal/routingkey`. The HA drop-in client rebuilds the HA
registry `unique_id` itself via the shared contract from the raw fields the
WS/REST payloads expose (`device_address` + `channel`, `parameter`,
`category`, hub `name` = legacy name), using the **CCU serial suffix** in
the central slot and the `loom_` namespace (see
[`ha-unique-id-migration.md`](./ha-unique-id-migration.md)). The legacy
`entry_id[-10:]` prefix is only the *source* side of the one-time registry
migration, not the new key.

**One declared divergence: CUxD.** The scoped address classes are the hub
roots (`sysvar` / `program` / `install_mode`), `INT000*`, the
virtual-remote buses (`BidCoS-*`, `HmIP-RCV-1`) **and `CUX*`**. The last
one is not in the legacy reference key: CUxD serials are `CUX` + a
two-digit device type + a per-CCU running number that conventionally
starts at 1, so two CCUs bridged into one Home Assistant declare
identical CUxD keys and HA keeps only the first. Because the daemon is
multi-CCU-first (ADR-0002) it scopes that class; the reference, which runs
one CCU per config entry, does not. A client rebuilding the key must
apply the same rule — or consume the `unique_id` the push payloads carry
(P6), which already has it. Only the *parameter-level* key is affected;
the channel-level key is unscoped for CUxD on both sides. The divergence
is pinned from both ends by
`tests/contract/testdata/routing_key/cuxd_scoping_golden.json` (Go
contract test + `make routing-key-parity`) and catalogued as
[`by_design.md` → BD-Identity-CUxDCentralScoping](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md),
whose retirement condition is the reference adopting the rule.

**Resolved (P6):** the canonical `unique_id` is now carried on the
value-bearing push payloads (`datapoint.value_changed`,
`custom_data_point.state_changed`, `hub.sysvar_changed`,
`hub.program_executed`, `datapoint.optimistic_rolled_back`,
`device.trigger`) as an optional field, so the client consumes it
directly rather than re-implementing the contract — and can verify its
own rebuild against it. It is built by `internal/routingkey` at the
publish boundary (which holds the central → serial mapping) and omitted
when the serial / program name is not yet known, keeping the field
backward-compatible. See
[`ha-unique-id-migration.md` → "Recommended daemon change"](./ha-unique-id-migration.md).

---

## Summary: who owns what

| Concern | Owner | Action |
|---|---|---|
| HA `unique_id` for the drop-in | **client / homematicip_local** | run the one-time unique_id migration to the loom/serial scheme ([`ha-unique-id-migration.md`](./ha-unique-id-migration.md)); the old `entry_id`-based `central_id` injection is **obsolete** — **(open, client repo)** |
| key algorithm bit-identical to aiohomematic | **shared contract** | client adopts `aiohomematic_contract`; daemon mirrors it in `internal/routingkey` + golden-fixture test; id namespaces documented as by-design — **P5 (resolved)** |
| canonical `unique_id` on push payloads | **daemon** | optional `unique_id` now carried on the value-bearing payloads so clients consume + verify (rebuild stays a fallback when absent) — **P6 (resolved)** |
| select *which* CCU per HA entry | **daemon contract** | serial→central_name resolution; equality normative in `openapi.yaml` — **P4 (resolved)** |
| fetch one CCU's devices over REST | **daemon** | `central` query param on `/devices` + `/snapshot` (per-address already multi-CCU) — **P2 (resolved)** |
| receive one CCU's device events | **daemon contract + client** | filter by payload `central`; normative in `topic-hierarchy.md` — **P3 (resolved)** |
| token = which CCU | **n/a** | token is daemon-wide; not a scoping axis — **P1** |

The cleanest near-term contract is **one `homematicip_local` entry = one
central**, the status quo `topic-hierarchy.md` already documents. P2 was
resolved with the `?central=` query parameter (option 2) and P4 with the
normative equality; together with the loom/serial unique_id migration (P5)
the daemon side is fully unblocked. Full multi-CCU-per-entry can follow.

The daemon-side enhancement **P6** has landed: the canonical `unique_id`
is now carried on the value-bearing payloads, so clients consume it
directly and verify their own rebuild against it. It is optional and
backward-compatible (omitted when unresolved; clients fall back to
rebuild), so it does not change the drop-in contract — see
[`ha-unique-id-migration.md` → "Recommended daemon change"](./ha-unique-id-migration.md).
The only remaining work is **client-side**: the one-time HA registry
`unique_id` migration in the `homematicip_local` / `py-openccu-loom-client`
repos.
