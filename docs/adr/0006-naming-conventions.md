# ADR 0006 — Naming Conventions for REST + MQTT Surfaces

- **Status**: Accepted
- **Date**: 2026-04-28
- **Related**: ADR 0005 (visibility-as-outbound-filter), CHANGELOG entry
  "MQTT Topology Cleanup"

## Context

The first cut of the REST + WS + MQTT surface accreted ad-hoc names:
- `data_points` (snake_case) lived next to `custom-data-points` and
  `calculated-data-points` (kebab-case).
- Long path segments — `custom-data-points` is 18 chars, repeated in
  every URL.
- `PUT .../custom-data-points/{name}/state` with `{operation, params}`
  in the body conflated state-mutation (idempotent) with action
  invocation (non-idempotent).
- `POST /backup` was a singular outlier among `/backups/{id}/...`.
- MQTT had a redundant `hub/` namespace under `{base}/{central}/`
  that disambiguated nothing (every hub-entity sits there alone).
- HA Discovery used a hardcoded `openccu-loom` node — Multi-Daemon
  setups on the same broker collided.

This ADR records the conventions chosen for the v1.0 surface so
future endpoints stay consistent.

## Decision

### REST URL conventions

1. **Kebab-case** for all multi-word path segments.
2. **Plural collection nouns**: `/devices`, `/programs`, `/sysvars`,
   `/backups`, `/cdps`, `/calc-dps`, `/data-points`, `/link-ps`.
   The collection name is plural even when only one item is
   addressable in a request (`POST /backups` creates ONE backup;
   the URL still uses the plural collection).
3. **Approved abbreviations** for the data-point family:
   - `dp` / `data-points` → "data point" (the wire-level paramset DP)
   - `cdp` / `cdps` → "custom data point" (Light, Cover, Climate, …)
   - `calc-dp` / `calc-dps` → "calculated data point" (DewPoint, …)
   - `link-ps` → "link paramset" (peer-keyed paramset)
   These abbreviations are also used in WS command namespaces and in
   audit-log source tags.
4. **HTTP method choice** — strict semantic discipline:
   - `GET` for reads, no side effects.
   - `PUT` for idempotent state-set (write a value, replace a config).
     A second identical PUT is indistinguishable from the first.
   - `POST` for non-idempotent actions: invoke an operation,
     execute a program, ack a message, trigger a backup.
   - `PATCH` for partial-update of metadata (description / unit /
     value-list). Disjoint from PUT, which sets the runtime value.
   - `DELETE` for resource removal.
5. **Action endpoints** carry the operation in the URL, not the body:
   - `POST /devices/{addr}/cdps/{name}/{operation}` (not
     `PUT .../state` with `{operation: "..."}` in the body)
   - `POST /programs/{id}/execute` (the action verb after the id)
   - `POST /service-messages/{id}/ack`
   This makes the request log + audit trail self-describing.
6. **Filter parameters** as query string, not URL:
   `GET /audit?device=0001&op=rest:cdp&since=2026-04-28T12:00Z`.

### WS command conventions

1. **Dot-namespace** mirroring REST collection names:
   `cdp.list / .get / .invoke`, `calc_dp.list / .get`,
   `paramset.put / .get`, `program.execute`, `sysvar.put`.
2. WS uses **snake_case** internally because the dispatch keys are
   not URL segments — kebab would require quoting in many client
   libraries. (`cdp` / `calc_dp` are short enough to read either way;
   we picked snake to match the existing `paramset.put`-style.)
3. Categories in `assets/wsapi.json` align with REST collections:
   `cdp`, `calc_dp`, `paramsets`, `programs`, `sysvars`, `system`,
   `central`, …

### MQTT topic conventions

1. **No `hub/` namespace** under `{base}/{central}/` — hub-entities
   sit directly under the central:
   `{base}/{central}/install_mode`, `{base}/{central}/sysvars/{name}`.
   The dropped `hub/` was a stylistic vestige — there was no other
   namespace to disambiguate against.
2. **`devices/` namespace** for device-scoped paths to avoid colliding
   with the future `groups/` namespace and with the hub-entities:
   - `{base}/{central}/devices/{addr}/availability`
   - `{base}/{central}/devices/{addr}/cdps/{name}/{operation}/invoke`
   - The raw paramset path stays under the canonical
     `{base}/{central}/{iface}/{addr}/{ch}/{param}` (no `devices/`),
     because that path is keyed by interface-id which is not
     `devices` — it's the wire transport.
