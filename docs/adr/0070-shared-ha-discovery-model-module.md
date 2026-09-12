# ADR 0070 — Extract the HA discovery model into a shared module

- Status: accepted
- Date: 2026-09-09

Extends [ADR 0050](./0050-mqtt-transport-shared-module.md) (which drew the
transport boundary and explicitly left the discovery layer behind) and reuses
the release pattern of [ADR 0053](./0053-go-openccu-data-module.md).

Full design: `notes/concepts/shared-ha-discovery-model.md` (working document, unpublished).

## Context

ADR 0050 moved the MQTT *transport* into `github.com/SukramJ/go-mqtt` and drew
the boundary deliberately: the shared module has no knowledge of the domain,
and *"the topic schema, HA-Discovery payload assembly, the value→payload
projection"* stay in `internal/north/mqtt`. That was right for the transport
question. It left the second half of the problem untouched.

Six projects now answer that second half independently — the five `go-*2mqtt`
bridges and this daemon:

| Repo | Package | LOC (excl. tests) | Payload style |
| --- | --- | ---: | --- |
| go-zendure2mqtt | `internal/hass` | 375 | `map[string]any` |
| go-mtec2mqtt | `internal/hass` | 591 | `map[string]any` |
| go-homeconnect2mqtt | `internal/hass` | 716 | `map[string]any` |
| go-daikin2mqtt | `internal/hass` | 846 | typed structs |
| go-unifi2mqtt | `internal/hass` | 1537 | typed structs |
| openccu-loom | `internal/north/mqtt` | 15756 | `payload` pkg + rule table |

The duplication is wider than payload assembly. Four or five near-identical
copies each exist of: the MQTT bootstrap (the comment block above `NewBreaker`
is word-for-word identical in four repos, as is the `mqttSession` glue struct),
the orphan reconcile, `IsOwnConfig`, `slugify`/`collapseTokens`/`umlautReplacer`
(the comment *"transliterates German umlauts to match HA's slugify"* appears
verbatim four times), the `object_id`/`default_entity_id` policy including its
~10-line rationale citing `home-assistant/core#157241`, `LocalizedName`/
`CodeForLabel`, the catalog loader frame, and the config loader.

The copies have drifted, and the drift is not a matter of taste:

- mtec publishes state **non-retained** — after a Home Assistant restart every
  entity sits at `unknown` until the next poll.
- mtec's availability topic is `<hass_base>/status/lwt`, inside Home
  Assistant's own birth tree, and is referenced by no entity. Wrong place and
  inert.
- homeconnect's daemon LWT is referenced by no entity either; a hard crash
  leaves a retained `online` standing.
- mtec's `slugify` is the only one without umlaut transliteration — "Größe"
  becomes `gr_e`.
- zendure namespaces `unique_id` with the *configurable* MQTT root, so changing
  `MQTT_TOPIC` orphans every entity and defeats its own `IsOwnConfig` sweep.
- The entity-id seed formula diverges four ways.
- **None of the six sets the `origin` block, and none uses device-based
  discovery.** Five identical instances of the same backlog.

Nobody chose any of this. It is what copy-and-edit produces over time.

## Decision

**Two new modules, and this daemon is the first full consumer.**

- **`github.com/SukramJ/go-hamqtt`** — the shared data model, the Home
  Assistant discovery bundle, and a publisher runtime on top of `go-mqtt`.
  Hand-written, SemVer, zero dependencies, MIT.
- **`github.com/SukramJ/go-ha-catalog`** — the Home Assistant vocabulary
  (device classes, state classes, units, per-platform discovery schemas,
  abbreviations), **generated from a Home Assistant core checkout**. CalVer
  snapshot constant, zero dependencies, a data artifact and not a lookup
  framework — the `go-openccu-data` charter of ADR 0053, verbatim.

Scope and shape, decided up front:

- The shared layer is model **and** discovery **and** publisher runtime. A
  types-only library would leave the publish loop, availability policy and
  orphan sweep duplicated six times.
- **Device-based discovery only** (HA ≥ 2024.11): one retained bundle per
  device at `<prefix>/device/<node_id>/config`. No per-entity legacy path.
- Bridge device catalogs stay declarative (YAML/JSON, schema-validated,
  codegen to Go). The *format* is shared; the *content* is not.
- **A clean break.** `unique_id` and topic schemas are harmonised, entities are
  re-created, and each consumer ships it as a major release with a migration
  note. No compatibility mode. *(Superseded for this daemon by the amendment
  below: openccu-loom keeps its `unique_id`.)*
