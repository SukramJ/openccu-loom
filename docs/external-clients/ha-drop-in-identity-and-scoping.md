# HA drop-in: central identity & scoping — open problems

**Status:** Problem statement / decision request
**Audience:** openccu-loom daemon maintainers
**Related:** [`topic-hierarchy.md`](./topic-hierarchy.md), [`asks.md`](./asks.md),
[ADR-0002 (multi-CCU first-class)](../adr/0002-multi-ccu-first-class.md)

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
| daemon `centralID` in `naming.go` | daemon | (the central's name) | prefix in the daemon's *own* unique-ID generator for MQTT / REST definition export |

Confirmed in code:
- HA prefix: `homematicip_local/.../control_unit.py` — `central_id = self.entry_id[-10:]` (CCU path); the **loom path does not pass `central_id` at all**.
- `payload.central` ← `CentralName`: `internal/model/generic/payload.go:56` (`Central: d.CentralName`).
- `central_name` is the `CentralRow.name` ("Daemon-local identifier; must be unique", `openapi.yaml` `CentralRow`).

**Key consequence:** the `unique_id` prefix HA needs is the HA
`entry_id[-10:]`, which the daemon *cannot* know and must not try to
supply. The daemon's `central` is for **scoping**, never for HA key
identity. The client injects the HA prefix; the daemon supplies the
scoping discriminator. These must stay separate.

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

**Decision needed (daemon side):** pick one —
1. add the scoped device tree ADR-0002 described (`/centrals/{name}/devices/...`,
   `/centrals/{name}/snapshot`), or
2. add a `central` query parameter / header to the existing unscoped
   endpoints, or
3. explicitly contract external clients as **one daemon = one central**
   for the device surface, and document multi-CCU as MQTT/UI-only.

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
[`by_design.md` → BD-Identity-RoutingKeyNamespaces](../parity/by_design.md):
the MQTT-discovery `unique_id` stays daemon-namespaced and pinned (changing
it would orphan existing MQTT entities); the WS/REST `unique_id` field is
opaque daemon scoping; and the cross-backend HA routing key is now mirrored
on the Go side in `internal/routingkey` (`GenerateUniqueID`,
`GenerateChannelUniqueID`, `HubSlug`), locked bit-for-bit against the shared
golden fixtures by a contract test under `tests/contract/`. The misleading
"HA-compatible" wording on `internal/model/device/naming.go` has been
removed; that generator is legacy/unused and new consumers use
`internal/routingkey`. The HA drop-in client rebuilds the HA registry
`unique_id` itself from `address` + `parameter` (+ its `entry_id[-10:]`
prefix), which the WS/REST payloads already expose (raw `address`,
`parameter`, `category`, and hub `name` = legacy name).

---

## Summary: who owns what

| Concern | Owner | Action |
|---|---|---|
| `unique_id` prefix = HA `entry_id[-10:]` | **client / homematicip_local** | inject `central_id` into `LoomCentralConfig` (loom path currently omits it) |
| key algorithm bit-identical to aiohomematic | **shared contract** | client adopts `aiohomematic_contract`; daemon mirrors it in `internal/routingkey` + golden-fixture test; id namespaces documented as by-design — **P5 (resolved)** |
| select *which* CCU per HA entry | **daemon contract** | serial→central_name resolution; equality normative in `openapi.yaml` — **P4 (resolved)** |
| fetch one CCU's devices over REST | **daemon** | scoped routes or `central` param — **P2** |
| receive one CCU's device events | **daemon contract + client** | filter by payload `central` (document it) — **P3** |
| token = which CCU | **n/a** | token is daemon-wide; not a scoping axis — **P1** |

The cleanest near-term contract is **one `homematicip_local` entry = one
central**, the model `topic-hierarchy.md` already calls the status quo.
Making that explicit (P2 option 3 + P4 guarantee) unblocks the drop-in;
full multi-CCU-per-entry (P2 options 1/2) can follow.
