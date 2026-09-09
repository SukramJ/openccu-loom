# A Shared Data Model and Home Assistant Discovery Layer for the `go-*2mqtt` Family

**Status:** accepted — [ADR 0070](../../docs/adr/0070-shared-ha-discovery-model-module.md)
**Date:** 2026-09-09
**Scope:** `go-mtec2mqtt`, `go-zendure2mqtt`, `go-homeconnect2mqtt`, `go-daikin2mqtt`, `go-unifi2mqtt`, `openccu-loom`
**Reference implementation:** `openccu-loom` — in case of doubt, loom wins.

---

## 1. Summary

Six projects publish device data to MQTT and announce it to Home Assistant.
Each carries its own copy of the same layer. The copies have drifted, and the
drift now contains bugs that no one chose.

This concept proposes two new modules and four tools:

- **`go-hamqtt`** — the shared data model, the Home Assistant discovery
  bundle, and a publisher runtime on top of `go-mqtt`. Hand-written, SemVer,
  zero dependencies.
- **`go-ha-catalog`** — the Home Assistant vocabulary (device classes, state
  classes, units, per-platform discovery schemas, abbreviations), **generated
  from the Home Assistant core checkout**. CalVer snapshot, zero dependencies,
  a data artifact rather than a framework.
- **`hacheck`, `hagen`, `hadiff`, `hadoctor`** — validation, catalog codegen,
  release-drift diffing, and live broker inspection.

Decisions taken up front, and assumed throughout:

| Decision | Choice |
|---|---|
| Module split | Two modules: model and catalog, released independently |
| Scope of the shared layer | Model **and** discovery **and** publisher runtime |
| Discovery style | **Device-based only** (HA ≥ 2024.11); no per-entity legacy path |
| Bridge device catalogs | Declarative YAML/JSON with schema validation and codegen |
| Migration | **Clean break**, breaking release; no compatibility mode |
| Catalog extraction | Two-stage: HA's own generated JSON, plus a Python introspection pass |
| loom's role | Architectural source **and** first full consumer |
| MQTT bootstrap | Split: transport ergonomics into `go-mqtt`, HA policy into `go-hamqtt` |

The load-bearing choice is the sequencing: **openccu-loom migrates first**,
before any bridge. It is both the architectural source and the hardest
consumer. If the extracted model cannot carry loom, the design is wrong — and
learning that costs one repository instead of six.
## 2. Where we are today

### 2.1 The duplication is larger than "discovery payloads"

Six implementations of the same idea exist side by side:

| Repo | Package | LOC (excl. tests) | Payload style |
|---|---|---|---|
| go-zendure2mqtt | `internal/hass` | 375 | `map[string]any` |
| go-mtec2mqtt | `internal/hass` | 591 | `map[string]any` |
| go-homeconnect2mqtt | `internal/hass` | 716 | `map[string]any` |
| go-daikin2mqtt | `internal/hass` | 846 | typed structs |
| go-unifi2mqtt | `internal/hass` | 1537 | typed structs |
| openccu-loom | `internal/north/mqtt` | 15756 | `payload` pkg + rule table |

But the shared surface is not just payload assembly. These are duplicated in
four or five near-identical copies each:

1. **MQTT bootstrap.** `NewTCPClient` → `NewLifecycle` → `OnConnect`=birth →
   `NewBreaker` → publish-through-breaker / subscribe-past-it. The comment
   block above `NewBreaker` is *word for word* identical in mtec, zendure,
   homeconnect and daikin. So is the glue struct:
   ```go
   type mqttSession struct { *mqtt.Breaker; mqtt.Subscriber }
   var _ mqtt.Client = (*mqttSession)(nil)
   ```
2. **Orphan reconcile.** Subscribe to the config tree → collect for 2 s (5 s in
   unifi) → unsubscribe → publish an empty retained payload for every retained
   topic that is ours but no longer published.
3. **`IsOwnConfig` / `ConfigFilter`**, four of them decoding the same anonymous
   struct of `unique_id` + `state_topic`.
4. **`slugify` / `collapseTokens` / `umlautReplacer`.** Four copies, and the
   comment *"transliterates German umlauts to match HA's slugify"* appears
   verbatim in all four.
5. **The `object_id` / `default_entity_id` policy**, including a ~10-line
   rationale comment citing `home-assistant/core#157241`, in all five.
6. **`LocalizedName(lang)` / `CodeForLabel(label)`** — `de` and non-empty wins,
   else English; the reverse lookup matches both languages.
7. **The catalog loader frame** — `LoadFile` → `Load(io.Reader)` → decode →
   validate → aggregate into `ValidationError{Issues []string}` → build
   `byTopic` / `byProperty` index maps.
8. **The config loader** — YAML into `map[string]any`, ENV overrides with a
   project prefix, round-trip back through yaml.v3 into the typed struct,
   defaults, aggregated validation. Even the field names are identical; only
   the prefixes differ (`MTEC_`, `ZENDURE_`, `HC2M_`, `DAIKIN_`, `UNIFI_`).

### 2.2 The drift is not a matter of taste — it contains real bugs

Nobody decided these differences. They are copy-and-edit artefacts:

| Aspect | mtec | zendure | homeconnect | daikin | unifi |
|---|---|---|---|---|---|
| Discovery topic levels | 4 | 4 | **5** | 4 | **5** |
| Availability in payload | **absent** | bridge | per device | bridge | 2-level list |
| Bridge status topic | `<hass_base>/status/lwt` | `<root>/bridge/status` | `<root>/status` | `<root>/bridge/status` | `<root>/bridge/status` |
| State retain | **false** | true | configurable | true | true |
| Discovery QoS | 0 | 0 | configurable | 0 | **1** |
| Umlaut transliteration | **no** | yes | yes | yes | yes |
| `entity_category` | no | no | yes | yes | yes |
| Publish dedup | no | no | no | no | yes |
| `origin` block | **no** | **no** | **no** | **no** | **no** |