3. **Snake_case** for path segments (matches MQTT convention
   `homeassistant/binary_sensor/...`).
4. **HA Discovery node** derives from `BridgeConfig.Base`, not a
   hardcoded literal — `homeassistant/{component}/{base}/{objectID}/config`.
   Default `Base="openccu-loom"` reproduces the legacy form;
   non-default Bases give multi-daemon installations distinct
   discovery roots.
5. **Inbound command topics** end in a verb suffix:
   - `/set` for parameter-value writes (raw paramset)
   - `/invoke` for CDP-operation invocation
   - `/trigger` for program triggering
   - `/restart`, `/refresh`, etc. as needed
   The verb makes intent explicit and prevents accidental writes
   from ambiguous wildcards.

### Migration

1. The `hub/` topology drop ships with a `LegacyAliasConfig.HubTopics`
   opt-in (default `false`). Operators flip it to `true` to mirror
   onto both old and new topics during their MQTT-subscriber rollout,
   then disable.
2. REST URL renames are pre-release; no backwards-compat aliases.
3. `data_points` → `data-points` (kebab fix) is pre-release;
   no compat alias.
4. WS command renames (`custom_data_point.set` → `cdp.invoke`) are
   pre-release; no compat alias.

## Consequences

**Positive**

- URL surface is consistent and predictable.
- `cdp` / `calc-dp` shorten common audit-log entries by ~30 %.
- Method semantics are correct: clients can rely on PUT idempotency
  and POST non-idempotency without reading code.
- MQTT topic tree is shallower; `hub/` redundancy gone.
- Multi-daemon HA Discovery setups don't collide.
- ADR pins the conventions so the next contributor doesn't reintroduce
  drift.

**Negative**

- One-time pre-release breaking change for REST + WS clients. Easy
  before any release; would be expensive after.
- Operators with running MQTT consumers must opt-in to LegacyAlias
  during the migration window or update subscriptions atomically.

**Neutral**

- The kebab-vs-snake-case split between REST URLs and WS commands +
  MQTT topics is pragmatic, not principled. Each surface has its
  own ecosystem convention; we follow the local one.

## Implementation references

- REST routes: `internal/north/rest/router.go`
- REST handler renames: `internal/north/rest/handlers/custom_data_points.go`
  (`PutCustomDataPointState` → `InvokeCustomDataPoint`)
- Audit-filter params: `internal/north/rest/handlers/audit.go`
- WS command catalogue: `assets/wsapi.json`, `tests/contract/wsapi_schema_test.go`
- OpenAPI spec: `assets/openapi.yaml`
- MQTT topic builder: `internal/north/mqtt/topics.go`
- Legacy hub-topic mirror: `internal/north/mqtt/legacy_alias.go::HubTopicBuilder`
- CDP invoke topic: `TopicBuilder.CustomDPInvoke` +
  `CommandSubscriber.handleCDPInvoke` + `CDPInvocationSink`
- HA Discovery node: `TopicBuilder.DiscoveryConfig` (uses `b.Base`)

## Amendment (2026-09-12) — the LegacyAlias opt-in was never reachable, and is gone

The Migration section above tells operators that "the `hub/` topology drop
ships with a `LegacyAliasConfig.HubTopics` opt-in (default `false`)", the
Consequences section tells them they "must opt-in to LegacyAlias during the
migration window or update subscriptions atomically", and the Implementation
references point at `internal/north/mqtt/legacy_alias.go::HubTopicBuilder`.
**All three sentences were false from the day this ADR was accepted.** They are
withdrawn. `legacy_alias.go` is deleted, and no mirrored topology exists or
ever did.

What was measured, before deleting anything:

- **`LegacyAliasConfig.HubTopics` never existed.** `git log -S "HubTopics"`,
  unrestricted, returns four commits; the one that introduces the identifier is
  `cd9e8ac0` ("Initial release"), and what it introduces is the **prose of this
  ADR**, at `docs/adr/0006-naming-conventions.md`. *(Correction of 2026-09-13:
  this bullet originally cited `git log -S "HubTopics" -- internal/` for that
  claim. A pathspec of `internal/` cannot reach a file under `docs/`, so it
  could not have been the command that established it. The pathspec form does
  return `cd9e8ac0` — its match there is a comment in
  `internal/central/adapter/hub_mqtt_publisher_test.go` mentioning the field,
  which is a test's prose about the same non-existent thing, not the field.
  Drop the pathspec, or use `-- docs/`, to reach the ADR. A wrong citation in
  an amendment is worse than none, because the next reader treats it as
  verified.)* The shipped struct
  carried two fields, `Enabled` and `Base`, and never a third. `HubTopicBuilder`
  never existed either: the file's only two types were `LegacyAliasConfig` and
  `LegacyTopicBuilder`, from `cd9e8ac0` until deletion.
- **The mirror it did implement was the flat *device* tree, not the `hub/`
  one.** `LegacyTopicBuilder` rendered
  `{base}/device/status/{addr}/{addr}_{ch}_{param}` and
  `{base}/device/availability/{addr}`. So even the feature that existed was not
  the migration path this ADR describes for the `hub/` topology drop, and it
  could not have carried a single operator across it.
- **No operator could enable it.** `BridgeConfig.LegacyAlias` was set at
  exactly one production construction site, `cmd/openccu-loom/daemon_north.go`'s
  `mqtt.NewBridge(mqtt.BridgeConfig{…})`, which never assigned the field. There
  is no YAML key (`NorthMQTT` carries twelve `yaml`-tagged fields and none of
  them is this one), no environment override, no CLI flag and no build tag.
  `cfg.LegacyAlias.Enabled` was the Go zero value `false` on every build ever
  produced, so `Bridge.legacy` was always `nil` and all six guarded branches in
  `bridge.go` were dead.
- **Only tests ever set it.** Four sites in `legacy_alias_test.go` set
  `LegacyAlias.Enabled = true`; one in `bridge_edge_cases_test.go` assigned the
  private `b.legacy` field directly. Nothing else in the repository, in either
  direction.
- **It was never announced, deprecated or scheduled.** `CHANGELOG.md` has no
  entry mentioning it, and `git log --follow` on the file shows two commits:
  `cd9e8ac0` ("Initial release") and `fb722716`, a license-header chore.

