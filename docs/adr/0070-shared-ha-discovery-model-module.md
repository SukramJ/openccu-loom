# ADR 0070 — Extract the HA discovery model into a shared module

- Status: **closed** (2026-09-14). Accepted 2026-09-09; the nine-phase
  programme it describes is complete. The closing section — *"The programme
  is complete"*, last before the references — carries the phase-by-phase
  outcome, the one phase that declined its final step, the erratum on the
  LOC table below, and the two findings the fan-out produced that belong to
  no single repository.
- Date: 2026-09-09

Extends [ADR 0050](./0050-mqtt-transport-shared-module.md) (which drew the
transport boundary and explicitly left the discovery layer behind) and reuses
the release pattern of [ADR 0053](./0053-go-openccu-data-module.md).

Full design: `notes/concepts/shared-ha-discovery-model.md` (working document, unpublished).

**What is frozen, and what is not.** *Context*, *Decision*, *Why* and
*Consequences* are a snapshot of what was believed and decided on
2026-09-09, and they are **left as written** — their value is that they
record the state of knowledge at the moment the decision was taken, so a
reader can tell a wrong prediction from a changed mind. They are therefore
read in the future tense they use. Every correction to them lives in a
later, dated section: the five amendments, and the closing section. Where a
frozen claim is corrected elsewhere, this document says so at the point of
the claim and does not edit it.

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

*This table is understated, and the closing section carries the erratum with
the measured figures: each bridge row counts one directory, while the surface
each migration actually had to move is 1.6x to 2.3x larger. The numbers are
left as written because five phases were planned against them.*

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

## Amendment (2026-09-13) — three claims in the decision text are false as written

Checked against the shipped modules while fixing the operator rollback
documentation. None of these changes a decision; all three are statements of
fact that a reader would take as current, and none of them is.

- **"Device-based discovery only … No per-entity legacy path"** (decision,
  4th bullet). `go-hamqtt` v0.34.0 ships a complete per-entity path:
  `publisher.LegacyEntity`, `publisher.LegacyTopicFunc`, and three form
  functions — `LegacyTopicWithNodeID`, `LegacyTopicByUniqueID`,
  `LegacyTopicByObjectID` — selected by `Config.LegacyEntityTopics` and named
  at boot by `Runtime.LegacyForms()`. The zero value is
  `LegacyTopicWithNodeID` alone, i.e. the per-entity form is the *default*.
  Nor does the headline default hold in practice: this daemon keeps
  `north.mqtt.discovery_bundles` **off**, so it runs the per-entity form, and
  the same is reported of the other consumers. The bullet describes an
  intention that the shared module deliberately did not adopt.

- **"`go-mqtt` gains exactly two additive helpers"** (consequences).
  `go-mqtt` v1.5.1 exports ten `With…` option helpers across `PublishOption`
  and `SubscribeOption`. `WithSubscriptionID` (MQTT 5.0 Subscription
  Identifiers) arrived in v1.5.0, after this sentence was written. The
  substantive half of the bullet — "and no domain knowledge … a pure
  transport" — is still true and is the half that mattered; the count is not.

- **"Four tools ship alongside: `hacheck`, `hagen`, `hadiff`, `hadoctor`"**
  (consequences). `go-hamqtt` v0.34.0 ships two: `cmd/hacheck` and
  `cmd/hadoctor`. `hagen` and `hadiff` do not exist.

The fourth amendment (2026-09-10, the bundle migration measurement) is
unaffected and remains the part of this ADR that records work actually done —
including the rollback hazard, which as of this amendment is finally written
somewhere an operator reads it (`docs/admin/configuration.md`,
`north.mqtt.discovery_bundles`).

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


## Amendment (2026-09-13) — the hub model keeps its topics; `MQTTAddressable` stays

The move-up sequence's step D was left open as a decision: either the hub
model stops returning finished topic strings and returns `model.Slot`
coordinates instead — in which case `payload.MQTTTopicSet` and
`payload.MQTTRole` disappear and five exports resolve — or it does not, and
they stay. **It does not.** `payload.MQTTAddressable`, `MQTTRoleAddressable`,
`MQTTRole` and `MQTTTopicSet` are decided, not blocked.

Four measurements decided it, and none of them is a preference:

- **The experiment has already run on this plane.** `hubTopicLayout` in
  `internal/north/mqtt/hub_discovery.go` *is* a `hamqtt/topic.Layout`, the hub
  plane renders every config through `discovery.RenderComponent` with it, and
  every one of its slot-taking methods ignores the slot and returns a string
  the builder composed — with a doc comment saying why: deriving it from the
  slot again "would be a second implementation of the same schema with nothing
  keeping the two in step". The daemon adopted the shared arrangement and, at
  the one point where it had to choose, chose finished strings. Removing
  `MQTTAddressable` relocates that composition; it does not remove it.
- **`topic.Layout` names two of the four topic kinds a hub object needs.**
  State and Command have a home; a CCU program's `trigger` and its per-role
  `execute_available` gate do not. `go-hamqtt`'s own `topic.PulseLayout` doc
  rules out a fifth `Layout` method — it "would break every one of the six
  consumers' layouts at once" — and a pulse is an outbound occurrence, not an
  inbound command topic. A slot arrangement would therefore still need a
  loom-local four-kind carrier, which is `MQTTTopicSet` renamed.
- **`model.Slot` is device-shaped and a hub object is not a device.**
  `Slot.Valid` requires a non-empty `Address` and at least one `Path` segment;
  a system variable has neither a device nor a paramset, and
  `<base>/<central>/hub/alarm_messages` has no leaf at all. Encoding this
  plane would put the literal string `"sysvars"` in the `Address` field.
- **The two runtime facts a hub topic needs are parameters here, and would be
  optional there.** `MQTTTopics(base, centralName)` cannot be called without
  the broker base and the resolved central; the compiler refuses. On a
  `model.Slot` they live in `Scope`, which nothing in this daemon fills, and a
  slot built without it renders a short topic that nothing catches. That is
  measured rather than argued: `deviceAvailabilitySlot` exists in
  `availability_runtime.go` for exactly this reason, and
  `availabilityLayout.Availability` returns the empty string when
  `len(s.Scope) < 2` — a silent empty on the one string that must be identical
  on both sides of an `availability_mode: all` entity.

**The runtime takeover did not need it, and it did not cost much.** Step D was
described as "the precondition for the publisher runtime taking over
availability and retract". The state, command and availability planes all run
on `go-hamqtt` now and step D never happened, so that justification is spent.
What it cost is one function: `deviceAvailabilitySlot`, six lines, injecting
the CCU and the interface into a slot on the way in because
`publisher.DeviceSlot` reads the coordinate off entity bindings and nothing
here fills `Scope`. **Step D would not have repaid it.** The reason `Scope` is
empty is not that the hub model returns strings — it is that `payload.WireSlot`
and `payload.CustomSlot` deliberately leave the coordinate partial, because the
bridge renders that plane for one channel event and, in `WireSlot`'s own words,
"naming it a second time from the model would be a second derivation of the
same fact with nothing keeping the two in step". `discovery.DeviceSlot` is
unusable here for a reason that lives on the datapoint planes, which step D
does not touch. The injection is the honest answer.

**And the shared module has since moved the other way.** `publisher.
ComponentStateTopic`'s doc records that deriving a state topic as
`layout.State(slot)` "is wrong on a third of the catalog" — the renderer
projects `state_topic` onto the 22 platforms that accept the key while a
`Layout` answers for all 32, so on the other ten "the config carries no such
key while the layout still hands out a plausible-looking topic", and
publishing there is silent in every direction. `StatePublisher.Publish` takes a
caller-supplied topic, documented as "what the state plane needs". A
slot-returning hub model would not reach the shared publisher any more directly
than the present one does.

Three consequences:

- **Five of the inventory's sixteen blocked symbols are settled**, four by
  decision and one — `MQTTTopicSet.IsZero`, which had no caller anywhere — by
  deletion. `MQTTTopicSet.Config` is deleted with it: a topic field no
  implementation ever filled and no caller ever read.
  `hub.InstallMode.MQTTTopics` and the `naming.MQTTHubInstallMode` free
  function it was the sole reader of are deleted too — a central-wide
  install-mode aggregate that no build publishes and no schema promises.