The consequences, concretely:

- **mtec publishes state non-retained.** After a Home Assistant restart every
  entity sits at `unknown` until the next poll, although the broker could have
  served the value.
- **mtec's availability topic is `<hass_base>/status/lwt`** — inside Home
  Assistant's *own* birth topic tree, and referenced by no entity at all. Wrong
  place and inert.
- **homeconnect's daemon LWT is referenced by no entity either**; entities hang
  off `<root>/<device>/availability`, which only the device manager writes. A
  hard daemon crash leaves a retained `online` standing.
- **mtec's `slugify` is the only one without umlaut transliteration** —
  "Größe" becomes `gr_e` instead of `groesse`, diverging from HA's own slugify
  and from its four siblings.
- **zendure namespaces `unique_id` with the configurable MQTT root**
  (`<root>_<sn>_<topic>`). An operator who changes `MQTT_TOPIC` orphans every
  entity — and `IsOwnConfig` no longer recognises the old ones as its own, so
  the sweep cannot clean them up either.
- **The entity-id seed formula diverges four ways**: mtec seeds from the
  English register *name* (alone in this), zendure and daikin from
  `deviceName + "_" + topic`, unifi from `deviceName + "_" + key`,
  homeconnect from `device + "_" + featureKey`.
- **unifi's Locate control** declares `DefaultEntityID: "button." + seed` but
  publishes under `switch`, and its declared state topic is never published.
- **Not one of the six sets the `origin` block**, and not one uses device-based
  discovery. Five identical instances of the same backlog.

### 2.3 What openccu-loom gets right

loom is the reference — in case of doubt it wins. Its strength is
architectural, not cosmetic, and it is captured in ADR 0011 as *"declarative
model, dumb bridge"*: the model owns semantics, the bridge owns topics, JSON
and retain policy. Four ideas carry over wholesale:

1. **Payload partitioning by struct tag.** `payload:"info"` / `"config"` /
   `"state"` with an `alt=` rename, harvested through cached reflection. One
   declaration replaces three hand-written DTOs per type.
2. **`HADiscoveryContext`.** The model never builds a topic; it asks the
   context. This single indirection is what makes the model transport-agnostic
   and testable.
3. **Optional capability interfaces** instead of configuration flags — not
   implementing one *is* the opt-out. Adding a capability is additive by
   construction, which matters when six consumers pin the module.
4. **The priority-rule catalog format** — AND-criteria plus an integer
   priority, pointer fields for "unset" — which resolves 147 rules
   deterministically and is pinned by a golden file rather than regenerated.

Also worth carrying: the three-source availability pattern, the `haDeviceFields`
whitelist, `via_device` hierarchy, `default_entity_id` as a
language-independent entity-id seed, hash-dedup publishing, the orphan sweep,
and `homeassistant/status` birth sync.

### 2.4 Where loom itself must improve

Being the reference does not make it finished:

- Discovery bodies are `map[string]any` — no compile-time checking, unstable
  key order, and `maps.Copy(body, base)` silently overwrites model keys.
  Ironically `go-unifi2mqtt` already has the typed version; that is the one
  place where a bridge outranks loom.
- **Three overlapping entity-description types** (`EntityDescription` with ~240
  legacy entries across 21 maps, `MqttEntityDescription`, `HARegistryDescription`
  with 147 rules), with a converter between two of them and a resolution chain
  that applies both table systems in sequence.
- **Two nearly identical discovery interfaces with opposite precedence** —
  `HADiscoveryPayloadBuilder` (frame wins) vs. `CombinedProjection`
  (projection wins). A genuine inconsistency.
- **Two `Bucket` enums** in different packages, identical strings, kept in sync
  by comment and bridged by a cast.
- **No validation before publish.** `DiscoveryConfigTopic` documents that empty
  inputs produce a malformed topic and that the caller must validate — but
  there is no validator. `body["availability"].([]map[string]string)` is
  exactly the fragility untyped bodies produce.
- `homeassistant/` is hardcoded, though HA allows a configurable prefix.
- A documented live bug: `sensorMetadataByUnit` is keyed on *canonical* units
  while `resolveSensorStateClass` passes the *raw* unit — 542 occurrences of
  raw `100%` against 36 of `%`, so no `state_class`, so no long-term
  statistics in HA. Blocked on an import cycle that the extraction dissolves.
## 3. `go-hamqtt` — the shared model

The name pairs with `go-mqtt` and says what it is: Home Assistant over MQTT.
`go-hamodel` would undersell the runtime half.

### 3.1 Package layout

```
go-hamqtt/
  model/       Device, Identity, Entity, Slot, Binding, State, Description,
               Localized/Enum, EntitySource, Enricher, Commander, Suppressor
  payload/     struct-tag partitioning: For(obj, Kind), Extra
  topic/       Layout, Join, Slug, Safe
  discovery/   Bundle, Component, DeviceInfo, AvailabilityEntry, Origin,
               Context, Builder, Dynamic, Render, Validate
  catalog/     Rule/Match/Overlay, Rules (an Enricher), Static (an EntitySource),
               loader + codegen
  hamqtt/      Bridge — the runtime
```

