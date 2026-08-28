# ADR 0067 — The north surface is a model API, not an HA entity projection

- Status: accepted
- Date: 2026-08-28

## Context

Two artefacts in this repository answer the same question in opposite
directions, and neither cites the other.

`tests/contract/doc_purity_test.go` fails the build for any comment under
`internal/`, `pkg/` or `cmd/` containing `aiohomematic`, `aiohomematic-config`,
`homematicip_local`, `homematicip-local`, `pydevccu` or `openccu-data`
(:55-140), on the stated grounds that "provenance belongs in the Markdown
docs". The only exemption is a filename prefix: `strings.HasPrefix(base, "ha_")`
(:250-252). That is the sole enforced rule in the repository on the question of
how much Home Assistant the daemon may know.

`internal/north/mqtt/` runs a complete Home Assistant entity projection —
discovery payloads, platform assignment, and `entity_descriptions_generated.go`,
whose 147 rules were generated from `homematicip_local`'s own description
tables. It is measured in `homematicip_local`'s three-way parity harness as an
equal HA layer beside `aiohomematic`. It is not named `ha_*`, so `doc_purity`
scans it in full; the effect is visible in its own header, where the sentences
on lines 6 and 9 break off exactly where the project name stood.

`docs/external-clients/ha-drop-in-identity-and-scoping.md` takes a third
position, and the one the REST clients actually implement: the client owns the
HA registry key, the daemon supplies scoping and raw fields.

The question was never written down. An architecture review of the six coupled
repositories (2026-08-28) had to derive the answer from measurements, because
half its candidate list was undecidable without it. This ADR records the answer
so the next reader does not repeat that work.

## Decision

**The REST/WS north surface stays an HA-agnostic model API.** The Home
Assistant projection lives in Python, in the consumer.

The MQTT plane keeps its entity projection. It is an independent product
surface for a different consumer — a broker, not a Python client — and it is
explicitly **not** a precedent for the REST surface.

Additive fields that describe the *model* remain welcome and are not affected
by this decision: an `operationId` on every route, `firmware` and
`availability` on `DeviceSummary`, a documented capability vocabulary, a named
WS envelope `kind`. The line this ADR draws is at *descriptors* — the fields
that exist only because Home Assistant renders an entity.

## Why

### The contract carries no descriptors, and the one entity that exists is the wrong sample

`device_class`, `state_class`, `entity_category`, `translation_key`,
`enabled_by_default`, `suggested_display_precision` and `platform` each appear
**0×** in the 14 384 lines of `assets/openapi.yaml`.

The single entity-shaped schema, `AlarmPanelEntity` (openapi.yaml:10932-10963),
has 10 fields and none of them is a descriptor. It is also the least
generalisable case available: `alarm_control_panel` is the one platform whose
domain the daemon itself owns and which exists only on the loom backend. There
is no CCU derivation step and no `aiohomematic` reference to reconcile — so it
does not pose the problem the other 16 platforms pose.

### It would produce three projections, not one

The argument for the projection is "one HA projection instead of two". Measured,
it is three:

| Piece | Fate | Size |
| --- | --- | ---: |
| MQTT discovery + descriptions | stays | 7 688 prod-LOC |
| `HADiscoveryPayload` in the model core | stays | 3 270 LOC |
| REST entity emitter | new build | — |
| Python compat shim | 10 439 → 7 171 LOC | 69 % survives |

The MQTT builders cannot be reused: they are topic-bound (43 topic references
in `discovery.go`, 78 in `hub_discovery.go`, 31 in `discovery_aggregate.go`).
And the shim barely shrinks, because its bulk answers a different question than
"which descriptor" — `_protocol_surface.py` (624 LOC) satisfies a protocol
surface, `refresh.py` (463 LOC) bridges two event buses in one process, and
`adapter.py` (1 944 LOC) presents a coordinator.

### A domain change has no migration path

