# HA `unique_id` migration — legacy → loom canonical schema

**Status:** Ready to implement (client-side). The daemon-side
prerequisite has landed — the value-bearing push payloads now carry the
optional canonical `unique_id` (P6, see
[`ha-drop-in-identity-and-scoping.md`](./ha-drop-in-identity-and-scoping.md)),
so the client can both consume the key and verify its own rebuild. This
document specifies the one-time HA registry migration the client runs.
**Audience:** `homematicip_local` / `py-openccu-loom-client` authors.
**Related:** [`ha-drop-in-identity-and-scoping.md`](./ha-drop-in-identity-and-scoping.md),
[`by_design.md` → BD-Identity-RoutingKeyNamespaces](../parity/by_design.md).

## Why a migration

Home Assistant stores each entity under a `unique_id` in its entity
registry; that key carries the entity's history, customisations, area
assignment and `entity_id`. The loom canonical schema differs from the
legacy `aiohomematic` routing key (it adds a constant `loom_` namespace
and, for hub-level entities, swaps the HA `entry_id[-10:]` prefix for the
CCU serial). A naive cutover would orphan every entity.

HA supports rewriting registry keys **without data loss** via
`homeassistant.helpers.entity_registry.async_migrate_entries`. The
integration runs a one-time, deterministic `old → new` rewrite on setup;
history and customisations follow the key. This document specifies that
mapping so it stays identical across client versions.

## The canonical schema (target)

Every loom `unique_id` is:

```
loom_<routing-key>
```

where `<routing-key>` is the shared cross-backend routing key (mirrored
on the daemon side in `internal/routingkey`, locked by a golden-fixture
contract test). The leading `central_id` slot of the routing key is:

- **empty** for normal devices — the device serial (e.g. `VCU1234567`) is
  already globally unique, so no prefix is needed;
- the **CCU serial, last 10 characters, lower-cased** for the address
  classes whose addresses repeat across CCUs: hub roots
  (`sysvar` / `program` / `install_mode`), internal addresses
  (`INT000*`), and virtual-remote channels (`BidCoS-*`, `HmIP-RCV-1`,
  `VCU` virtual-remote range).

Hub data-point names are slugged with the shared `hub_slug` rule
(python-slugify defaults: Unicode transliteration, dash separator,
lower-cased) **before** they enter the routing key.

### Examples

CCU serial `3014F711A0001234` → serial suffix `11a0001234`.

| Entity | Legacy HA `unique_id` | New loom `unique_id` |
|---|---|---|
| Device `VCU1234567:1` `STATE` | `vcu1234567_1_state` | `loom_vcu1234567_1_state` |
| Button event `VCU1234567:1` `PRESS_SHORT` | `event_vcu1234567_1_press_short` | `loom_event_vcu1234567_1_press_short` |
| Sysvar `Außen Temperatur` | `a1b2c3d4e5_sysvar_aussen-temperatur` | `loom_11a0001234_sysvar_aussen-temperatur` |
| Program `My Prog` | `a1b2c3d4e5_program_my-prog` | `loom_11a0001234_program_my-prog` |
| Internal `INT0001234:1` `LEVEL` | `a1b2c3d4e5_int0001234_1_level` | `loom_11a0001234_int0001234_1_level` |
| Virtual remote `BidCoS-RF:1` `PRESS_SHORT` | `a1b2c3d4e5_bidcos_rf_1_press_short` | `loom_11a0001234_bidcos_rf_1_press_short` |

`a1b2c3d4e5` above is the legacy `entry_id[-10:]`; `11a0001234` is the new
`serial[-10:]`.

## The mapping

Two inputs the client already has per config entry:

- `entry_suffix = entry.entry_id[-10:]` — the legacy hub prefix.
- `serial_suffix = serial[-10:].lower()` — the CCU serial, last 10
  characters. The serial is the config entry's own HA `unique_id`
  (the entry identifies its CCU by serial); it is also available from the
  daemon via `GET /system/ccu` (`SystemCCUEntry.serial`).

The rewrite is purely string-level, so it does not depend on the device
tree being loaded:

```python
def migrate_unique_id(old: str, *, entry_suffix: str, serial_suffix: str) -> str | None:
    # Idempotent: already migrated.
    if old.startswith("loom_"):
        return None
    # Hub / internal / virtual-remote entities carried the entry_id[-10:]
    # prefix; swap it for the CCU serial suffix.
    prefix = f"{entry_suffix}_"
    if old.startswith(prefix):
        return f"loom_{serial_suffix}_{old[len(prefix):]}"
    # Everything else (devices, button events) had no central prefix.
    return f"loom_{old}"
```

Wired into HA setup:

```python
from homeassistant.helpers import entity_registry as er

async def _async_migrate_unique_ids(hass, entry):
    entry_suffix = entry.entry_id[-10:]
    serial_suffix = entry.unique_id[-10:].lower()  # entry.unique_id == CCU serial

    @callback
    def _migrator(reg_entry: er.RegistryEntry) -> dict | None:
        new = migrate_unique_id(
            reg_entry.unique_id,
            entry_suffix=entry_suffix,
            serial_suffix=serial_suffix,
        )
        if new is None or new == reg_entry.unique_id:
            return None
        return {"new_unique_id": new}

    await er.async_migrate_entries(hass, entry.entry_id, _migrator)
```

Run it once, early in `async_setup_entry`, **before** any entity is added
for that entry, so newly-created entities already see the migrated keys.

## Rules & edge cases

- **Idempotency.** A second run is a no-op: keys already starting with
  `loom_` are skipped. Safe to call on every setup.
- **Per-entry scope.** `async_migrate_entries` is scoped to one
  `entry_id`, so only that CCU's entities are touched. Multi-CCU setups
  migrate each entry with its own `serial_suffix`.
- **Target collisions.** If two legacy keys map to the same new key
  (e.g. two sysvars whose names slug identically — already a property of
  the shared `hub_slug` rule), HA's registry refuses the second rename
  because the target `unique_id` already exists; the entity keeps its old
  key. Log these; they indicate a pre-existing slug collision on the CCU
  side, not a migration bug.
- **Forced-sensor suffix.** Daemon-side data points that carry the
  `_sensor` disambiguation suffix already include it in the routing key,
  so it survives the `loom_` prepend unchanged — no special handling.
- **Serial availability.** The migration uses `entry.unique_id` (the
  serial) and never needs a live CCU connection, so it is safe to run
  before the first connect. (The daemon, by contrast, only learns the
  serial post-connect — but it builds hub keys only for entities that are
  themselves discovered post-connect, so the two sides stay consistent.)
- **Verification.** The daemon now carries the `unique_id` field on its
  value-bearing payloads (see *Recommended daemon change* below, now
  landed), so a device entity's migrated `unique_id` should equal that
  field verbatim (devices carry no central prefix, so daemon key == HA
  key) — a cheap built-in drift check. The client still owns the HA value
  and keeps the rebuild path as a fallback for payloads that omit the
  field (e.g. before the CCU serial is known).

## Relationship to the daemon schema

The daemon runs the same routing-key contract on the Go side
(`internal/routingkey`) and uses the canonical `loom_` key for **MQTT
discovery**. The **value-bearing WS payloads now carry the canonical
`unique_id`** as an optional field (P6) — built from the same raw inputs
(`device_address` + `channel` → address, `parameter`, and the hub `name`)
at the publish boundary, which holds the central → serial mapping. The
client consumes `payload.unique_id` when present and **rebuilds** the key
from the raw fields when it is absent. Because both sides run the same
contract, the rebuilt key is bit-identical to the daemon's.

The namespace split (why three id producers exist and which is the HA key)
is catalogued in
[`by_design.md` → BD-Identity-RoutingKeyNamespaces](../parity/by_design.md).

### Recommended daemon change: carry `unique_id` on the value-bearing payloads (landed, P6)

Rebuilding works, but it re-implements the contract on every consumer and
leaves a silent-drift risk: a client that rebuilds slightly wrong routes
to the wrong entity (or none), and nothing catches it. Carrying the
canonical key on the payload removes that — the client consumes it
directly and, at most, *verifies* its own rebuild against it.

**Done:** an **optional** `unique_id` field (the canonical
`loom_<routing-key>`) is now carried on the per-entity push payloads:

- `DataPointValueChangedPayload`
- `CustomDataPointStateChangedPayload`
- `SysvarChangedPayload`, `ProgramExecutedPayload` — hub keys; these use
  the post-connect serial suffix the daemon already has (it builds them
  only for post-connect entities, so this stays consistent — see
  *Serial availability* above). The program key resolves the program
  *name* from the hub model (the event carries only the id); it is
  omitted until the program is known.
- `OptimisticRollbackPayload`, `DeviceTriggerPayload` — ride the same
  per-data-point topics and route to the same entity, so they carry the
  same key as the value-change.

How it landed:

1. `unique_id string` was added to the payload structs
   (`internal/north/rest/ws/`), populated at the publish boundary via
   `routingkey.CanonicalUniqueID(serialSuffix, address, parameter, eventPrefix)`
   — the serial suffix comes from `(*central.Registry).SerialSuffix`;
2. the `unique_id` property was added to each schema in `assets/openapi.yaml`;
3. `openccu-loom-types` is regenerated downstream from that spec;
4. the client consumes `payload.unique_id` when present, and keeps the
   rebuild path as a **verifiable fallback** for payloads that omit it.

The field is **optional** so the contract stays backward-compatible:
clients fall back to rebuild when it is absent.