Dependencies point strictly downward:
`hamqtt → {discovery, catalog, topic, payload} → model → go-ha-catalog`.
**Nothing below `hamqtt` imports `go-mqtt`.** Everything except the root
package is pure values and pure functions — that is what makes the bridges
"dumb" in loom's sense and what makes the model testable without a broker.

Why each boundary:

- **`model`** — the semantic layer every consumer talks to. Usable without
  knowing that Home Assistant or MQTT exist, which is what lets loom keep its
  own non-HA MQTT surface on the same types.
- **`payload`** — a reflection utility with zero domain knowledge, separate so
  loom can use it for its own info/config/state topics.
- **`topic`** — the *only* place that turns coordinates into strings. Separating
  it from `discovery` is what mechanically enforces "the model never builds
  topics".
- **`discovery`** — sole owner of Home Assistant JSON key names. Capability
  interfaces it type-asserts live here, per Go convention.
- **`catalog`** — one `EntitySource`/`Enricher` implementation among several, so
  a YAML catalog never becomes the *only* way to obtain entities.
- **`hamqtt`** — the only package with I/O, goroutines and `go-mqtt`.

### 3.2 Core types

Identity replaces the five different notions of "which device is this":

```go
type Identity struct {
	IDs         []Identifier // ordered; IDs[0] is primary, yields UID() and node_id
	Connections []Connection // HA "connections", e.g. {"mac", "aa:bb:.."}
}
type Identifier struct{ Namespace, Value string } // "serial", "homeconnect:haId", "unifi:mac"

func (id Identity) UID() string           // "<ns>:<value>" of IDs[0] — stable registry key
func (id Identity) Equal(o Identity) bool // any shared Identifier ⇒ same device
```

`Equal` is what solves daikin's shared outdoor unit: two indoor units register
the same outdoor `Identity`, and the runtime merges rather than publishing it
twice.

A datapoint is a coordinate, never a topic string:

```go
type Slot struct {
	Address string   // == Identity.UID() of the owning device
	Channel string   // "" at device level
	Bucket  Bucket   // Values | Master | Calculated | Custom
	Path    []string // ["temperature"] or ["BSH","Common","Setting","PowerState"]
}
```

`Path` is variadic and `Channel` is a string — that is the deliberate departure
from loom's `Parameter string` + `Channel int`, which is Homematic-shaped and
does not fit homeconnect's dotted feature names or unifi's non-numeric ports.

State carries provenance, which multi-source bridges need:

```go
type State struct {
	Value       any
	Available   bool
	Origin      Origin    // "cloud", "local", "" = unspecified
	ModifiedAt  time.Time
	RefreshedAt time.Time
	Extra       map[string]any
}

type OriginPolicy interface{ Accept(cur, next State) bool }
func Precedence(highestFirst ...Origin) OriginPolicy // daikin: Precedence("local", "cloud")
```

`time.Time` rather than loom's `float64`: encoding to unix-float JSON is the
runtime's job, not the model's.

**One** entity description type, replacing loom's three:

```go
type Description struct {
	Name           Localized
	DeviceClass    hacatalog.DeviceClass
	StateClass     hacatalog.StateClass
	Unit           hacatalog.Unit
	Icon           string
	Category       hacatalog.EntityCategory
	Enabled        bool
	Options        *Enum   // select / enum sensors, with i18n round-trip
	Precision      *int
	Min, Max, Step *float64
	Availability   Availability
	Extra          map[string]any
}
```

Availability is a list with a mode, never a string:

```go
type Availability struct {
	Levels []AvailabilityLevel // Bridge | Device | Parent | Self
	Mode   AvailabilityMode    // all | any | latest
}
```

The zero value means `[Bridge, Device]` with mode `all`. unifi's case — a
connectivity sensor that must stay reachable in order to *report* the device
offline — is `Availability{Levels: []AvailabilityLevel{LevelBridge}}`. Expressed
by omission, not by an opt-out flag.

Localisation is data, not a method, so catalogs and codegen can carry it:

```go
type Localized struct {
	Default string
	Lang    map[string]string // "de": "Leistung"
}
type Enum struct {
	Codes  []string             // deterministic order = HA options order
	Labels map[string]Localized
}
func (e *Enum) Code(label string) (string, bool) // reverse lookup across all languages
```

`Entity` is a small interface; `Basic` is its struct form for catalogs and
codegen:

```go
type Entity interface {
	Key() string                  // unique per device → object_id suffix, suppression key
	Platform() hacatalog.Platform
	Desc() *Description           // pointer: the Enricher's target
	Bindings() []Binding
}
type Binding struct {
	Role string // "state", "command", or platform roles: "temperature", "mode", "fan_mode"
	Slot Slot
	Mode BindMode // Read | Write | ReadWrite
}
```

### 3.3 The capability interfaces

Not implementing one *is* the opt-out. Each concern has exactly one interface.

| Interface | Package | Signature | Absent ⇒ |
|---|---|---|---|
| `Commander` | model | `Command(ctx, Command) error` | runtime calls the configured `Writer` with the bound slot |
| `Suppressor` | model | `Suppresses() []string` | nothing suppressed |
| `Deriver` | model | `Derive(map[string]State) (State, bool)` | each binding publishes its own state |
| `OriginPolicy` | model | `Accept(cur, next State) bool` | last write wins |
| `EntitySource` | model | `Entities(ctx, *Device) ([]Entity, error)` | — |
| `Enricher` | model | `Enrich(*Device, Entity) error` | — |
| `Builder` | discovery | `BuildDiscovery(Context, *Component) error` | default projection |
| `Dynamic` | discovery | `DiscoveryTriggers() []Slot` | discovery re-rendered only on Add/Sync/birth |
| `Extra` | payload | `ExtraPayload(Kind) map[string]any` | struct tags only |
| `Layout` | topic | topic construction | `topic.Default{Root}` |