- **The cost is named and guarded.** Keeping the interface means every hub
  topic has two spellings: the model's and the discovery builder's, both
  through `internal/model/naming`.
  `TestHubModelAndDiscoveryDeclareOneTopic` pins them byte-equal, and
  `TestHubTopicLayoutIsACarrierNotASchema` pins the layout as a carrier rather
  than a second schema. `TestHubPlaneTopicsRoundTrip` did not cover the first:
  it matches command topics against wildcard subscriptions, so a
  `command_topic` that drifted while staying under `…/hub/sysvars/+/set`
  passed it — verified by mutation.
- **Step E is unaffected.** Nothing here touches `DiscoverySlug`, `HubSlug` or
  `routingkey`, and no published byte moves.


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

## Amendment (2026-09-13) — step E landed, and it moved `identifiers`, not `unique_id`

The third amendment ends by naming `DiscoverySlug`'s deletion as "the last
step, alone, under ADR 0068's process". It has landed. What the measurement
taken immediately before it establishes, and what the inventory could not say
because it measured slug *outputs* rather than the fields they feed:

**The eight divergent inputs reach three published fields and stop.** Traced
per production call site — five of them — and pinned by
`TestDiscoverySlugUnificationMovedTheseFields` and its `…LeftTheseFieldsAlone`
counterpart in `internal/north/mqtt`:

| Field | Reached | Moves |
| --- | --- | --- |
| discovery `node_id` | `PathData.DiscoveryNodeID`, `hubNodeID` | yes |
| discovery `object_id` | hub sysvar / program object ids | yes |
| device `identifiers`, `via_device` | `centralDeviceIdentifier`, `physicalDeviceIdentifier` | **yes** |
| `unique_id` | only `installModeInterfaceSuffix` / connectivity, over the CCU interface vocabulary | no |
| `default_entity_id` | seeded from `unique_id`, or suppressed | no |
| state / command / availability topics | `TopicSafe`, a different rule | no |

**The first amendment holds, and the third is narrower than it read.** This
daemon keeps its `unique_id`: no `unique_id` moved, because every one of them
is keyed on the CCU serial (`scopedUniqueID`) or the ISE id
(`routingkey.CanonicalUniqueID` over `HubSlug`), and `HubSlug` is untouched.
The one path from the discovery slug to a `unique_id` is the per-interface
install-mode and connectivity suffix, and it is safe only because every CCU
interface id (`HmIP-RF`, `BidCos-RF`, `BidCos-Wired`, `VirtualDevices`,
`CUxD`) is ASCII with no separator run. That is now asserted rather than
assumed — a new interface family carrying either would move a `unique_id`
with no migration path, and the test says so.

**`identifiers` is the cost, and it is a device-registry cost.** Home
Assistant keys the device registry on `identifiers` and has no migration for
it either, so an affected CCU's device card and its central-scoped device
cards are re-created. Device-level area, a renamed device and any
device-targeted automation go with them; every entity keeps its registry row,
its history and its `entity_id`. That is a materially smaller break than a
`unique_id` re-key, and it is still a break: it ships with the full ADR 0068
note in `docs/external-clients/ha-unique-id-migration.md`.

**The sweep needed extending, exactly as ADR 0068's "revisit when" predicted.**
That entry anticipates "a break that also moves the discovery topic, where
retracting the old config needs the sweep extended rather than inherited".
This is that case. `discoveryNodePrefixes` now carries a third spelling,
`legacyDiscoverySlug` — the pre-unification rule, kept for retraction only,
publishing nothing, deletable after one release.

**`internal/routingkey` stayed put, and the reason sharpened.** The third
amendment argued it on its aiohomematic contract and its `golang.org/x/text`
import. Both hold. The sharper form: unifying is about functions that answer
the same *question*. `DiscoverySlug` and `topic.Slug` both answered "what does
Home Assistant accept in a node id", and having two answers was the defect.
`HubSlug` answers "what does python-slugify produce", which is a different
question with a different right answer — and it is the one of the three that
feeds a `unique_id`, so moving it would have cost exactly what the first
amendment withdrew.


## Closing (2026-09-14) — the programme is complete

All nine phases have shipped. This section is the outcome record: it does not
revise the frozen sections above, it says what happened to each of their
claims. Where a phase's own repository holds the measurement, that note is
authoritative and is linked; nothing here is re-derived.

### The phases, and what each one proved

