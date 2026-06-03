# ADR 0024 — Daemon-instance vs CCU identity, and the two interface ids

- **Status**: accepted
- **Date**: 2026-06-02
- **Refines**: [0002](./0002-multi-ccu-first-class.md) (multi-CCU),
  [0020](./0020-external-client-wire-contract.md) (wire contract)

## Context

`central_name` (the `CentralRow.name` config label) was overloaded with
three conceptually distinct roles:

1. **Daemon identity towards a CCU** — the prefix of the XML-RPC `init()`
   `interface_id`, whose stated purpose (see the former
   `internal/central/adapter/interface_id.go` comment) is to keep the
   identifier unique on the CCU so two *daemons* against the same CCU do
   not overwrite each other's callback registration.
2. **CCU discriminator within the daemon** — the callback URL path
   (`/RPC2/<central_name>`) and the wire scoping field
   (`payload.central`, MQTT topic segment, REST `?central=`).
3. (Historically also conflated with) HA entity identity — now fully
   decoupled: HA `unique_id` is serial-based and loom-namespaced (P5,
   `internal/routingkey`), independent of this identifier.

Roles 1 and 2 pull in **opposite directions** and only coincided because
one `central.Unit` = one CCU and `central_name` was per-CCU:

- Role 1 needs the prefix to distinguish **daemons** (the same CCU,
  different clients).
- Role 2 needs it to distinguish **CCUs** (the same daemon, different
  CCUs) — because `DataPointKey` is
  `(InterfaceID, ChannelAddress, ParamsetKey, Parameter)` with **no
  separate central field**, so CCU scoping rides entirely on the
  `InterfaceID`. Addresses that repeat across CCUs (`BidCoS-RF:1`,
  `INT000*`, VCU virtual-remote channels) would collide internally
  without a per-CCU component in the `InterfaceID`.

Putting the CCU's own name in the `interface_id` satisfies role 2 but
**fails role 1**: two daemons derive the same CCU name from the same CCU,
produce the same `interface_id`, and collide on the CCU — exactly the
case the prefix was meant to prevent.

## Decision

Separate the two identities and carry both in the wire `interface_id`,
and keep the existing **`central`** naming throughout — a CCU *is* a
Zentrale/central, one `central.Unit` represents exactly one CCU, and the
term is aiohomematic-aligned. (An exploratory `central → ccu` rename was
considered and rolled back; see below.)

- **`instance_name`** — the daemon's identity. Reuses the existing
  `config.InstanceName` (default: OS hostname, `.local` stripped).
  Appears **only** in `InitInterfaceID` (the CCU-facing init id), never
  in the canonical `InterfaceID`. Operators override it for the rare
  same-hostname-multiple-daemons case.
- **`central_name`** — the connected CCU's name. User-defined per
  connection (`centrals[].name`, required, unique per daemon). The CCU
  discriminator: the callback path token and the wire scoping field
  (`payload.central`, MQTT topic segment, REST `?central=`).
- **Two interface identifiers — the hostname touches only the CCU wire:**
  - **`InterfaceID = <central_name>-<interface>`** — the canonical id
    used **everywhere**: `DataPointKey`, the value-writer key, the
    `Clients` registry, MQTT topics, REST/WS payloads, the SPA.
    Host-independent; `central_name` already gives daemon-internal
    per-CCU uniqueness (`DataPointKey` has no separate central field).
  - **`InitInterfaceID = <instance_name>-<central_name>-<interface>`** —
    derived **only** for the CCU `init()`/`deinit()` and the BIN-RPC
    callback registration, where the id must be unique per daemon on the
    CCU (two daemons against one CCU). The CCU echoes it in callbacks;
    the callback handler strips `<instance_name>-` back to `InterfaceID`
    (`StripInstance`). The hostname therefore never leaks into topics,
    internal keys, or the external `interface_id` field.
- **Callback URL = `<host:port>/RPC2/<central_name>`** — the daemon is
  implicit in the callback server's `host:port`.

The normative scoping equality from P4 holds:
`central_name == SystemCCUEntry.name == payload.central`.

