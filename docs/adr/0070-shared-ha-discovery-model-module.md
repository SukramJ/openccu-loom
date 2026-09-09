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
  note. No compatibility mode.
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
the Homematic domain.

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
  `unique_id` formats. This is the accepted cost of not freezing the drift
  above — including its bugs — permanently.
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
- [ADR 0063](./0063-self-maintained-device-profiles.md) — the rule table stays
  hand-maintained
- [ADR 0067](./0067-north-surface-is-a-model-api.md) — the MQTT plane keeps its
  entity projection; unaffected
- [ADR 0068](./0068-unique-id-stability-per-plane.md) — the identity guarantee
  this ADR knowingly breaks once
- `notes/concepts/shared-ha-discovery-model.md` — the full design
- Home Assistant core `2026.9.0b9-31-g76ca483aec0`:
  `homeassistant/generated/{device_classes,sensor}.json`,
  `homeassistant/components/mqtt/{abbreviations,discovery,schemas,sensor}.py`
