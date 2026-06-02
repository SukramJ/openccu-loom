# ADR 0024 — Daemon-instance vs CCU identity, and the interface_id triple

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
  Appears **only** in the `interface_id` prefix. Operators override it
  for the rare same-hostname-multiple-daemons case.
- **`central_name`** — the connected CCU's name. User-defined per
  connection (`centrals[].name`, required, unique per daemon). It is the
  CCU discriminator: the `interface_id` middle component, the callback
  path token, and the wire scoping field (`payload.central`, MQTT topic
  segment, REST `?central=`).
- **`interface_id = <instance_name>-<central_name>-<interface>`** —
  satisfies CCU-side client uniqueness (via `instance_name`) **and**
  daemon-internal per-CCU uniqueness (via `central_name`) with no
  translation layer; the `InterfaceID` stays the single internal key.
- **Callback URL = `<host:port>/RPC2/<central_name>`** — the daemon is
  implicit in the callback server's `host:port`, so `instance_name` is
  not repeated in the path.

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
| interface_id | `<instance_name>-<central_name>-<interface>` |

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

- The wire `interface_id` format changes
  (`<central_name>-<iface>` → `<instance_name>-<central_name>-<iface>`).
  On upgrade the daemon re-registers callbacks (deinit old, init new) and
  the `values_cache` rows (keyed by `(central_name, interface_id, …)`)
  are wiped and refetched — they are a cache (`ValuesCacheSchemaVersion`
  bump), not a hard data migration.
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
- The Svelte config UI may surface `central_name` as a first-class facet
  (CCU filter/selector on the device + hub views) consuming the existing
  `?central=` scoping — an open follow-up.
- Supersedes the `central_name`-as-daemon-discriminator rationale that
  lived in `interface_id.go`.