`discovery.Context` is loom's key indirection, carried over unchanged in
spirit — the model asks, it never formats:

```go
type Context interface {
	StateTopic(s model.Slot) string
	CommandTopic(s model.Slot) string
	Availability(e model.Entity) []AvailabilityEntry
	UniqueID(e model.Entity) string
	Language() string
}
```

Dropped from loom, each for a reason: `HAEntity{HAComponent()}` folds into the
required `Platform()`; `Slotted` folds into `Bindings()`; `ServiceRegistry`
folds into `Commander`; `LocalizedName` becomes the `Localized` data type.

**One precedence rule**, replacing loom's two contradictory ones. Stages run in
fixed order and a later stage overwrites an earlier one:

```
Description → Enricher chain (registration order) → default projection → Builder → Component.Extra
```

There is no "frame wins" versus "projection wins". There is only "later wins".

### 3.4 The discovery bundle

Device-based only. One retained document per device at
`<prefix>/device/<node_id>/config`:

```go
type Bundle struct {
	NodeID     string               `json:"-"`
	Device     DeviceInfo           `json:"device"`
	Origin     Origin               `json:"origin"`
	Components map[string]Component `json:"components"` // key = entity Key()
}
```

`Component` types the common keys, carries platform-specific keys in a typed
`Fields` struct generated from `go-ha-catalog`'s per-platform key lists, and
keeps an `Extra map[string]any` escape. Marshal order is common → Fields →
Extra, later winning — the same single rule as §3.3.

```go
func Render(ctx Context, dev *model.Device, es []model.Entity, o Origin) (*Bundle, error)
func Validate(b *Bundle) error
```

`Validate` is not optional: an invalid bundle returns an error and **nothing**
is published for that device. loom publishes unvalidated today, which is how
the micro-sign class of bug reaches production.

### 3.5 Composite entities and suppression — the design's litmus test

daikin's `climate` consumes seven slots and must hide the individual sensors
and selects a catalog also produces for those slots. It needs no special case
in the runtime — three capability interfaces cover it:

```go
func (c *Climate) Bindings() []model.Binding {
	return []model.Binding{
		{Role: "current_temperature", Slot: c.temp,    Mode: model.Read},
		{Role: "temperature",         Slot: c.tempSet, Mode: model.ReadWrite},
		{Role: "mode",                Slot: c.mode,    Mode: model.ReadWrite},
		{Role: "fan_mode",            Slot: c.fan,     Mode: model.ReadWrite},
		{Role: "power",               Slot: c.power,   Mode: model.Read},
	}
}

// Suppressor — the catalog entities these slots would otherwise produce.
func (c *Climate) Suppresses() []string {
	return []string{"temperature", "target_temperature", "mode", "fan_mode", "power"}
}

// Builder — asks the context for every topic, formats none.
func (c *Climate) BuildDiscovery(ctx discovery.Context, comp *discovery.Component) error {
	comp.Fields = discovery.ClimateFields{
		CurrentTemperatureTopic: ctx.StateTopic(c.temp),
		TemperatureStateTopic:   ctx.StateTopic(c.tempSet),
		TemperatureCommandTopic: ctx.CommandTopic(c.tempSet),
		ModeStateTopic:          ctx.StateTopic(c.mode),
		ModeCommandTopic:        ctx.CommandTopic(c.mode),
		Modes:                   []string{"off", "heat", "cool", "auto"},
	}
	return nil
}

// Deriver — "power off ⇒ mode off": several points aggregated into one role state.
func (c *Climate) Derive(states map[string]model.State) (model.State, bool) {
	if p, ok := states["power"]; ok && p.Value == false {
		m := states["mode"]
		m.Value = "off"
		return m, true
	}
	return states["mode"], true
}
```

What `Sync` does with it:

1. Collect entities from every `EntitySource` of the device — the static
   catalog plus an `EntitySourceFunc` returning the composite.
2. Run the enricher chain. A rule with `Suppress: true` removes an entity here,
   so operator opt-out uses the same mechanism as composite suppression.
3. Apply every `Suppressor`. Keys published in a previous bundle get Home
   Assistant's removal form once, then disappear — so switching a device from
   single entities to a composite is a clean migration, not a ghost.
4. **Suppression hides the entity, not the datapoint.** The bound slots keep
   publishing their state topics, because the composite references them.
5. `Derive` runs whenever any bound slot changes; its result is published on the
   role's state topic instead of the raw value.

Synthetic entities — the normal case, not the exception — need no new concept:
a virtual switch over foreign registers is a `Basic` on a `BucketCalculated`
slot plus a `Commander`; a program button is a `button` with a `Write` binding
and a `Commander`; unifi's site health is an ordinary `Device` with
`Identity{IDs: {{"unifi:site", siteID}}}`.

### 3.6 The runtime

```go
func New(client mqtt.Client, origin discovery.Origin, opts ...Option) *Bridge

// Registry — dedup by Identity lives here
func (b *Bridge) Add(ctx context.Context, dev *model.Device, src ...model.EntitySource) error
func (b *Bridge) Remove(ctx context.Context, id model.Identity) error
func (b *Bridge) Sync(ctx context.Context) error

// Data plane
func (b *Bridge) Set(ctx context.Context, s model.Slot, st model.State) error
func (b *Bridge) SetAvailable(ctx context.Context, id model.Identity, up bool) error

// Lifecycle
func (b *Bridge) Run(ctx context.Context) error
func (b *Bridge) OnConnect(ctx context.Context) error
func (b *Bridge) Will() mqtt.Will

// Escape hatch for non-HA topics — loom's own MQTT API, same client, same layout
func (b *Bridge) Handle(filter string, h mqtt.MessageHandler) error
func (b *Bridge) PublishPayload(ctx context.Context, topic string, obj any, k payload.Kind) error
```