- The MQTT bootstrap splits along ADR 0050's line: `SplitClient` and a
  connect-retry helper are additive transport ergonomics and go into `go-mqtt`;
  birth, LWT and availability policy are Home Assistant semantics and go into
  `go-hamqtt`.
- **`go-ha-catalog` regenerates and releases itself on every Home Assistant
  release, unattended.** Home Assistant cannot dispatch to us, so a scheduled
  workflow polls the `home-assistant/core` releases API daily and compares
  against the checked-in `SnapshotRef` (plus `workflow_dispatch` for a manual
  run). Every stable release — patches included — triggers a regeneration, but
  `hadiff` decides whether anything is published: a byte-identical catalog
  produces no commit and no tag. When the catalog did change and CI is green,
  the workflow commits, tags and releases without human review. `hadiff`'s
  classification sets the bump: additive changes are a minor, removals are a
  major.

**This repository's architecture is the source, and this repository migrates
first** — before any bridge. `internal/payload`, `internal/model/naming`,
`internal/routingkey` and the bridge mechanics (hash-dedup publish, retract,
orphan sweep, birth sync) move up; the daemon imports them back and keeps only
the Homematic domain. *(The three-package half of that sentence is superseded
by the third amendment below: `routingkey` stays, `naming` and `payload` split,
and the remaining work is a collapse of re-exports rather than a migration.)*

## Why

### The architecture to extract is already here, and it is ADR 0011's

The reusable part is not the discovery implementation — it is the separation
ADR 0011 calls *"declarative model, dumb bridge"*: the model owns semantics,
the bridge owns topics, JSON and retain. Four pieces carry over wholesale: the
`payload:"info|config|state"` struct-tag partitioning, `HADiscoveryContext`
(the model asks for topics and never formats one), optional capability
interfaces where not implementing one *is* the opt-out, and the priority-rule
catalog format with AND-criteria and pointer fields for "unset".

### Migrating this daemon first is what keeps the extraction honest

It is simultaneously the architectural source and the most demanding consumer:
17 platforms, multi-CCU scoping, 147 priority rules, a non-HA MQTT surface of
its own, and 15 756 production lines. If the extracted model cannot carry it,
the design is wrong. Learning that at phase 3 of 9 costs one repository;
learning it after five bridges have migrated costs six.

The alternative — extract from this daemon but leave it unmigrated — creates a
sixth copy immediately, which is the failure mode this ADR exists to end.

### The catalog belongs in its own module because it has its own clock

The HA vocabulary follows Home Assistant's monthly CalVer; the model follows
its own API stability needs. Coupling them forces a model release for every HA
update. ADR 0053 already solved this shape: generator repo → CI dispatch →
thin, dependency-free Go artifact with `go:embed` and a `SnapshotVersion`
constant → consumer builds the lookup semantics itself. The core checkout used
for the analysis (`2026.9.0b9-31-g76ca483aec0`) already lines up with the
`go-openccu-data` snapshot series (`2026.9.0`).

Extraction is two-stage. Home Assistant ships pre-generated
`generated/device_classes.json`, `generated/sensor.json` and per-domain
`icons.json` whose freshness `hassfest` enforces in HA's own CI — a stronger
guarantee than any parser we could write, and readable from Go with no Python
at all. A second pass in an HA venv adds the enums, `DEVICE_CLASS_STATE_CLASSES`,
the abbreviation tables, and the per-platform MQTT `DISCOVERY_SCHEMA`s. That
last one is the point of the exercise: the authoritative list of which JSON
keys are legal on which platform, which is what finally makes a payload
validator possible.

### Device-only discovery is affordable now and gets more expensive later

Every entity payload currently carries a full copy of the device block. An
HmIP-BWTH with ~40 entities publishes it 40 times. Since everything is already
grouped by `(nodeID, component, objectID)`, the structural change is small —
and it will not get smaller while five more consumers accumulate retained
per-entity topics.

### The known defects are fixed by construction, not by six patches

`Validate` runs before publish and an invalid bundle publishes nothing; today
this daemon publishes unvalidated, which is how the micro-sign class of bug
(HA silently discards a whole config over U+00B5 vs U+03BC) reaches production.
The `sensorMetadataByUnit` raw-vs-canonical unit bug — 542 occurrences of raw
`100%` against 36 of `%`, hence no `state_class`, hence no long-term statistics
— is blocked today on an import cycle that the extraction dissolves.

## Consequences