Adopting the daemon's platform assignment would move entities between HA
domains: `WEEK_PROFILE` from `sensor` to `select`
(`internal/north/mqtt/category_component.go:66-75` against
`homematicip_local/sensor.py:167-170,295`), `TEXT_DISPLAY` from `notify` to
`text` (against `const.py:261-262`).

Home Assistant's registry offers `async_migrate_entries` for a changed
`unique_id`. For a changed domain it offers nothing — the old entity is
orphaned, with its history and every automation referencing it.

### The cascade balance is negative

Descriptions change. There were 7 description commits in 89 days. Today each is
a Python patch delivered over HACS. Behind the contract, each becomes an
`APIVersion` bump, a daemon release, and an add-on image update — roughly 21
release events for the same 7 changes.

### Hub singletons would create a third identity namespace

The nearest thing to a ready-made case is the hub singleton set, and it is
further away than it looks. The wire DTOs carry no `unique_id` at all:
`HubCountDataPoint` is `required [legacy_name, value]`, `HubUpdateDataPoint` is
`required [legacy_name, update_available, in_progress]` — no `category`, no
`translation_key` (openapi.yaml:11309-11340).

Worse, for those same entities the daemon already stamps a *different* key in
the MQTT namespace: `hub_discovery.go:1074-1078` takes the numeric `ise_id`
where the Python client takes the slug, and `discovery_combined.go:62,94`
prefixes `openccu-loom_` where the client prefixes `loom_`. Serving entity
identity over REST would put a third namespace beside the two that
`by_design.md` → "BD-Identity-RoutingKeyNamespaces" deliberately keeps apart.

## Consequences

- `doc_purity_test.go` stands as written. Its rule is now backed by a decision
  rather than only by a convention.
- `internal/north/mqtt/entity_descriptions_generated.go` is the one artefact
  that crosses the line, and it is regularised rather than extended — see ADR
  0063. Its generator is referenced by no Makefile target and no workflow, its
  stamp is frozen at `2026-05-02T15:22:37Z`, and 12 rule keys
  (`SECURITY_*`, `DAEMON_CONNECTION`, `DAEMON_LATENCY`, `event_doorbell`) now
  exist only on the Home Assistant side while `entity_helpers/` has taken 9
  commits with +231/−40 lines since. `DO-NOT-EDIT` comes off, the generator
  goes, and the 147 rules become daemon-owned data with a coherence test
  against a named source. Note the constraint this ADR imposes on that work:
  the file is not named `ha_*`, so it is scanned in full and may not document
  its own provenance in Go comments.
- External clients keep owning the HA registry key. `docs/external-clients/`
  remains the contract for that, and this ADR is the reason it is not
  superseded.
- The Python compat shim in `openccu-loom-client` stays where it is. That is a
  consequence of this decision, not an independent one: it exists to answer the
  descriptor and type-identity questions, and this ADR declines to move either
  behind the wire.

## Revisit when

- Descriptor fields enter the contract for a reason of their own — a second,
  non-Home-Assistant consumer that needs entity-shaped output, or an operator
  surface in the SPA that needs them.
- Home Assistant grows a registry migration for a changed domain. That removes
  the sharpest of the four objections and makes the platform-assignment half
  of the question reopenable on its own.

## References

- `tests/contract/doc_purity_test.go:55-140`, `:250-252`
- `internal/north/mqtt/entity_descriptions_generated.go`,
  `category_component.go:66-75`, `hub_discovery.go:1074-1078`,
  `discovery_combined.go:62,94`
- `assets/openapi.yaml:10932-10963` (`AlarmPanelEntity`), `:11309-11340` (hub
  data-point DTOs)
- `docs/external-clients/ha-drop-in-identity-and-scoping.md`
- `notes/parity/by_design.md` → "BD-Identity-RoutingKeyNamespaces"
- ADR 0063 — Self-maintained device profiles
- `homematicip_local/tests/e2e/` — the three-way parity harness