### Naming convention (binding)

One word — **`central`** — for the per-CCU concept across every artefact;
**`instance`** for the daemon; **CCU** only in prose / UI for the
hardware. This homogeneity is the rule a future change must preserve.

| Artefact | Name |
|---|---|
| Go package | `internal/central` |
| Runtime type (one per CCU) | `central.Unit` |
| Constructor | `central.New() (*Unit, error)` |
| Registry | `central.Registry` |
| Config type / section / name field | `config.CentralConfig` / `centrals:` / `centrals[].name` |
| Scoping field — one-CCU context | `Name` |
| Scoping field — cross-CCU tag | `Central` (`json:"central"`) |
| Wire field / REST query / log key | `central` / `?central=` / `central` |
| SQL column | `central_name` |
| Local var / param | object → `u` / `unit`; name string → `centralName` |
| Payload self-DTOs | `payload.CentralInfo` / `CentralConfig` / `CentralState` |
| Daemon identity | `instance_name` / `InstanceName` |
| InterfaceID (canonical, everywhere) | `<central_name>-<interface>` |
| InitInterfaceID (CCU init/deinit + BIN-RPC register only) | `<instance_name>-<central_name>-<interface>` |

**Variable-naming rule (Go-idiomatic, binding).** `central` is the
*package*; reach its API as `central.Unit`, `central.New`,
`central.Registry`, `central.Config`. The two layers never collide in a
name:

- A variable/receiver of type `*central.Unit` is the **object** → name it
  `u` (receiver and short scopes) or `unit` (clarity); a slice is `units`.
  **Never** name a `*central.Unit` variable `central` — it would shadow
  the package and make `central.X` unreachable.
- The CCU's **name** (a string — *which* CCU) is `centralName` (var /
  param), the exported `Central` field (`json:"central"`) when tagging a
  cross-CCU payload, and `central_name` on the wire / config / SQL.
- Rule of thumb: **object → `u`/`unit`, identity/scope → `centralName`**.
  `central.Registry` keeps receiver `r`; `*Unit` methods use receiver `u`.

## Consequences

- The **internal** `InterfaceID` stays two-part `<central_name>-<interface>`
  (the pre-ADR format), so `DataPointKey`s, the value-writer key, MQTT
  topics, REST/WS payloads and the `values_cache` are all unchanged and
  host-independent (`ValuesCacheSchemaVersion` stays `1`). Only the
  CCU-facing `InitInterfaceID` gains the `instance_name` prefix: on
  upgrade the CCU re-registers the callback under the new init id (deinit
  old, init new); nothing on disk migrates. An earlier attempt that put
  the triple into the unified internal id leaked the hostname into MQTT
  topics + the external `interface_id` (it was even unpredictable to
  clients/tests) and was reverted in favour of this two-id split.
- The runtime type was shortened `CentralUnit` → `central.Unit` (the
  package qualifier already carries "central"), and the CCU-self payload
  DTOs are `payload.Central{Info,Config,State}`. The `central` package,
  `central.Registry` and `config.CentralConfig` keep their names; the
  `central_name` SQL columns are unchanged.
- **Rejected alternative — `central → ccu` rename.** Renaming everything
  to `ccu` (package, wire field, config key, SQL) was prototyped as a
  clean break from aiohomematic, then rolled back: `central` is the
  correct domain term and stays aligned with the reference. The wire and
  config surfaces therefore remain `central` / `centrals[].name`.
- HA entity identity is unaffected (serial-based `loom_` scheme, P5).
- The Svelte config UI surfaces the central as a first-class facet: a
  per-view CCU selector + filter (DeviceList, Sysvar/Program/Message/Inbox/
  Firmware/Audit lists, UnIgnore) and the owning CCU shown per row
  (`DeviceCard` `· <central>`, message badges). It filters client-side on
  the `central` field of the loaded items (right for the embedded UI's
  scale); the server-side `?central=` scoping (P2) stays available for
  external clients. svelte-check clean.
- Supersedes the `central_name`-as-daemon-discriminator rationale that
  lived in `interface_id.go`.