The nine-phase sequence lives in the design note
(`notes/concepts/shared-ha-discovery-model.md`, §8.2) and never appeared in
this ADR — which is why until now a reader could not tell from the ADR alone
that there had been phases, let alone that they had all closed. The table is
therefore reproduced here with its outcome column, and this is the copy a
reader of the ADR is meant to find.

| Phase | Work | Outcome |
| ---: | --- | --- |
| 0 | `go-ha-catalog`: two-stage extraction, first tag | Shipped. At **v0.2.1**, a `go:embed`ed data artifact with a CalVer snapshot constant, as ADR 0053 prescribes. |
| 1 | `go-mqtt`: additive `SplitClient` + connect-retry helper | Shipped, and then some. At **v1.5.1**; still a pure transport with no domain knowledge, but no longer "exactly two additive helpers" — see the first amendment. |
| 2 | The shared model, discovery bundle, validation, naming | Shipped as **`go-hamqtt`**, not `go-hamodel` — the name was decided against during design. At **v0.34.1**. |
| 3 | **openccu-loom migrates** | Shipped in **v0.78.0**: 92 merged pull requests (#739–#834), most of them this migration, plus three rounds of adversarial review. The model carried the hardest consumer, which is what phase 3 existed to establish. |
| 4 | Runtime layer (state, command, availability, sweep, birth) | Shipped, but not in the shape §3.6 drew — see *What the design got wrong* below. |
| 5 | `go-zendure2mqtt` — the pilot | Complete through the bundle migration. Two defects found afterwards, both fixed: a process-lifetime `publisher.Runtime` that memoised a QoS 0 retraction flushed to a dying socket (measured on the fleet: **7 of 29 retractions re-sent, both documents published anyway, 22 of 29 entities would not have appeared**; fixed with `Runtime.Reset()` in `PublishOnline`), and `LegacyEntityTopics` spelled twice with nothing comparing them. No tombstones. |
| 6 | `go-mtec2mqtt` | Complete, bundle shipped (100 components, ~50 KB on the wire). An adversarial review then found **eleven** findings, four harness-proven; the first was that the retract-then-publish ordering the whole migration rests on holds *within* a connection and breaks *across a reconnect* (measured: **retractions re-sent 0, document published true, configs still retained**). Fixed structurally, by rebuilding the runtime per connection. Tombstones deliberately not implemented, recorded as a known limitation: a withdrawn entity lingers, and lingers as *available*. |
| 7 | `go-homeconnect2mqtt` | Complete, bundle shipped (687 per-entity configs → one document per appliance, **472 847 bytes**), tombstones implemented by broker read-back. Two reviews of the bundle work, both fixed: the read-back, and an attribution rule defeated by a **nested** sibling root — driven over the shipped catalogue, the outer instance claimed 687 of 687 of the inner one's components and deleted the **510** that were live, repeatedly. Attribution is now an exact match against a topic only one instance renders, never a prefix. |
| 8 | `go-daikin2mqtt` | Complete, bundle shipped (264 per-entity configs → 31 documents), tombstones implemented. A review found the scheduler's compile-time node id shared between siblings, so the read-back marked a sibling's **live** switches removed and the armed sweep retracted their configs — a permanent ping-pong. Fixed by two independent closures; the same-account variant is separable by no predicate and is pinned and documented rather than fixed. The composite `climate` is this phase's proof and it held: all **14** reproduced byte for byte, seven role bindings of which **five are synthetic**, carried as ordinary `model.Slot`s with **no runtime special case**. |
| 9 | `go-unifi2mqtt` | Complete **except the bundle**. Steps 0–5 shipped; step 6 was deliberately declined — see immediately below. |

### Phase 9 declined its final step, and the reason is not a defect

This is recorded exhaustively in that repository — [`notes/adr0070-phase9-measurement.md`](https://github.com/SukramJ/go-unifi2mqtt/blob/main/notes/adr0070-phase9-measurement.md),
section *"Step 7 outcome"* — and nowhere here, where a reader of the ADR
would look for it. In short:

**Two UniFi consoles cannot be told apart.** `Site.Internal` — the API's
`internalReference` — is `default` on every console out of the box, and it is
the only site-scoped string the bridge has. Two consoles on one broker
therefore publish byte-identical config topics, `unique_id`s,
`device.identifiers`, node ids, `default_entity_id`s, state topics and
availability topics; changing `MQTT_TOPIC` moves the availability topic and
only the availability topic. Every candidate identity that would separate them
re-registers entities Home Assistant has already registered — which is the
break ADR 0068 says nobody downstream can repair, and which the first
amendment above withdrew for this daemon on exactly the same reasoning.

**The bundle makes the collision worse rather than better.** Under the
per-entity form the collision is an overwrite entity by entity; a bundle is one
retained topic carrying a device's entire component set, so two consoles would
replace each other's *whole* entity set on every poll. So the decline is a
trade, not a deferral.

**It is declined with notice.** Step 6 becomes available when the bridge has an
identity that is per console, stable across restarts and renames, known before
the first publish, and **outside both registry keys** — it may enter the
bundle's `node_id`, which Home Assistant keys nothing on, but not `unique_id`
and not `device.identifiers`. The fourth property is the one every candidate so
far has failed.

That one of nine phases can decline its last step on a measurement, and say
what would reopen it, is the fan-out rule of ADR 0050 working as intended: each
consumer decides in its own repository, at its own pin.

### Erratum — the LOC table understates every bridge row

The table in *Context* (restated in *Why* as "15 756 production lines", and
identically in the design note's §2.1) is not false: each bridge figure is
exactly the `internal/hass` line count it claims to be. What it does not say is
that `internal/hass` is roughly **half** the surface each migration actually had
to move — the topic schema, the publish loop, the orphan reconcile, the birth
and LWT wiring and the MQTT bootstrap live in `coordinator`, `bridge`,
`process` and `main.go`. Every phase measured this independently and every
phase found the same shape:

| Repo | Recorded | Measured today | Addressable surface | Ratio |
| --- | ---: | ---: | ---: | ---: |
| go-zendure2mqtt | 375 | 372 | ~672 | 1.8x |
| go-mtec2mqtt | 591 | 576 | 942 | 1.6x |
| go-homeconnect2mqtt | 716 | 713 | 1 318 | 1.8x |
| go-daikin2mqtt | 846 | 838 | ~1 619 | 1.9x |
| go-unifi2mqtt | 1537 | 1528 | ~3 486 | 2.3x |

Measured by brace-matched function extents, and by whole files where the whole
file is topic or payload construction; each phase note carries its own row
breakdown. Two separate corrections travel together here and should not be
confused: the **ratio**, which is the substantive one, and a few lines of
staleness in each recorded figure, all from the same class of commit dropping
the dead `object_id` discovery key.

**The loom row is not corrected, because it is on a different basis.** 15 756
is `internal/north/mqtt` entire, not a discovery subdirectory, so the ~2x
adjustment does not obviously apply to it and no phase measured it. Two of the
bridge notes generalise their finding to "all six rows"; that generalisation is
asserted for loom, not verified, and is recorded here as such.

The practical consequence is the one worth carrying forward: **a plan sized off
the package that holds the payload builder is sized off half the job**, and the
bridge with the most dynamic entity set (unifi, 2.3x) pays the most, because
announce, clear and reconcile grow with it.

### Two findings that belong to no single repository

Both were found more than once, in different repositories, by different
reviews. Neither is about Home Assistant, MQTT or this model; they are about
what a test has to do to be worth having, and they are written down here
because otherwise they exist only scattered across six repositories' notes.

**A value spelled twice — once in production and once in a fixture — with
nothing comparing them.** The programme numbers its own instances and reached
**five**: mtec's legacy availability topic (whose test forwarded a literal and
asserted the same literal) and its birth topic built twice and diverging on a
trailing slash; homeconnect's breaker-bypass topic and its duplicated
`haplanePacketSize`; and zendure's `publisher.Config.LegacyEntityTopics`, where
**deleting the field from the composition root left the entire suite green**.
Two further near-misses were recorded as avoided rather than found, and one
instance pre-dates the numbering. A fixture that re-states the production value
does not compare anything; it pins the test to itself. The check that works
compares two independently produced spellings, and a mutation of one side has
to turn it red.

**Drive the operation; asking the predicate proves nothing.** Every repository
that only asserted its ownership predicate either missed a defect or proved
nothing at all. The two worst defects of the programme — homeconnect's nested
sibling (510 live components tombstoned) and daikin's scheduler ping-pong — are
both invisible to a predicate test and both fell out of *driving the sweep*
against a second instance's live configs. This daemon paid the same tuition
twice, in #826 and #833, and #833's fixtures now drive the topic-keyed sweep
against a second loom daemon rather than only against a foreign integration. It
is also what `go-hamqtt`'s `SweepRequest.SelfClaimed` exists for: ownership
stopped being a predicate over the payload and became a claim list, because a
`button` or `climate` payload carries no `state_topic` and the predicate
collapsed to a shared prefix — 39 of this daemon's 9 996 configs, 24 of 264 for
daikin, 20 of 687 for homeconnect.

### What the design got wrong, and what it did not deliver

Recorded so a reader can calibrate the frozen sections rather than trust them:

- **The runtime façade did not survive contact.** The design note's §3.6 draws
  a single `Bridge` type over an `mqtt.Client`. What shipped is
  `publisher.Runtime` plus separate `StatePublisher`, `AvailabilityPublisher`
  and `CommandRouter`, over a narrow `Transport` interface rather than a
  `go-mqtt` client — a smaller dependency and a seam each consumer could adopt
  one plane at a time, which is what made the five bridge migrations
  incremental instead of atomic. This is the one large shape change between
  design and delivery.
- **`hagen` and `hadiff` were never built**, and the catalog releases without
  them; see the first amendment.
- **The `sensorMetadataByUnit` defect is still open in this daemon.** *Why*
  claims it is "blocked today on an import cycle that the extraction
  dissolves". The extraction happened; the fix did not follow it.
  `internal/north/mqtt/discovery.go` still passes `ev.descUnit()` — the raw
  wire spelling — to `resolveSensorStateClass`, against a table keyed on
  canonical units, so the affected sensors still publish without a
  `state_class` and Home Assistant still keeps no long-term statistics for
  them. It is a benefit the ADR promised and the programme did not collect.
- **`homeassistant/` is still hardcoded here.** The shared module made the
  prefix a parameter (`discovery.DefaultPrefix`, a constant the consumer may
  override); this daemon still spells it as
  `naming.DiscoveryTopicPrefix`, with no operator knob. The design note's §2.4
  listed it as a loom weakness and it remains one.

### The "Revisit when" entries, dispositioned

- *Phase 3 fails* — it did not. The ADR is closed rather than superseded.
- *A consumer that must support HA < 2024.11* — moot: the per-entity path was
  never removed, and is in fact the default (first amendment).
- *Home Assistant changes the device schema in a way `hadiff` cannot absorb* —
  `hadiff` does not exist; the guard in practice is `go-ha-catalog`'s
  regenerate-and-compare workflow plus each consumer's exact pin.
- *The unattended release produces a bad catalog that reaches a consumer* —
  has not happened; this one stands as written and is the only entry that
  outlives the ADR's closure.


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
- The five phase measurements, each authoritative for its own phase:
  [go-zendure2mqtt `docs/adr0070-pilot-measurement.md`](https://github.com/SukramJ/go-zendure2mqtt/blob/main/docs/adr0070-pilot-measurement.md) (phase 5),
  [go-mtec2mqtt `notes/adr0070-phase6-measurement.md`](https://github.com/SukramJ/go-mtec2mqtt/blob/main/notes/adr0070-phase6-measurement.md) (6),
  [go-homeconnect2mqtt `notes/adr0070-phase7-measurement.md`](https://github.com/SukramJ/go-homeconnect2mqtt/blob/main/notes/adr0070-phase7-measurement.md) (7),
  [go-daikin2mqtt `notes/adr0070-phase8-measurement.md`](https://github.com/SukramJ/go-daikin2mqtt/blob/main/notes/adr0070-phase8-measurement.md) (8),
  [go-unifi2mqtt `notes/adr0070-phase9-measurement.md`](https://github.com/SukramJ/go-unifi2mqtt/blob/main/notes/adr0070-phase9-measurement.md) (9, including the step-6 decline)
- [`go-hamqtt` CHANGELOG](https://github.com/SukramJ/go-hamqtt/blob/main/CHANGELOG.md) —
  v0.34.0's guards, each one traceable to a defect a consumer measured
- Home Assistant core `2026.9.0b9-31-g76ca483aec0`:
  `homeassistant/generated/{device_classes,sensor}.json`,
  `homeassistant/components/mqtt/{abbreviations,discovery,schemas,sensor}.py`