- Three of this repository's structural weaknesses are resolved as part of the
  move, not deferred: the three overlapping entity-description types collapse
  to one `Description`; the two discovery interfaces with **opposite**
  precedence (`HADiscoveryPayloadBuilder` frame-wins vs. `CombinedProjection`
  projection-wins) collapse to one ordered pipeline where a later stage always
  wins; the two `Bucket` enums become one.
- `internal/model/value/` — the unused second transcription of the value
  conversions, which documents itself as a warning label — is deleted rather
  than carried up.
- `internal/north/mqtt` shrinks to the Homematic domain. What stays: `hmenum`/
  `hmtypes`, the 21 custom datapoint builders, paramset semantics,
  `DataPointUsage`/`DataPointCategory`, the 147 rules and ~240 legacy entries,
  and the CCU translations and profiles from `go-openccu-data`.
- The daemon's own non-HA MQTT surface survives unchanged, through
  `Bridge.Handle` and `Bridge.PublishPayload` on the same client and layout.
  This ADR does not reopen ADR 0067.
- `go-mqtt` gains exactly two additive helpers and no domain knowledge. Six
  consumers depend on it staying a pure transport.
- Entity re-creation breaks history and automations for every consumer's users.
  The orphan sweep retracts the legacy retained configs on first start so no
  ghost entities remain, and each consumer documents the old and new
  `unique_id` formats. This was accepted as the cost of not freezing the drift
  above — including its bugs — permanently. *(No longer applies to this daemon;
  see the amendment. It still applies to a consumer that chooses the shared
  scheme.)*
- Fan-out follows ADR 0050's rule: tag the module first, then each consumer
  bumps its exact pin in its own squash PR. No `latest`, no lockstep.
- The unattended catalog release is safe *because* of that fan-out rule rather
  than in spite of it. Consumers pin an exact version, and
  `dependabot-auto-merge.yml` merges only non-major bumps — so an additive
  catalog update flows through on its own, while a removal arrives as a major
  bump that stops in each consumer's PR queue. The review gate sits downstream,
  where the breakage would actually surface as a compile error, instead of
  upstream where nobody can tell which consumer a dropped constant affects.