The migration window this ADR opened therefore never opened. The `hub/`
topology shipped without a compatibility mirror, operators updated their
subscriptions atomically because that was the only option available to them,
and the pre-release framing in the Migration section ("no backwards-compat
alias") is what actually happened on the MQTT surface too.

`docs/mqtt-topic-schema.md`, the operator-facing contract, never documented the
legacy tree and needs no change. The deletion moves no published byte: the
branches removed from `PublishState`, `PublishAvailability`, `EvictState` and
`RetractRawStateForDevice` were all behind `if b.legacy != nil`.

One thing worth keeping from the episode. The mirror's payload renderer,
`Bridge.renderStatePayload`, was the only place in the whole north/mqtt package
that read the wall clock at publish time, stamping `modified_at` with
`time.Now()` rather than with the event's own timestamp. It is deleted with the
rest, which removes one of the two payload shapes that would defeat a
byte-comparison dedup gate on the state plane.

## Amendment (2026-09-13) — four of the five MQTT topic conventions are false

§MQTT topic conventions above lists five rules. Two hold — snake_case
segments (3) and the inbound verb suffixes (5). The other three carry four
claims that the daemon contradicts, and each one is the kind a reader acts
on: they are spelled as concrete topic paths.

**1. "No `hub/` namespace."** There is one, and it is where most of the
per-CCU surface lives: `<base>/<central>/hub/status`,
`hub/sysvars/<name>/{state,set}`,
`hub/programs/<id>/{state,set,trigger,execute_available}`,
`hub/connectivity/<iface>`, `hub/install_mode/<iface>` and its `/set`,
`hub/update`, `hub/alarm_messages`, `hub/service_messages`, `hub/inbox` —
and the two reserved shapes `hub/info` and `hub/diagnostics`. The two
example paths this rule gives are both wrong: it is not
`{base}/{central}/install_mode` but `{base}/{central}/hub/install_mode/{iface}`,
and not `{base}/{central}/sysvars/{name}` but
`{base}/{central}/hub/sysvars/{name}/state`. The rule's own justification —
"there was no other namespace to disambiguate against" — is what stopped
being true: `system/` sits beside it, carrying `system/status` and the three
central-wide metric topics, and `devices/` beside that.

**2a. The device availability path.** The rule gives
`{base}/{central}/devices/{addr}/availability`. The daemon publishes
`{base}/{central}/{iface}/{addr}/availability` — interface-keyed, the same
prefix as `/info`, `/diagnostics`, `/update` and every channel topic. The
`devices/` namespace is real, but it carries exactly one shape: the
custom-DP invoke topic
`{base}/{central}/devices/{addr}/cdps/{name}/{operation}/invoke`, which this
rule also gives and which is correct.

**2b. The raw paramset path is missing a segment.** The rule writes it as
`{base}/{central}/{iface}/{addr}/{ch}/{param}`. Between `{ch}` and
`{param}` sits the **paramset bucket** — `values`, `master` or
`calculated` — so the canonical shape is
`{base}/{central}/{iface}/{addr}/{ch}/values/{param}`. The bucket is not
cosmetic: it is what lets the same parameter name exist in two paramsets
without collision, and the command subscriber registers an 8-segment filter
for the bucket-aware shape beside a 7-segment one for the bucket-less
spelling this rule describes.

**4. `DiscoveryConfig` does not use `b.Base`.** The rule says the HA
Discovery node "derives from `BridgeConfig.Base`, not a hardcoded literal —
`homeassistant/{component}/{base}/{objectID}/config`", and the
implementation reference below repeats it as "`TopicBuilder.DiscoveryConfig`
(uses `b.Base`)". It does not. The method delegates to
`naming.DiscoveryConfigTopic(component, nodeID, objectID)` and never reads
the receiver's base at all. The `node_id` segment is
`<central-slug>_<address-lower>` for device entities — it distinguishes one
*device* from another, which is HA's own convention — and a fixed literal
for the daemon-level planes (`security`, `alarm`). The multi-daemon
collision this rule set out to prevent is therefore **not** prevented by a
configurable base: two daemons bridging the same CCU under different topic
bases still write the same `homeassistant/.../config` topics.

That last one is a live gap, not a documentation error, and it is recorded
here rather than fixed: changing the discovery node id moves every retained
config topic on the broker and orphans the old ones, which is a migration
with its own ADR, not a drive-by.

*(Superseded on 2026-09-13 — see the amendment below. The gap is closed and
rule 4 now describes the implementation.)*

The section is not rewritten. What the conventions were meant to achieve —
one namespace per concern, verb-suffixed inbound topics, no hardcoded
discovery root — still reads as the decision. The paths are corrected here,
and `docs/mqtt-topic-schema.md` is the authority on what the daemon actually
writes; since 2026-09-13 it is checked against the builders in both
directions by `tests/contract/mqtt_topic_schema_producer_test.go`.

## Amendment (2026-09-13) — rule 4 is implemented, and it was never the base *alone*

The amendment above recorded that `DiscoveryConfig` ignored `b.Base`, called
it "a live gap, not a documentation error", and left it. It is fixed now, in
the same unreleased window as the discovery-slug unification, so operators
absorb one disruption rather than two.

**What the fix is.** `TopicBuilder.DiscoveryConfig` prefixes the `node_id`
with the topic base's slug, and only when that base is not the default:

```
homeassistant/<component>/[<base-slug>_]<node_id>/<object_id>/config
```

`naming.DiscoveryBaseScope` is the rule and `naming.ScopedDiscoveryNodeID`
joins the two halves. The bundle plane renders its topic inside `go-hamqtt`
and never reaches the builder, so it scopes in `Bridge.routeToBundle`; the
retained-config sweeps learn the scope through `discoveryNodePrefixes` and
`daemonLevelNodeID`.

**Rule 4's original wording was wrong about the mechanism, and stays
withdrawn.** It said the node "derives from `BridgeConfig.Base`, not a
hardcoded literal — `homeassistant/{component}/{base}/{objectID}/config`",
i.e. the base *replacing* the node id. That form cannot work: `node_id` is
also what separates one device from another within a daemon, which is Home
Assistant's own convention and what the multi-CCU layout depends on. The base
is a **prefix** to that, not a substitute for it. The decision rule 4 was
reaching for — one daemon, one discovery namespace — is what is implemented;
the path it wrote down is not.

**Only a non-default base contributes.** A scope that always applied would
move every retained config of every installation, single-daemon ones
included, for a collision they cannot have. Setting a base is how an operator
asks for a namespace, so it is what earns one. Two daemons that both leave the
base at its default are not separated, deliberately: they already overwrite
each other on every raw state topic, which the discovery plane cannot repair
and must not disguise.

**What this does not fix, stated because rule 4's "Consequences" line
("Multi-daemon HA Discovery setups don't collide") over-promises it.** Two
daemons against the *same* CCU still declare byte-identical `unique_id`s and
device `identifiers` — both are keyed on the device address and the CCU
serial, never on the base or the central name (ADR 0024 role 3). Home
Assistant keys its entity registry on the first and its device registry on
the second, so a single HA instance still resolves both daemons to one set of
entities however the discovery topics are spelled. What the scope fixes is
everything downstream of the topic: the retained-config race, and — the one
that actually deleted entities — each daemon's orphan sweep judging the
other's live configs against its own claim set. Ownership there is decided
from the node id alone, because `publisher.ConfigTopic` carries no payload.

**Migration.** Recorded under ADR 0068's six obligations in
`docs/external-clients/ha-unique-id-migration.md`. It moves a `node_id` and
nothing else: no `unique_id`, no `identifiers`, no `default_entity_id`, no
state, command or availability topic — so unlike the slug unification in the
same window, not even a device row is re-created. The pre-scope node ids are
retracted by the orphan sweep through the unscoped entries
`discoveryNodePrefixes` keeps for one release, the same pattern as
`legacyDiscoverySlug`.

## Amendment (2026-09-13) — the scope needed the `unique_id` too, and its retraction is opt-in

The amendment above is correct about the mechanism and wrong about two of its
consequences. Both corrections land in the same unreleased window.

**1. The daemon-level `unique_id`s carry the scope as well.** Moving the node
id separated two daemons on the *broker* and left them unseparated inside
*Home Assistant*. The three daemon-level planes (ADR 0052: alarm, Security &
Safety, add-on self-update) carry no `<central>` segment and declare fixed
literals — `loom_addon_update`, `openccu-loom_alarm_<zone>`,
`loom_security_<key>`. Two daemons therefore wrote two **distinct** config
topics carrying the **same** `unique_id`, which HA's MQTT integration rejects
outright. The semantics moved from "last writer wins, and a restart repoints
the entity at the live daemon" to "first writer wins permanently": strictly
worse than before the scope. `naming.ScopedDaemonUniqueID` prefixes those
three ids with the same `<base-slug>_`, under the same condition — non-default
base only — so a single-daemon installation on the default base is untouched
to the byte, and the re-key (which costs the entity's history, `entity_id`,
area and automation references) is paid by exactly the configurations that
have the collision. The per-device and hub planes are untouched: they are
keyed on the CCU serial or the ISE id, which already differ.

**2. The unscoped retraction is an operator opt-in, not a default.** The
amendment above described the pre-scope node ids as "retracted by the orphan
sweep through the unscoped entries `discoveryNodePrefixes` keeps for one
release", and PR #817 called the resulting hazard "not a regression". Both
were wrong. The unscoped spelling is not merely *similar* to a live sibling's
— it is the identical string, and `publisher.ConfigTopic` carries the node id
and nothing else. A custom-base daemon that claims it therefore retracts a
**default-base** sibling's device, hub, alarm and security configs on every
boot; HA deletes those entities and the device-registry rows that lose their
last entity, and `identifiers` has no migration path. Before the scope both
daemons swept *and* republished into the shared namespace, so the deletion was
immediately undone; afterwards it is one-directional. It is now
`north.mqtt.discovery_retract_unscoped` (expert, default `false`), turned on
for one start when the operator knows no default-base sibling shares the
broker. A one-shot persisted marker was considered and rejected: once is
already unrecoverable.

**3. Known limitation, recorded rather than fixed: the scope and the central
slug share a `_`.** `topic_base: haus` with a CCU named `CCU`, and the default
base with a CCU named `Haus CCU`, both render `haus_ccu_<address>`.
Disambiguating it means changing the separator, which moves every
non-default-base node id a second time one release after the first. The
mitigation is a naming rule — a `topic_base` must not be a prefix of any CCU
name on the broker — documented on `naming.DiscoveryBaseScope`, in
`docs/user/multi-ccu.md` and in the migration note. Revisit only if a node-id
move becomes necessary for another reason.