How it consumes `go-mqtt`:

- `Bridge` holds a `mqtt.Client` and **never dials**. Reconnection stays the
  consumer's `mqtt.Lifecycle`, whose `OnConnect` calls `bridge.OnConnect`. The
  `Breaker` stays optional and the consumer wraps the publisher before passing
  it in. This preserves `go-mqtt`'s boundary exactly as ADR 0050 draws it.
- `Run` subscribes once per Write binding and to `<prefix>/status`; an `online`
  birth message takes the same path as `OnConnect`.
- Command handlers dispatch onto a per-entity worker rather than running inline.
  `go-mqtt` documents that `MessageHandler` runs synchronously in the read loop
  and that a blocking handler can trip the PINGRESP watchdog — a slow device
  API must not be able to do that.
- `Add` on a known `Identity` merges: first registration's device fields win,
  `Connections` are unioned, entities unioned by key, and a conflicting
  `Platform()` for the same key is `ErrEntityConflict`.

```go
client := mqtt.NewTCPClient(mqtt.TCPConfig{BrokerURL: url, Will: b.Will()})
b := hamqtt.New(client, discovery.Origin{Name: "go-daikin2mqtt", SW: version},
	hamqtt.WithEnrichers(catalog.Defaults, operatorOverlay),
	hamqtt.WithOriginPolicy(model.Precedence("local", "cloud")),
	hamqtt.WithWriter(dev.Write))
lc := mqtt.NewLifecycle(client, mqtt.LifecycleConfig{OnConnect: b.OnConnect})

_ = b.Add(ctx, indoor, catalog.Static, model.EntitySourceFunc(newClimate))
_ = b.Add(ctx, outdoor) // a second indoor unit adds the same outdoor Identity → merged
go lc.Start(ctx)
go b.Run(ctx)
_ = b.Sync(ctx)
_ = b.Set(ctx, tempSlot, model.State{Value: 21.5, Available: true, Origin: "local"})
```

### 3.7 What goes back into `go-mqtt`

Only transport ergonomics, additive, no domain knowledge:

```go
func SplitClient(p Publisher, s Subscriber) Client // replaces 4 copies of mqttSession
func ConnectWithRetry(ctx context.Context, l *Lifecycle, cfg RetryConfig) error
```

The second one exists because `Lifecycle.Start` makes a single connect attempt,
which is why mtec and unifi both wrote a `startMQTT` retry wrapper. Birth, LWT
and availability policy stay out — those are Home Assistant semantics and
belong in `go-hamqtt`.
## 4. `go-ha-catalog` — the generated Home Assistant vocabulary

### 4.1 Why a separate module

The catalog follows Home Assistant's release cadence (monthly, CalVer), the
model follows its own API stability needs (SemVer). Coupling them would force a
model release for every HA update. The ecosystem already runs this exact
pattern successfully: `openccu-data` (Python generator) → `go-openccu-data`
(thin, dependency-free Go artifact with `go:embed` + a `SnapshotVersion`
constant) → `openccu-loom` (builds the lookup semantics itself).

`go-ha-catalog` copies that discipline verbatim, including its charter:

> **A data artifact, not a lookup framework.**

Consumers get tables and constants. Resolution logic — "which device_class
applies to this parameter" — lives in `go-hamodel` or in the bridge.

### 4.2 Two-stage extraction

Stage 0 and Stage 1 differ in cost, not in importance. Stage 0 needs no Python
at all; Stage 1 unlocks the actual validation gold standard.

#### Stage 0 — read HA's own generated artifacts (no Python)

Home Assistant ships pre-generated, machine-readable catalogs whose freshness
is enforced by `hassfest` in HA's own CI. These are the most stable contract
available and can be read by a Go program directly:

| Source | Yields |
|---|---|
| `homeassistant/generated/device_classes.json` | device_class lists for 12 domains |
| `homeassistant/generated/sensor.json` | `device_class_units`, `convertible_units`, `numeric_device_classes`, `state_classes`, `state_class_units` |
| `homeassistant/components/<domain>/icons.json` | device_class → MDI icon, incl. `range` steps and `state` overrides |
| `homeassistant/generated/entity_platforms.py` | the platform enum (trivial StrEnum AST) |

`script/hassfest/device_classes.py` fails HA's CI with *"File
device_classes.json is not up to date"* if these drift — that guarantee is
worth more than any parser we could write.

#### Stage 1 — Python extractor in an HA venv

Everything else needs a real import. This was verified end to end during the
analysis: a `python3 -m venv` (system Python 3.14.4 satisfies HA's
`requires-python >=3.14.2`) plus `pip install -r requirements.txt` and
`paho-mqtt==2.1.0` is enough to import all 32 MQTT platform modules and
introspect their schemas. No running `hass` object, no config entry.

Extracted this way:

- **Enums by import**, never by AST — `{m.name: m.value for m in Enum}`. AST
  parsing breaks on `_DEPRECATED_*` members, on `set(EnumClass)` expressions
  inside dicts, and on `EnumWithDeprecatedMembers` metaclasses.