- Four tools ship alongside: `hacheck` (validate a bundle against the extracted
  platform schemas), `hagen` (catalog codegen), `hadiff` (catalog drift across
  an HA release), `hadoctor` (live broker inspection — it finds mtec's and
  homeconnect's availability defects automatically).

## Amendment (2026-09-10) — this daemon keeps its `unique_id`

The decision above calls for a clean break: `unique_id` harmonised across the
six consumers, entities re-created, a migration note per project. For
openccu-loom that part is withdrawn. **The `unique_id` this daemon publishes
does not change, and neither does any `entity_id` derived from it.**

The reason is the measurement ADR 0068 already took. Home Assistant's MQTT
integration has no `unique_id` migration path at all — not `previous_unique_id`,
not `async_migrate_entries`, and an entity takes the key straight from the
payload. So a re-key is not a migration a consumer can perform; it is a break
nobody downstream can repair. ADR 0068 permits such a break under six
obligations, and states plainly that the mitigation "does not save the history".

Against that, the benefit was harmonisation for its own sake. It buys nothing
an operator can see: on a fleet of ten thousand entities it costs every one of
them its history, its area, its customisations and every automation or
dashboard entry that names it. A shared *format* was never a requirement of the
shared *model* — `hadiscovery.Component.UniqueID` is a string the consumer
fills in, exactly as `channelUniqueID` fills it today.

Two things follow, and one deliberately does not:

- **Step 11 keeps its Identity half and loses the re-key.** `model.Identity`
  and the merge rules are worth having on their own; the key they compute is
  not published in place of the existing one.
- **Step 13 is unaffected.** Home Assistant keys its entity registry on
  `unique_id`, not on the discovery topic, so moving from one retained config
  per entity to one bundle per device leaves the registry entries in place.
  That is worth confirming against a live instance before the bundle migration
  ships, but it is not a reason to re-key.
- **The six consumers keep different `unique_id` formats.** That is the cost,
  and it is the smaller one: a new consumer can adopt the shared scheme from
  its first release, where a break avoided costs nothing.

Everything the typed-discovery work has landed so far already holds to this.
Across the nine changes of step 7, the full-fleet capture shows zero changes to
`unique_id`, `default_entity_id`, `object_id` and the discovery topic, over all
9,996 entities — measured, not assumed.

## Amendment (2026-09-10) — the bundle migration is measured, and ordered

The first amendment left one thing open: "That is worth confirming against a
live instance before the bundle migration ships." It has been. The measurement
was taken on a Home Assistant 2026.9 instance with 958 MQTT entities across 82
devices, using a throwaway device published for the purpose and removed
afterwards.

**The registry entry survives, exactly as assumed.** A sensor was discovered
under the per-entity form, then given a custom name, a custom icon and a
renamed `entity_id`. After the migration it came back carrying all three, on
the same `device_id`, with the same `unique_id`. Home Assistant keys the entity
registry on `unique_id` and not on the discovery topic, and this is that
statement measured rather than read.

**But the order is mandatory, and the wrong one fails silently.** Publishing
the device bundle while the per-entity config is still retained is refused:

```
WARNING [homeassistant.components.mqtt.entity] Received a conflicting MQTT
discovery message for entity sensor.…; the entity was previously discovered on
topic homeassistant/sensor/…/config …; the conflicting discovery message was
received on topic homeassistant/device/…/config
```

A log line is all there is. The retained bundle sits on the broker, the entity
keeps its old config, and nothing anywhere says the migration did not happen —
the same shape as every other defect this ADR exists to remove.

The order that works is the reverse: **retract the per-entity config first,
then publish the bundle.** Retracting removes the entity from the state machine
but leaves the registry entry; the bundle then reattaches to it.

Three consequences for step 13:

- **The orphan sweep runs before the bundle publish, not after it.** That is
  the opposite of the natural reading — publish the new thing, then clean up
  the old — and it is the one this daemon has to implement.
- **There is a window in which the entity does not exist.** Between the
  retraction and the bundle it is absent, not merely unavailable. The two
  publishes belong together, and a crash between them leaves the operator
  without the entity until the next start republishes it.
- **The refusal is symmetric, so the rollback needs the same care.**
  Measured the same way on 2026-09-11: a per-entity config published while
  the device document for the same entity is still retained is refused with
  the same warning, the topics named the other way round. Turning
  device-bundle mode back off is therefore not "stop publishing bundles" —
  without retracting the document first, every per-entity config of that
  first boot is refused and the only evidence is a log line. The round trip
  itself is lossless: the rollback probe kept its renamed `entity_id`, its
  custom name and its `device_id`.
- **`migrate_discovery: true` is not a substitute.** Setting it on the
  per-entity config did not lift the conflict in this run: the bundle
  published afterwards was refused with the same warning. Whatever the flag
  is for, the retraction is what this daemon must rely on.

## Amendment (2026-09-12) — the three-package move-up is wrong on two of three

The decision above ends with a sentence that reads as three package moves:
*"`internal/payload`, `internal/model/naming`, `internal/routingkey` and the
bridge mechanics … move up"*. That half of the sentence is **superseded**. The
packages were measured symbol by symbol before anything moved —
`notes/adr0070-moveup-inventory.md`, 262 exported symbols classified, every
"unused" claim counted — and the measurement disagrees with the decision on two
of the three packages. None of the three moves whole.

**`internal/routingkey` does not move at all.** It is on the list because it
produces `unique_id` and `unique_id` is a Home Assistant concept, but the
package's contract does not point at Home Assistant. It points sideways at
`aiohomematic` and the Python HA drop-in, and it is pinned there by 40 golden
cases under `tests/contract/testdata/routing_key/` and by
`script/routing_key_parity.py`, which replays them through the Python
reference. `HubSlug` is a `python-slugify` emulation: it separates with `-` and
folds `ü` to `u` where both candidate replacements separate with `_` and expand
it to `ue`, so the three functions disagree on every non-trivial input — and
`HubSlug` is *right* to, because python-slugify is what the drop-in produces on
the other side of the contract. Its address families (`INT000`, `CUX`,
`BidCoS-RF`, `HmIP-RCV-1`) are CCU families. And `slug.go` imports
`golang.org/x/text`, which a module whose charter is one requirement cannot
take. Moving it up would put a Homematic interop contract inside a module five
bridges import.

**`internal/model/naming` splits at a file boundary.** `discovery_slug.go` (88
lines, one export) is the only file in the package with no `hm*` import. The
other 1 121 lines are `PathData` — 864 of them — and `NameData`: loom's own
MQTT topic tree and loom's CCU name model, both resting on
`hmtypes.WireInterfaceID` and `hmenum.InterfaceVirtualDevices`. `go-hamqtt`
declines to own either on purpose; `topic.Layout` is an interface precisely so
each consumer keeps its own schema. And the small half does not *move* — it is
**deleted** in favour of `topic.Slug`, which changes published node ids and is
therefore the last step, alone, under ADR 0068's process.

**`internal/payload` splits three ways, not two.** `params.go` (137 lines, six
exports) goes up cleanly. The 99 typed CCU DTOs in `info.go`, `state.go` and
`descriptor.go`, the `hmenum.CommandPriority` contract that runs through
`Source.Invoke` and `ServiceRegistry.Invoke`, `TopicSlot`, and
`discovery_entity.go`'s adapter onto `hamodel`/`hatopic` all stay — the adapter
is *supposed* to live down here. What is left of the generic core is already
upstairs behind a thin wrapper.

**The bulk of the move already happened, and not as a move.** All twelve
discovery planes render through `discovery.RenderComponent`; the bucket enum is
`hamodel.Bucket`; topic escaping is `topic.Safe`; the reflection harvest is
`hapayload.ForWith`. What the decision's sentence actually describes, measured,
is **one file move (`params.go`), one deletion with byte risk
(`DiscoverySlug`), and a large collapse of duplicate re-exports.** Forty-seven
of the 262 symbols are daemon-agnostic at all; 22 of those 47 collapse in place
rather than move, because they are already the shared thing, re-exported.

Nothing has to move *with* the three, which is the other half of the finding:
the two packages that would be dragged — `pkg/hmenum` (3 794 lines) and
`pkg/hmtypes` (980) — are the Homematic vocabulary this ADR says stays.

Four consequences:

- **The move-up sentence is replaced by a sequence.** Delete the dead exports,
  collapse the duplicate `Bucket` alias, move `params.go`, settle
  `MQTTAddressable` against `topic.Layout`, and only then take `DiscoverySlug`.
  Every step that cannot change a published byte goes first, so the one that
  can arrives alone on a clean tree.
- **`DiscoverySlug` → `topic.Slug` needs fixtures before it needs a
  migration note.** The golden pins cover 68 node ids and German umlauts, but
  of the eight measured divergences between the two functions, zero appeared in
  any fixture: `Café` → `caf` becoming `cafe`, and `watchdog__ccu-jack`
  becoming `watchdog_ccu-jack`, both shipped green. Those rows are added ahead
  of the step rather than with it.
- **`UniqueSlug` / `EffectiveSlug` / `ZoneSlugStem` are the only routingkey
  symbols that could ever go up**, and only once they take the slug function as
  a parameter instead of calling `HubSlug`. Thirty lines, not a package move.
- **The first amendment's conclusion is reinforced, not reopened.** The package
  that computes the `unique_id` this daemon promised not to change keeps its
  home for the same reason the key keeps its spelling.


## Revisit when

- Phase 3 fails: if this daemon's layer cannot be expressed on the extracted
  model, the module is redesigned before any bridge migrates, and this ADR is
  superseded rather than patched.
- Home Assistant changes the device-discovery schema in a way `hadiff` cannot
  absorb additively.
- The unattended release produces a bad catalog that reaches a consumer. The
  answer is then a review gate on removals upstream, not abandoning the
  automation.
- A consumer appears that must support HA < 2024.11, which would reopen the
  per-entity legacy path this ADR closes.

## References

- [ADR 0011](./0011-mqtt-topic-and-payload-architecture.md) — declarative
  model, dumb bridge
- [ADR 0050](./0050-mqtt-transport-shared-module.md) — the transport boundary
  this ADR extends
- [ADR 0053](./0053-go-openccu-data-module.md) — the data-artifact module
  pattern reused for the catalog
- [ADR 0068](./0068-unique-id-stability-per-plane.md) — what a `unique_id`
  promises per plane, and why an MQTT re-key is a break nobody downstream can
  repair
- [ADR 0063](./0063-self-maintained-device-profiles.md) — the rule table stays
  hand-maintained
- [ADR 0067](./0067-north-surface-is-a-model-api.md) — the MQTT plane keeps its
  entity projection; unaffected
- [ADR 0068](./0068-unique-id-stability-per-plane.md) — the identity guarantee
  this ADR knowingly breaks once
- `notes/adr0070-moveup-inventory.md` — the symbol-level measurement of the
  three move-up packages that this ADR's third amendment rests on
- `notes/concepts/shared-ha-discovery-model.md` — the full design
- Home Assistant core `2026.9.0b9-31-g76ca483aec0`:
  `homeassistant/generated/{device_classes,sensor}.json`,
  `homeassistant/components/mqtt/{abbreviations,discovery,schemas,sensor}.py`
