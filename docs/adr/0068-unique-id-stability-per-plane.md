# ADR 0068 — What `unique_id` promises, and it differs per plane

- Status: accepted
- Date: 2026-08-29

## Context

Three `unique_id` re-keys landed within two days: CUxD entities gained a
central-id slot (0.67.1), and system variables and programs moved from the slug
of a renameable name onto the CCU's own id (0.68.0). Each was correct on its
merits. Together they exposed that this project has never said what a
`unique_id` promises — only what it currently is.

The cost is visible downstream. `homematicip_local` carries **seven** entity
registry migration functions (`custom_components/homematicip_local/__init__.py`
:352, :463, :509, :609, :648, :687, :705), each encoding one specific re-key.
Every one of them exists because a key that a consumer had already written into
its registry stopped matching what the producer emits.

The trigger for writing this down was a measurement taken while planning the
eighth. Home Assistant's MQTT integration has **no migration path for
`unique_id` at all**:

| Checked in `../core` | Result |
|---|---|
| `previous_unique_id` / `old_unique_id` in `homeassistant/components/mqtt/` | 0 occurrences |
| `async_migrate_entries` in `homeassistant/components/mqtt/` | 0 occurrences |
| discovery schema around the key | one field, `unique_id` (`uniq_id`) |
| `homeassistant/components/mqtt/entity.py:1437` | `self._attr_unique_id = config.get(CONF_UNIQUE_ID)` |

The entity takes the value straight from the payload, and `discovery_update`
refreshes an existing entity in place without rebinding it. Nor can another
integration repair it: `async_migrate_entries` is scoped to one config entry
(`homeassistant/helpers/entity_registry.py:2721-2733`), and MQTT-discovered
entities belong to the MQTT config entry, not to `homematicip_local`'s.

So the two planes are not equally free. On REST/WS a re-key is a migration a
consumer can perform. On MQTT it is a break a consumer cannot repair.

## Decision

**The `unique_id` carries a different promise on each plane, and the promise is
stated rather than inferred.**

- **MQTT discovery — the key is immutable.** Once a `unique_id` has been
  published on this plane it does not change. A new vocabulary is keyed on a
  stable identifier from its first release, because there is no second chance.
  A change that would move an existing key is not shipped; the entity is
  retired and re-introduced under a new identity only when the underlying
  object genuinely became a different object.
- **REST/WS — the key may change, with a documented transition.** A re-key
  ships together with the consumer-side migration it requires, in the same
  release, and is recorded in
  `docs/external-clients/ha-unique-id-migration.md` before it is released.

**No `previous_unique_id` field.** It was designed and rejected; see below.

## Why

### The plane asymmetry is a property of the consumer, not a preference

The daemon can emit whatever it likes on either plane. What differs is what the
receiving end can do about it. A REST/WS consumer owns its registry and can
rewrite it. An MQTT consumer — Home Assistant, in practice — has no rewrite at
all, so a changed key silently orphans the old entry with its history, area and
every automation built on it, and spawns an unhistoried twin beside it.

Treating the two planes the same means either giving up re-keys everywhere, or
breaking MQTT users every time REST/WS earns one. Naming the difference is
cheaper than either.

### `previous_unique_id` was designed, and does not pay

The obvious generic answer is an optional `previous_unique_id` on every payload
that carries a `unique_id`: equal means nothing to do, different means migrate.
It was rejected on three measurements.

**It does not solve the hard part.** The field would arrive in the same payload
as the data, which is the same moment a consumer already has `legacy_name` and
`vid` and could compute the old key itself. The difficulty in the consumer was
never *where the mapping comes from* — it is *when the registry may be
rewritten*, which is before its entities are created. The field does not move
that moment.

**It would not have earned its keep.** Of the seven migration passes
`homematicip_local` carries, two originate in a daemon-side algorithm change
and could have used the field. The other five are backend switches
(aiohomematic ↔ loom) and local anchor changes (`entry_id` → serial), whose old
keys the daemon has never known and cannot emit.

**It misses the plane that needs it.** 18 REST schemas carry a `unique_id`, so
the field would be an 18-schema addition plus an old-algorithm code path in the
daemon per re-key — and Home Assistant's MQTT integration would ignore all of
it, per the table above.

What remains of its value is narrow: it would move knowledge of the old
algorithm from the consumer into the daemon. That is real but small, and it is
not worth a permanent contract surface.

### The consumer already holds what it needs

For the case that prompted this — sysvars and programs moving onto the CCU id —
the consumer receives `legacy_name` and the id in the same object, and the old
key was `slug(legacy_name)`. Every re-key this decision permits on REST/WS must
meet that bar: **the old key has to be derivable from data the consumer already
receives.** A re-key that fails it is not shippable, because no consumer could
migrate across it.

## Consequences

- MQTT discovery gains a stability guarantee it did not have. The programs
  re-key in 0.68.0 predates this ADR and stands; it is the last one on that
  plane.
- A new north-bound vocabulary is keyed on a stable identifier from its first
  release. "We can fix the key later" is only true on one plane and only with
  a migration attached.
- `docs/external-clients/ha-unique-id-migration.md` becomes a precondition of a
  REST/WS re-key rather than a record written afterwards.
- The derivability bar above is a design constraint on future keys: a key whose
  predecessor cannot be reconstructed from the payload cannot be replaced.
- Nothing here changes an existing key. `TestHubUniqueIDMatchesAcrossPlanes`
  keeps the two planes agreeing on the keys they now emit.

## Revisit when

- Home Assistant's MQTT integration grows a `unique_id` migration path. That
  removes the asymmetry this ADR is built on, and the MQTT promise can then
  match the REST/WS one.
- A re-key becomes unavoidable on the MQTT plane — a CCU-side identifier turns
  out not to be stable, say. The decision then is not "re-key anyway" but how
  to retire and re-introduce the affected entities deliberately, with the
  history loss stated up front.

## References

- `docs/external-clients/ha-unique-id-migration.md` — the transition record
- ADR 0020 — external-client wire contract
- ADR 0028 — contract digest and version guard
- ADR 0067 — the north surface is a model API
- `internal/model/hub/payload.go`, `internal/north/mqtt/hub_discovery.go`
  (`programUniqueSlug`, `sysvarUniqueID`)
- `tests/contract/hub_unique_id_cross_plane_test.go`
- Home Assistant: `homeassistant/components/mqtt/entity.py:1437`,
  `homeassistant/helpers/entity_registry.py:2721-2733`