- `DEVICE_CLASS_STATE_CLASSES` (`sensor/const.py:810`), `number.DEVICE_CLASS_UNITS`,
  `UNITS_PRECISION`, `AMBIGUOUS_UNITS`, `UNIT_CONVERTERS`.
- **The MQTT `DISCOVERY_SCHEMA` of every platform**, by recursive schema
  introspection. This is the single most valuable artifact in the whole
  pipeline: the authoritative list of which JSON keys are legal on which
  platform, with required-ness and defaults.
- `ABBREVIATIONS` / `DEVICE_ABBREVIATIONS` / `ORIGIN_ABBREVIATIONS`
  (`mqtt/abbreviations.py`, ~290 + 11 + 3 entries).

Measured key counts per platform from the verification run — the catalog's
actual size:

```
alarm_control_panel 39   binary_sensor 30   button 27   camera 24
climate 84   cover 52   date 28   datetime 29   device_automation 4
device_tracker 28   event 26   fan 53   humidifier 48   image 27
lawn_mower 32   lock 40   notify 25   number 35   scene 25
select 29   sensor 32   siren 36   switch 33   tag 6   text 32
time 28   update 34   vacuum 37   valve 39   water_heater 45
```

`light` and `infrared` are meta-schemas: light dispatches on
`schema: basic|json|template` into three sub-schemas (74 / 42 / 42 keys),
infrared dispatches on `device_class` into `emitter` (26) / `receiver` (25).

### 4.3 Two traps the generator must handle

Both were hit during verification and both fail *silently*.

**Trap 1 — probatio, not voluptuous.** HA 2026.x replaced voluptuous with
`probatio`. `homeassistant/__init__.py` calls `install_as_voluptuous()` and
aliases `voluptuous` in `sys.modules`. The extractor **must `import
homeassistant` before `import voluptuous`**, otherwise every `isinstance`
check against `vol.Schema` / `vol.All` fails and every platform yields zero
keys — with no error. The APIs are otherwise compatible
(`probatio.schema.Schema.schema`, `All.validators`).

**Trap 2 — deprecated unit constants.** `PERCENTAGE` is no longer a standalone
literal but `UnitOfRatio.PERCENTAGE.value`; `ppm`/`ppb` moved into
`UnitOfRatio`. The old names survive only as `_DEPRECATED_*` objects. Any
extractor that walks module globals must skip every `_DEPRECATED_` name or it
will bake dead constants into the Go catalog.

A third, subtler one is worth generating *around* rather than working around:
`AMBIGUOUS_UNITS` (`sensor/const.py:899`) silently rewrites the legacy micro
sign `µ` (U+00B5) to `μ` (U+03BC) and normalises `VAr` variants. loom already
learned this the hard way and documents it — HA discards an entire discovery
config over the wrong micro sign. The generator should emit the canonical form
so no consumer ever has to know.

### 4.4 What cannot be generated

These are Python control flow, not data, and must be hand-ported into Go with a
source reference comment:

- The cross-field `validate_*` functions wired into each platform's
  `vol.All(...)`: `validate_sensor_state_and_device_class_config`
  (`mqtt/sensor.py:92` — `options` only with `device_class: enum` and never
  together with `state_class`/`unit_of_measurement`; `last_reset_value_template`
  only with `state_class: total`), plus the equivalents in climate, cover, fan,
  humidifier, text, valve, vacuum, device_tracker.
- The `~` topic-base rule (`discovery.py:224`): `~` is substituted only in
  values whose **key ends in `"topic"`**, as prefix or suffix.
- The loose `PRESET_*` / `FAN_*` / `SWING_*` constants in `climate/const.py` —
  no enum, and HA explicitly does not treat them as exhaustive. Emit them as a
  "known, non-exhaustive" list.

Each generated Go symbol carries provenance:

```go
// source: homeassistant/components/mqtt/sensor.py:92 (HA 2026.9)
```

### 4.5 Versioning and release plumbing

```go
// SnapshotVersion is the Home Assistant release this catalog was generated from.
const SnapshotVersion = "2026.9.0"

// SnapshotRef is the exact core checkout, for drift diagnosis.
const SnapshotRef = "2026.9.0b9-31-g76ca483aec0"
```

Two decoupled schemes, exactly as `go-openccu-data` does it: module tags are
SemVer, `SnapshotVersion` is HA's CalVer (which can never be a Go major
version).

### 4.6 Unattended release on every HA release

Home Assistant cannot fire a `repository_dispatch` at us the way `openccu-data`
can, so the trigger is a poll:

1. A `schedule` workflow runs daily (plus `workflow_dispatch`), queries the
   `home-assistant/core` releases API, and compares the latest stable tag
   against the checked-in `SnapshotRef`. Betas are skipped.
2. On a new tag it checks out core, runs Stage 0 and Stage 1, and rebuilds the
   catalog. Every stable release triggers this, patches included — HA ships
   many patches that never touch the vocabulary.
3. `hadiff` compares the new catalog against the released one. **Byte-identical
   ⇒ stop.** No commit, no tag, no release; only that outcome keeps patch
   releases from generating tag noise.
4. Otherwise CI runs, and on green the workflow commits, tags and releases
   without human review. `hadiff`'s classification picks the bump: additive
   ⇒ minor, any removal or tightened constraint ⇒ major.

The safety net is downstream, not upstream. Consumers pin exact versions and
their `dependabot-auto-merge.yml` merges only non-major bumps — so an additive
catalog update propagates on its own, while a removal lands as a major bump
that waits in each consumer's PR queue. That is the right place for the gate: a
dropped constant is a compile error in the consumer, and only there is it
visible who it affects.
## 5. Does it fit all six? A requirements trace

