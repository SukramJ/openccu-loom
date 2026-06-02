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
one `CentralUnit` = one CCU and `central_name` was per-CCU:

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

Separate the two identities and carry both in the wire `interface_id`.

- **`instance_name`** — the daemon's identity. Reuses the existing
  `config.InstanceName` (default: OS hostname, `.local` stripped).
  Appears **only** in the `interface_id` prefix. Operators override it
  for the rare same-hostname-multiple-daemons case.
- **`ccu_name`** — the connected CCU's name (renamed from
  `central_name`). **User-defined per connection** (required, unique per
  daemon) — the operator owns the value, exactly as `central_name` is
  chosen today. The UI may *suggest* a default seeded from the CCU's own
  name/IP slug, but the stored value is operator-controlled and stable
  thereafter (CCU name and IP can change; the scoping key must not). It
  is the CCU discriminator: the `interface_id` middle component, the
  callback path token, and the wire scoping field.
- **`interface_id = <instance_name>-<ccu_name>-<interface>`** — satisfies
  CCU-side client uniqueness (via `instance_name`) **and** daemon-internal
  per-CCU uniqueness (via `ccu_name`) with no translation layer; the
  `InterfaceID` stays the single internal key.
- **Callback URL = `<host:port>/RPC2/<ccu_name>`** — the daemon is
  implicit in the callback server's `host:port`, so `instance_name` is
  not repeated in the path.

The normative scoping equality from P4 carries over with the rename:
`ccu_name == SystemCCUEntry.ccu_name == payload.ccu`.

## Consequences

- The wire `interface_id` format changes
  (`<ccu_name>-<iface>` → `<instance_name>-<ccu_name>-<iface>`). On
  upgrade the daemon re-registers callbacks (deinit old, init new) and
  the `values_cache` rows (keyed by `(ccu_name, interface_id, …)`) are
  wiped and refetched — they are a cache, handled like the migration-003
  schema-version wipe, not a hard data migration.
- A repo-wide rename `central` → `ccu` follows and **is intentionally
  breaking** (accepted pre-1.0, greenfield, no compatibility shim): the
  config key, internal `CentralName`, the wire field `central` → `ccu`,
  the MQTT topic segment, REST `?central=` → `?ccu=`, and the OpenAPI
  schemas all move together. Sequenced so each phase builds and tests
  green. ADR-0020's wire contract is updated accordingly.
- HA entity identity is unaffected (serial-based `loom_` scheme, P5).
- The Svelte config UI surfaces `ccu_name` as a first-class facet:
  a CCU filter/selector on the device (and hub-entity) views, and the
  CCU shown per row, so a multi-CCU daemon is navigable per CCU. This
  consumes the same `?ccu=` scoping the REST/WS surfaces expose.
- Supersedes the `central_name`-as-daemon-discriminator rationale that
  lived in `interface_id.go`.