The cross-cutting analysis produced ten genuine, legitimate differences between
the projects — the ones a shared model must absorb rather than flatten. Each
maps to a specific mechanism:

| # | Requirement | Mechanism |
|---|---|---|
| a | Identity varies: serial, API-id + per-component serial, MAC, device name | `Identity` with namespaced, ordered `Identifier`s |
| b | Topic depth varies from 3 to arbitrary (dotted feature names) | `Slot.Path []string`, `topic.Join` variadic |
| c | Hierarchy: flat · sub-devices · **shared** outdoor unit · full network topology | `Device.Via` + `Identity.Equal` merge on `Add` |
| d | Entity set comes from catalog · device profile · hardware expansion | `EntitySource` interface; `catalog.Static` is one implementation |
| e | Enrichment is a layer: catalog defaults, operator override | `Enricher` chain, ordered; `Rules` is itself an `Enricher` |
| f | Multi-source fusion with precedence (cloud vs. local, two APIs) | `State.Origin` + `OriginPolicy` / `Precedence(...)` |
| g | Availability is 1-, 2- or 3-level with per-entity opt-out | `Availability{Levels, Mode}`; opt-out by omission |
| h | Synthetic entities are the normal case | `Basic` + `Commander` on `BucketCalculated`/`BucketCustom` |
| i | Composite entities that suppress their constituents | `Suppressor` + `Deriver` + `discovery.Builder` (§3.5) |
| j | i18n with a reverse lookup for enum selects | `Localized`, `Enum.Code(label)` |

And per domain, the features each family needs:

- **Energy (mtec, zendure)** — `state_class: measurement` versus
  `total_increasing` is the load-bearing distinction, and getting it wrong
  corrupts Home Assistant's long-term statistics irreversibly. `go-ha-catalog`
  supplies `DEVICE_CLASS_STATE_CLASSES`, so `hacheck` can reject an illegal
  combination at build time. Both projects also refuse to publish a derived
  value when an input is missing — that stays bridge logic, correctly.
- **Appliances (homeconnect)** — state machines rather than measurements:
  enum sensors with localised options, `payload_press` that must carry the
  actual write value, `entity_category: diagnostic` and
  `enabled_by_default: false` for the long feature tail. All `Description`
  fields; the profile-derived `EntitySource` supplies the entity set.
- **Climate (daikin)** — the only domain needing a true composite entity, plus
  bidirectional enum mapping and physics-driven aggregation across a shared
  outdoor unit. §3.5 covers the composite; the aggregation stays domain code.
- **Network (unifi)** — `device_tracker` with presence hysteresis, dynamic
  entity sets where the orphan sweep is essential rather than cosmetic, and
  controls gated by *both* operator config and API capability. The gating stays
  in the `EntitySource`, which is exactly where a conditional entity belongs.
- **Homematic (loom)** — the widest platform coverage (17 components), the
  priority rule table, multi-CCU scoping, and its own non-HA MQTT surface,
  which survives through `Bridge.Handle` and `PublishPayload` on the same
  client and layout.

## 6. What deliberately stays per project

The shared layer is not a place to centralise domain knowledge. These stay
where they are:

- **Device catalogs themselves** — mtec's Modbus registers, zendure's property
  list, daikin's characteristics with their `match:` predicates, homeconnect's
  feature mapping, loom's 147 Homematic rules and ~240 legacy entries. The
  *format* is shared; the *content* is not.
- **Protocol clients** — Modbus, ONECTA, the UniFi APIs, Home Connect's session
  handling, CCU XML-RPC.
- **Value semantics that are physics, not presentation** — daikin's aggregation
  across a shared outdoor unit (quiet = OR, demand = min, energy = sum only
  when the counters differ), mtec's refusal to publish an incomplete derived
  value.
- **loom's Homematic layer** — `hmenum`/`hmtypes`, the 21 custom datapoint
  builders, paramset semantics, `DataPointUsage`/`DataPointCategory`, CCU
  translations and device profiles from `go-openccu-data`.
- **`go-mqtt`'s domain freedom.** It gains two additive transport helpers and
  nothing else. Six consumers depend on it staying a pure transport.

One thing that is *not* on this list, deliberately: the config loader. It is
duplicated five times with identical field names and only differing prefixes,
but it is not part of this concept's scope. It is a good candidate for a
follow-up, and it does not belong in a Home Assistant module.
## 7. Tooling beyond the libraries

The two modules are the deliverable; these are what make them safe to use.

### 7.1 `hacheck` — validate a discovery bundle before it reaches the broker

A library function plus a CLI over the same code. It answers the question
nobody can currently answer without a running Home Assistant: *is this payload
actually legal?*

- Every JSON key checked against the platform's extracted `DISCOVERY_SCHEMA`.
- Required keys present; unknown keys reported (HA silently drops them with
  `extra=REMOVE_EXTRA`, which is why they are invisible today).
- `device_class` legal for the platform; `state_class` legal for that
  `device_class`; unit legal for that `device_class`; unit in canonical form.
- The hand-ported cross-field rules (`options` only with `device_class: enum`,
  never with `state_class`/`unit_of_measurement`, …).
- Device-bundle rules: `origin` required, every component carries a
  `unique_id`, `platform` ∈ supported components, at least one device
  identifier.
- Topic syntax, and `unique_id` collision detection across the whole bundle.

Run it in each bridge's test suite over its full catalog. A malformed payload
becomes a failing unit test instead of a missing entity someone notices weeks
later.

### 7.2 `hagen` — catalog codegen

Reads a bridge's declarative device catalog (YAML/JSON, `go:embed`-ed),
validates it against a published JSON Schema, and emits typed Go. What this
buys over loading YAML at runtime: catalog errors surface at build time, the
generated constants make topic keys refactorable, and the schema gives
non-Go contributors a contract for adding devices.

### 7.3 `hadiff` — catalog drift against a new HA release

Diffs two `go-ha-catalog` snapshots and reports what a HA upgrade changed:
device classes added or removed, units moved between classes, new platforms,
newly forbidden combinations. Runs in the regenerate PR so the release notes
write themselves — and so a removed device_class is caught before it ships.

### 7.4 `hadoctor` — live broker inspection

Connects to a real broker, snapshots the retained discovery tree, and reports:
orphans (retained configs nobody owns), duplicate `unique_id`s across bridges,
entities whose availability topic is never published (mtec's and homeconnect's
current bugs, found automatically), and topics that no longer validate against
the current catalog. This is the operational counterpart to `hacheck`.

### 7.5 Golden-file contract tests

loom's discipline, generalised: the JSON shape of a discovery bundle *is* the
contract with Home Assistant. Every consumer pins its full bundle as a golden
file with an `-update` flag. A model change that alters any payload shows up as
a reviewable diff in six repositories rather than as a silent behaviour change.

---

## 8. Rollout

### 8.1 A clean break, deliberately

Harmonising `unique_id` and topic schemas re-creates entities in Home
Assistant: history and automations referencing the old entity ids break. This
is accepted — a compatibility mode would freeze the §2.2 divergences,
including their bugs, permanently. The mitigation is process, not code:

- Each bridge ships the change in a **major release** with a migration note
  naming the old and new `unique_id` formats.
- The orphan sweep retracts the legacy retained configs on first start, so
  users are not left with duplicate ghost entities.
- `hadoctor` gives users a before/after inventory.

### 8.2 Sequence

Order is chosen so each step de-risks the next.

| Phase | Work | Why here |
|---|---|---|
| 0 | `go-ha-catalog`: Stage 0 + Stage 1 extraction, first tag | No dependants yet; pure upside, usable standalone |
| 1 | `go-mqtt`: additive `SplitClient` + connect-retry helper | Small, additive, unblocks every bridge's bootstrap |
| 2 | `go-hamodel`: types, discovery bundle, validation, naming | Design lands while loom is still the reference impl |
| 3 | **openccu-loom migrates** — extract `payload`, `naming`, `routingkey`, bridge mechanics upward, import back | The hardest consumer proves the design *before* it is fanned out |
| 4 | Runtime layer (state publishing, command routing, availability, orphan sweep, birth sync) | Now informed by loom's real requirements |
| 5 | `go-zendure2mqtt` (375 LOC) as pilot | Smallest surface, catalog-driven, single device hierarchy |
| 6 | `go-mtec2mqtt` (591) | Fixes retain/availability/slugify bugs as a side effect |
| 7 | `go-homeconnect2mqtt` (716) | Proves the profile-derived `EntitySource` |
| 8 | `go-daikin2mqtt` (846) | Proves composite entities and multi-source fusion |
| 9 | `go-unifi2mqtt` (1537) | Proves hardware expansion and dynamic entity sets |

Phase 3 is the load-bearing decision. loom is both the architectural source and
the most demanding consumer; migrating it first is what keeps the extraction
honest. If the model cannot carry loom, it is wrong — and finding that out at
phase 3 costs one repository, not six.

Fan-out follows the established ADR 0050 rule: tag the module first, then each
consumer bumps its exact pin in its own squash PR. No `latest`, no lockstep.

### 8.3 Risks

| Risk | Mitigation |
|---|---|
| The model cannot carry loom's 15k-line layer | Phase 3 before any fan-out; abort cost is one repo |
| Device-only discovery excludes older HA installs | Accepted per decision; HA 2024.11 is the floor, documented in the README |
| HA changes the discovery schema | `hadiff` in the regenerate PR; golden files fail loudly |
| Composite entities (climate) do not generalise | Phase 8 is the proof; if it fails, climate stays a daikin-local builder implementing the public interface |
| Zero-dependency rule under pressure (YAML) | `go:embed` JSON as the canonical catalog format; YAML stays a build-time input to `hagen`, never a runtime dependency |
| Six pinned consumers make breaking changes expensive | Capability interfaces are additive by construction; golden files make every shape change visible pre-merge |

---

## 9. Open points

These need a decision before phase 2, but not before phase 0 or 1:

1. **Discovery prefix per bridge or global?** HA allows a configurable prefix.
   Proposal: a `WithDiscoveryPrefix` option defaulting to `homeassistant`, set
   once per bridge.
2. **Envelope or bare value on state topics?** loom publishes a JSON envelope
   (`{value, available, ...}`) and references it with a value template; the
   bridges publish bare values. `WithEncoding(Envelope|Raw)` covers both, but a
   default has to be chosen — proposal: `Envelope`, since it is what carries
   per-datapoint availability, with `Raw` for bridges that do not need it.
3. ~~**Where does the concept document live?**~~ **Settled.** Recorded as
   [ADR 0070](../../docs/adr/0070-shared-ha-discovery-model-module.md) (accepted,
   2026-09-09), citing ADR 0050 (shared MQTT module) and 0053 (CalVer snapshot
   versioning); this document is the full design and lives in `notes/concepts/` per the published/working split.
4. **Does `go-ha-catalog` vendor the extracted data or require a core
   checkout?** Proposal: vendor, following `go-openccu-data` — consumers must
   not need Python or an HA checkout.
