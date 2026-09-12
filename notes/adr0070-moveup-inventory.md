# ADR 0070 move-up — symbol-level inventory

- Status: measurement, not a decision
- Date: 2026-09-12
- Subject: [ADR 0070](../docs/adr/0070-shared-ha-discovery-model-module.md), the
  sentence *"`internal/payload`, `internal/model/naming`,
  `internal/routingkey` and the bridge mechanics … move up"*

This document measures the three packages ADR 0070 names, before any of them
moves. It is the counterpart of the measurement phase 3 got: the rendering
half of the migration is complete — all twelve discovery planes render through
`go-hamqtt`'s `discovery.RenderComponent` — and the move-up of the three
packages has not started. The publisher runtime (the bridge-mechanics half of
the same sentence) is being built separately and is out of scope here; where
the two halves touch, this document says so and stops.

Every count below is measured. Where a symbol is called unused or
single-caller, the count is shown. The `.claude/worktrees/` copy of the tree is
excluded from every count — it doubles every naive `grep -r` in this
repository.

## What the three packages actually are

| Package | Files | Non-test lines | Test lines | Exported symbols |
| --- | ---: | ---: | ---: | ---: |
| `internal/payload` | 15 | 2 395 | 1 006 | 168 |
| `internal/model/naming` | 4 | 1 209 | 1 885 | 75 |
| `internal/routingkey` | 3 | 407 | 311 | 19 |
| **total** | **22** | **4 011** | **3 202** | **262** |

The "~7 200 lines" figure in circulation is the sum of both columns. The
production code that would move is **4 011 lines**, and 864 of them are one
file, `naming/pathdata.go`. The test-to-code ratio is inverted in `naming`
(1 885 test lines against 1 209 production lines) because `contract_test.go`
alone is 20 600 bytes of pinned naming behaviour — which is the good news in
this document and is revisited under sequencing.

## The classification, in one table

The test applied is ADR 0070's own: not "does the name mention Homematic" but
"would one of the `go-*2mqtt` bridges — no channels, no paramsets, no CCU —
need this, and would it mean the same thing there?" Each call below says
whether the *signature* decided it or the *semantics* did.

| | Homematic-specific | Daemon-agnostic |
| --- | ---: | ---: |
| `internal/payload` | 134 | 34 |
| `internal/model/naming` | 65 | 10 |
| `internal/routingkey` | 16 | 3 |
| **total** | **215** | **47** |

Of the 47 agnostic symbols:

- **22 collapse in place rather than move.** They are already the shared
  thing, re-exported. `payload.Bucket` and `naming.Bucket` are both
  `= hamodel.Bucket` aliases today, with five constant aliases each;
  `naming.TopicSafe` is a one-line delegation to `topic.Safe`;
  `payload.For` / `ForWith` / `Merge` / `Kind` / `Options` /
  `ExtraProperties` wrap or shadow `go-hamqtt/payload`. Moving them up is a
  no-op followed by a delete.
- **9 move cleanly.** `params.go`'s six (`ParamBool`, `ParamFloat64`,
  `ParamInt32`, `ParamString`, `ErrServiceMissingParam`,
  `ErrServiceInvalidParam`), `payload.EpochSeconds`,
  `naming.DiscoveryTopicPrefix`, `naming.DiscoveryConfigTopic`.
- **16 are blocked.** Listed under "What blocks each one" below.

The 215 are not a rounding error to be argued down. 99 of `internal/payload`'s
168 exports — every type in `info.go` (39), `state.go` (39) and
`descriptor.go` (21) — are typed CCU payload structs: `ChannelInfo.ChannelNo`,
`ChannelConfig.ParamsetIn`, `DeviceInfoChannelRow.ParamsetKeys`,
`ClimateInfo.Key` documented as `<address>:<channel>:<parameter>`,
`InterfaceClientInfo`, `InstallModeInfo`, `SysvarInfo`. Not one of them means
anything to a bridge that polls a REST API. They were decided by semantics,
and in most cases by signature too.

## `internal/routingkey` — 407 lines, 19 exports

This is the package the ADR's list is wrong about, and the reason is a
contract the ADR does not mention.

### It is not a Home Assistant contract. It is an aiohomematic one.

`tests/contract/testdata/routing_key/` holds three byte-pinned fixture files —
25 `unique_id` cases, 8 channel cases, 7 hub-slug cases — replayed by
`tests/contract/routing_key_contract_test.go` and independently verified
against the Python reference by `script/routing_key_parity.py`. The hub-slug
fixture states its own authority:

> python-slugify with default settings (dash separator, Unicode
> transliteration, lowercased). A consumer MUST reproduce this exactly

Measured against the two candidate replacements:

| Input | `routingkey.HubSlug` (pinned) | `hamqtt/topic.Slug` | `naming.DiscoverySlug` |
| --- | --- | --- | --- |
| `Außen Temperatur` | `aussen-temperatur` | `aussen_temperatur` | `aussen_temperatur` |
| `Heizung Büro` | `heizung-buro` | `heizung_buero` | `heizung_buero` |
| `ÖlstandTank` | `olstandtank` | `oelstandtank` | `oelstandtank` |
| `Wohnzimmer-Licht:1` | `wohnzimmer-licht-1` | `wohnzimmer-licht_1` | `wohnzimmer-licht_1` |

`HubSlug` separates with `-` and folds `ü` to `u`; both candidates separate
with `_` and expand `ü` to `ue`. They disagree on **every non-trivial case**,
and `HubSlug`'s output is the parameter slot of a published `unique_id`
(`loom_11a0001234_sysvar_nicht-aufgeloste-variable` in
`internal/north/mqtt/testdata/discovery_golden_hub.json`). The disagreement is
not drift to be harmonised: `HubSlug` is correct *because* it matches
`python-slugify`, which is what the Home Assistant drop-in produces on the
other side of the contract.

A shared Home Assistant discovery module has no business carrying a
python-slugify emulation, and `go-hamqtt` could not accept it anyway:
`slug.go` imports `golang.org/x/text/{runes,transform,unicode/norm}`, and
`go-hamqtt`'s `go.mod` has exactly one requirement — `go-ha-catalog` — by
charter.

### The address families are CCU families

`needsCentralPrefix` branches on `INT000`, `CUX`, and the virtual-remote roots
`BidCoS-RF` / `BidCoS-Wir` / `HmIP-RCV-1`. `CanonicalSerial` takes the last ten
characters of a CCU serial. `HubAddress` / `InstallModeAddress` /
`ProgramAddress` / `SysvarAddress` are the CCU hub pseudo-addresses. Every one
of these was decided by semantics — `CanonicalSerial(string) string` is as
generic a signature as exists — and none of them means anything on a bridge.

### Per-symbol

| Symbol | Call | Decided by | Non-package prod files | go-hamqtt counterpart |
| --- | --- | --- | ---: | --- |
| `CanonicalUniqueID` | HM | semantics | 13 | `discovery.UniqueID` — disagrees |
| `CanonicalSerial` | HM | semantics | 4 | none |
| `SerialSuffix` | HM | semantics | 2 | none |
| `HubSlug` | HM | semantics | 3 | `topic.Slug` — disagrees |
| `NeedsCentralScope` | HM | semantics | 2 | none |
| `CalculatedUniqueID` | HM | semantics | 2 | none |
| `EventGroupUniqueID` | HM | semantics | 2 | none |
| `CalculatedFamilyPrefix` | HM | semantics | 3 | none |
| `EventGroupFamilyPrefix` | HM | semantics | **0** | none |
| `HubAddress` | HM | semantics | 1 | none |
| `InstallModeAddress` | HM | semantics | 1 | none |
| `ProgramAddress` | HM | semantics | 1 | none |
| `SysvarAddress` | HM | semantics | 1 | none |
| `PseudoAddresses` | HM | semantics | **0** | none |
| `GenerateUniqueID` | HM | semantics | **0** (2 test) | none |
| `GenerateChannelUniqueID` | HM | semantics | **0** (2 test) | none |
| `EffectiveSlug` | agnostic shape | signature | 1 | none |
| `UniqueSlug` | agnostic shape | signature | 2 | none |
| `ZoneSlugStem` | agnostic shape | signature | 2 | none |

`UniqueSlug` / `EffectiveSlug` / `ZoneSlugStem` are a name-collision rule any
consumer would want — append `-2`, `-3`, fall back to a stem. They are the
only three symbols here whose signature *and* semantics survive the move. Both
functions call `HubSlug`, so neither moves until `HubSlug` does, and `HubSlug`
does not move at all.

### What blocks it

Nothing internal. `uniqueid.go` and `canonical.go` import only `strings` and
`strconv`; `slug.go` adds `golang.org/x/text`. The package is import-clean and
could move tomorrow. It should not: its contract points sideways at
aiohomematic, not upwards at Home Assistant.

## `internal/model/naming` — 1 209 lines, 75 exports

### `pathdata.go` is 864 of the 1 209 lines and is the daemon's topic tree

`PathData` carries `Interface hmtypes.WireInterfaceID`, `Address`,
`ChannelNo int`, `Bucket`, `Kind`, and renders 22 methods of loom's own MQTT
paths — `MQTTChannelDeviceError`, `MQTTCustomDPServiceMethod`,
`MQTTWeekProfileCommand`, `MQTTDeviceUpdateState`. Sixteen more free functions
render the hub tree (`MQTTHubSysvarCommand`, `MQTTHubInstallModeForInterface`,
`MQTTHubProgramTrigger`). Decided by semantics *and* by signature: `hmtypes`
appears seven times in the file, and `NewDataPointPathData` takes a
`hmtypes.WireInterfaceID` whose only purpose is to decide between the
`device/` and `virtdev/` path roots via `hmenum.InterfaceVirtualDevices`.

`go-hamqtt` deliberately has no counterpart. `topic.Layout` is the shared
answer to the same question and it is an *interface*, four methods wide, with
`Default` as a usable-if-you-have-no-opinion implementation. The comment on
`topic.Layout` says why: *"the six consuming projects have six different
schemas for reasons that are theirs to keep."* `PathData` is loom's reason.
It stays.

### `NameData` is the CCU name model

`DeviceName`, `ChannelName`, `ParameterName`, `ChannelPostfix` documented as
`"ch3"`, `TranslatedParameterName` documented as the OCCU label. Nine exports
(the type, seven methods, `EmptyNameData`). `TitleCaseParameter` documents
itself as mirroring *"Python's `str.title().replace("_", " ")` for HM
parameter names"*. `EntityDisplayName` and `ComposedEntityName` take a
`NameData`. All Homematic, decided by semantics.

The nearest `go-hamqtt` shape is `model.Description.Name` plus
`model.Localized` — a finished string and a translation wrapper, not a
four-part composition with a channel postfix. Not a counterpart; a different
layer.

### `DiscoverySlug` is the one real merge candidate, and it disagrees

`naming.DiscoverySlug` and `hamqtt/topic.Slug` answer the same question — turn
a string into the `[A-Za-z0-9_-]` Home Assistant accepts for a node id. Over a
23-case probe, **8 cases diverge**, in two classes:

| Input | `DiscoverySlug` | `topic.Slug` |
| --- | --- | --- |
| `Café` | `caf` | `cafe` |
| `Señor` | `se_or` | `senor` |
| `Garçon` | `gar_on` | `garcon` |
| `Ångström` | `ngstroem` | `angstroem` |
| `Ærø` | `r` | `aeroe` |
| `Søren` | `s_ren` | `soeren` |
| `a__b` | `a__b` | `a_b` |
| `Watchdog:_CCU-Jack` | `watchdog__ccu-jack` | `watchdog_ccu-jack` |

Class one: `topic.Slug` transliterates non-German accented Latin;
`DiscoverySlug` drops it. `Café` and `Caf` collide into one node id today.
Class two: `topic.Slug` collapses a run of separators including a literal
underscore; `DiscoverySlug` collapses only *generated* underscores and passes a
literal `__` through. The second class is the dangerous one because
`Watchdog:_CCU-Jack` is a real CCU name — the `DiscoverySlug` doc comment cites
it as its own motivating example.

This divergence is already known and already written down, in the doc comment
on `naming.TopicSafe`:

> the sibling `DiscoverySlug` is deliberately NOT delegated to `topic.Slug`
> yet … "café" slugs to "caf" here and "cafe" there, so "Café" and "Caf"
> collide today. The shared behaviour is better, but adopting it moves
> published object ids

That comment is correct and the sequencing below keeps its conclusion.

### The dead re-exports

| Symbol | References outside the package |
| --- | ---: |
| `Bucket` (alias) | 0 |
| `BucketUnset` | 0 |
| `BucketCalculated` | 0 |
| `BucketCustom` | 0 |
| `SetPathRoot` | 0 |
| `StatePathRoot` | 0 |
| `VirtDevSetPathRoot` | 0 |
| `VirtDevStatePathRoot` | 0 |
| `SysvarSetPathRoot` | 0 |
| `SysvarStatePathRoot` | 0 |
| `MQTTChannelAggregateState` | 0 |

Eleven of 75 exports have no caller outside their own package. The six path
roots are consumed only by `NewDataPointPathData` and `NewSysvarPathData`
twenty lines below their declaration. `naming.Bucket` and three of its five
constants exist purely as a second alias of `hamodel.Bucket`, which
`internal/payload` also aliases — the ADR's "the two `Bucket` enums become
one" has landed as *one type behind two aliases*, which is one alias too many.

### What blocks it

`pkg/hmenum` (3 794 non-test lines) and `pkg/hmtypes` (980). `pathdata.go`
uses `hmtypes.WireInterfaceID` (six sites), `hmtypes.DataPointKey`, and
`hmenum.InterfaceVirtualDevices`. Those two packages are the Homematic
vocabulary and are exactly what ADR 0070 says stays. The package therefore
cannot move whole under any sequencing; only `DiscoverySlug`,
`DiscoveryTopicPrefix` and `DiscoveryConfigTopic` are even candidates, and
`discovery_slug.go` (88 lines) is the only file with no `hm*` import.

## `internal/payload` — 2 395 lines, 168 exports

### The 99 DTOs are the package

`info.go` (39 exports, 384 lines), `state.go` (39, 395) and `descriptor.go`
(21, 198) are typed CCU payload structs and nothing else. They are the
daemon's wire surface under ADR 0067 and they stay with the daemon. Nothing in
`go-hamqtt` is a counterpart; `go-hamqtt/model` carries `Description`,
`Device`, `Entity`, `State` — the *shape* of an entity, not the *content* of a
CCU channel.

### The generic core has already moved, and left a wrapper behind

`payload.ForWith` delegates to `hapayload.ForWith` and its doc comment
explains exactly what it kept: the `NamingLower` field-name policy (ADR 0067's
`interfaceid`, not `interface_id`) and the `ExtraProperties` merge. That is the
right shape for what remains. But three of the nine exports in `payload.go`
have **zero callers outside the package**:

| Symbol | External references |
| --- | ---: |
| `payload.For` | 0 |
| `payload.Merge` | 0 |
| `payload.KindConfig` | 0 |
| `payload.KindState` | 0 |
| `payload.KindInfo` | 6 |
| `payload.ForWith` | 8 |

`payload.Merge` is worse than dead: it is a second implementation of
`hapayload.Merge` with the same name, the same arity and the same types, and
**opposite aliasing**. Loom's allocates a fresh map and leaves both arguments
untouched; the shared one mutates `dst` in place and returns it. A future
cleanup that deletes the local one and adds an import compiles silently and
changes behaviour at every call site that relied on `a` surviving. Recorded
under findings.

### Two interfaces disagree with `topic.Layout` about who formats a topic

`payload.MQTTAddressable` is `MQTTTopics(base, centralName string)
MQTTTopicSet` — the model returns five finished topic strings.
`hamqtt/topic.Layout` is the opposite arrangement: the model returns a
`model.Slot` coordinate and the *layout* renders it, and the package doc says
that separation is *"what mechanically enforces the rule the whole model rests
on"*. Both are present in the daemon today, and `payload.SlotLayout` — which
implements `hatopic.Layout` over loom's `HADiscoveryTopics` — is the bridge
between them. This is the one place where the near-duplicate is a genuine
architectural disagreement rather than drift, and it is a finding, not a merge
candidate. `MQTTAddressable` / `MQTTRole` / `MQTTRoleAddressable` /
`MQTTTopicSet` (5 exports) are agnostic in shape and blocked on that
disagreement being settled.

### `TopicSlot` is shaped for Homematic and `hamodel.Slot` is shaped not to be

```go
type TopicSlot struct {
    Address   string
    Channel   int      // hamodel.Slot: Channel string
    Bucket    Bucket
    Parameter string   // hamodel.Slot: Path []string
}
```

`hamodel.Slot`'s doc says why, explicitly: *"a Homematic parameter is one
segment on a numbered channel; a Home Connect feature is a dotted name six
segments deep; a UniFi port is named, not numbered. A fixed arity or an
integer channel would fit exactly one of those."* `TopicSlot` is that one.
Decided by signature. It is already being translated at the boundary —
`CustomSlot`, `WireSlot` and `WireSlotOn` in `discovery_entity.go` exist to
turn a `TopicSlot` into a `hamodel.Slot` — so the pattern is settled and the
type stays down here.

### The `hmenum` coupling is narrow but load-bearing

`internal/payload` references exactly seven `hm*` symbols:
`hmenum.CommandPriority` (18 sites), `hmenum.CommandPriorityCritical` (4),
`hmenum.Parameter` (2), `hmenum.ParamsetKey`, `hmenum.ParameterType`,
`hmenum.CommandPriorityLow`, `hmenum.CommandPriorityHigh`, plus
`hmtypes.SplitChannelAddress` (1). Seven symbols is a small surface, but
`CommandPriority` sits in `Source.Invoke`, `ServiceHandler`,
`ServiceRegistry.Invoke` and `CombinedWritable.WriteCombined` — the write half
of the model's whole north-bound contract. A shared module cannot take a CCU
queue-placement enum, and parameterising it away (`Invoke[P any]`, or an
opaque `any`) would trade a compile-time guarantee for a runtime cast at every
custom data point. Decided by signature, and the signature is the point.

### Per-file summary

| File | Lines | Exports | Call |
| --- | ---: | ---: | --- |
| `state.go` | 395 | 39 | HM — CCU state DTOs |
| `info.go` | 384 | 39 | HM — CCU identity DTOs |
| `registry.go` | 259 | 11 | HM by signature (`hmenum.CommandPriority`) |
| `discovery_entity.go` | 247 | 13 | mixed — the `hamodel` boundary adapter, stays |
| `descriptor.go` | 198 | 21 | HM — CCU config DTOs |
| `topology.go` | 140 | 10 | 6 agnostic re-exports, 4 HM |
| `params.go` | 137 | 6 | **agnostic, moves cleanly** |
| `payload.go` | 123 | 9 | agnostic, already shared — collapses |
| `combined.go` | 112 | 5 | 2 agnostic constants, 3 HM by signature |
| `mqtt_addressable.go` | 104 | 5 | agnostic shape, blocked on `topic.Layout` |
| `discovery.go` | 89 | 2 | agnostic, superseded — dies with `UpdateEvent` |
| `source.go` | 80 | 4 | HM by signature |
| `wrapper.go` | 54 | 2 | agnostic shape |
| `localisable_selection.go` | 38 | 2 | HM — "CCU VALUE_LIST tokens" |
| `doc.go` | 35 | 0 | — |

## Is the ADR's three-package list right?

**No, on two of the three, and none of the three moves whole.**

### `internal/routingkey` should not move at all

It is on the list because it produces `unique_id`, and `unique_id` is a Home
Assistant concept. But the package's contract is not with Home Assistant; it is
with `aiohomematic` and the Python HA drop-in, pinned by 40 golden cases and a
parity script that replays them through the reference implementation. Its
address families are CCU families. Its slug rule is python-slugify's, which
disagrees with the shared slug rule on every non-trivial input and is *right*
to. And it imports `golang.org/x/text`, which a zero-dependency module cannot
take.

Moving it up would put a Homematic interop contract inside a module five
bridges import, and would immediately raise the question of which slug rule is
canonical — a question that has a different answer for loom than for any other
consumer. The ADR's first amendment already concluded that loom keeps its
`unique_id`; the package that computes it should keep its home too.

Three symbols (`UniqueSlug`, `EffectiveSlug`, `ZoneSlugStem`) are genuinely
generic and could go up later as a `hamqtt/topic` collision helper, once they
take the slug function as a parameter instead of calling `HubSlug` directly.
That is 30 lines, not a package move.

### `internal/model/naming` splits, and only the small half goes

`discovery_slug.go` (88 lines, one export) is the generic half. The other
1 121 lines are `PathData`, `NameData` and the hub topic tree, which are
loom's topic schema and loom's CCU name model, and which `go-hamqtt`
deliberately declines to own. The split is clean at the file boundary:
`discovery_slug.go` is the only file in the package with no `hm*` import.

But `DiscoverySlug` does not *move* — it is **deleted** in favour of
`topic.Slug`, and that deletion changes published node ids. See sequencing.

### `internal/payload` splits into three, not two

1. **Already up** — `payload.go`'s reflection wrapper. Keeps the
   `NamingLower` policy and the `ExtraProperties` merge, loses `For`, `Merge`,
   `KindConfig`, `KindState`.
2. **Goes up** — `params.go` (137 lines, 6 exports). A JSON-to-Go coercion set
   for service-call bodies, with no `hm*` reference and no counterpart in
   `go-hamqtt` today. Every bridge that accepts a command payload needs it.
3. **Stays** — everything else. The 99 DTOs, the `hmenum.CommandPriority`
   contract, `TopicSlot`, and `discovery_entity.go`'s adapter onto
   `hamodel`/`hatopic`, which is *supposed* to live down here.

### The fourth package the list is missing is a negative

Nothing has to move *with* the three. The two packages that would be dragged —
`pkg/hmenum` (3 794 lines) and `pkg/hmtypes` (980) — are the Homematic
vocabulary and must not. That is the finding: the ADR's list reads as three
package moves, and what it actually describes is **one file move
(`params.go`), one deletion-with-byte-risk (`DiscoverySlug`), and a large
collapse of duplicate re-exports.** The real bulk of the extraction has
already happened, through `RenderComponent`, `hamodel.Bucket`, `topic.Safe`
and `hapayload.ForWith`.

## Recommended sequencing

The ordering principle: every step that cannot change a published byte goes
first, so that the one step that can arrives alone, on a clean tree, with a
migration note.

### Step A — delete the dead exports (no byte risk) — DONE

Remove `routingkey.PseudoAddresses`, `routingkey.EventGroupFamilyPrefix`,
`payload.For`, `payload.Merge`, `payload.KindConfig`, `payload.KindState`,
`payload.ChannelState`, `payload.DRGDaliLightState`,
`naming.MQTTChannelAggregateState`; unexport the six `*PathRoot` constants and
`naming.Bucket` with its three unused constants. Zero callers, so nothing can
change.

**Golden pins:** would not need to catch anything. Compilation is the guard.

**Unblocks:** every later step reads a smaller surface. `payload.Merge` in
particular must go before anyone is tempted to swap in `hapayload.Merge`.

### Step B — collapse the double `Bucket` alias (no byte risk) — DONE

`payload.Bucket` and `naming.Bucket` both alias `hamodel.Bucket`. Point both
packages' callers at `hamodel` and delete both aliases. Type identity makes
this mechanical.

**Golden pins:** catch nothing, because nothing changes — which is the point
of doing it while nothing does. `discovery_golden.json`'s 132 topics pin the
bucket segment (`values`, `master`, `custom`) as rendered text, so a mistake
that reached the topic string *would* be caught.

**Unblocks:** `topology.go` shrinks to `TopicSlot` plus three interfaces,
which makes the `TopicSlot` vs `hamodel.Slot` boundary legible.

### Step C — move `params.go` up (no byte risk)

`go-hamqtt` gains a `payload.Param*` set; loom deletes 137 lines and imports
them back. No published string passes through these functions — they decode
*inbound* service-call bodies.

**Golden pins:** catch nothing, correctly. The discovery goldens are publish
fixtures; the command path has its own pins under
`tests/contract/wire_snapshots/`, which is where a coercion regression would
surface.

**Unblocks:** the first real proof that a non-trivial piece of
`internal/payload` can live upstairs, at a cost of one release tag and one
pin bump.

### Step D — settle `MQTTAddressable` against `topic.Layout` (design, then byte risk)

Not a move. A decision about which of the two arrangements the daemon keeps.
`SlotLayout` already implements `hatopic.Layout`; if the model stops returning
finished topic strings and returns slots instead, `MQTTTopicSet` and
`MQTTRole` go away and the five exports resolve. If it does not, they stay
forever and this document's "blocked" count is permanent.

**Golden pins:** this is where they earn their keep. All 170 discovery topics
and every `state_topic` / `command_topic` / `availability` entry inside the
eleven fixtures are pinned text. A layout change that moved any topic by one
character fails the suite loudly. This step is byte-risky in principle and
well-guarded in practice.

**Unblocks:** nothing downstream in this document, but it is the precondition
for the publisher runtime taking over availability and retract, which is the
sibling half of ADR 0070's sentence.

### Step E — `DiscoverySlug` → `topic.Slug` (byte-risky; needs a migration note)

Last, alone, and only with the ADR 0068 breaking-change process applied.

**What the golden pins catch.** The fixtures exercise 68 distinct node ids
across 11 files, and German umlauts *are* covered: `ccu_kueche_002a5d8989d5c3`
pins `DiscoverySlug("CCU Küche")`, and
`loom_11a0001234_sysvar_nicht-aufgeloste-variable` pins `HubSlug`'s ö-fold. A
naive swap of `HubSlug` for `topic.Slug` fails immediately on that second pin.

**What they do not catch, measured.** Of the eight divergent inputs above,
**zero** appear in any fixture:

- No fixture contains a non-German accented character in an identifier
  position. The only non-ASCII in any of the eleven files sits in `name`,
  `unit_of_measurement`, or a `TopicSafe`-rendered bridge topic
  (`gh/ccu-01/hub/sysvars/Interner_Zähler/state`) — never in a node id, an
  object id or a `unique_id`. So `Café` → `caf` becoming `cafe` ships
  green.
- No fixture contains a literal double underscore in a central name or
  object-id suffix. `Watchdog:_CCU-Jack` is named in `DiscoverySlug`'s own doc
  comment as the motivating case and appears in no `testdata/` file, so the
  `watchdog__ccu-jack` → `watchdog_ccu-jack` change also ships green.
- Every device address in every fixture is plain hex or a known virtual-remote
  root (`bidcos-rf`, `hmip-rcv-1`, `cux2801001`, `int0000001`, `vcu1234567`).
  The address-family branch is well covered; the *character* handling is not.

**Therefore:** before step E, add fixture rows for an accented non-German
name in an identifier position and for a name carrying `__`. Done — three
rows on the hub plane (`sysvar/hazard-accent-twin-a`, `-twin-b`,
`sysvar/hazard-literal-double-underscore`), plus a direct pin of all eight
divergences in `internal/model/naming/discovery_slug_divergence_test.go`. The
original text follows. Without them the suite
is green through a change that silently orphans every entity on any
installation whose CCU is named in French, Spanish, Danish, Swedish or
Norwegian, and on every installation with a CCU name containing a
colon-underscore pair — which the code's own documentation says is a real
shape.

`naming.contract_test.go` (20 600 bytes) is the second guard and a better one
than the goldens for this specific step, because it pins the naming functions
directly rather than through a rendered payload. It should grow the same two
cases.

### Steps that are explicitly not on this list

- Moving `internal/routingkey`. See above.
- Moving `PathData` or `NameData`. They are loom's topic schema and loom's CCU
  name model; `topic.Layout` exists precisely so they do not have to move.
- Re-keying `unique_id`. Withdrawn by ADR 0070's first amendment and unaffected
  by anything here, provided step E stays last and separate.

## Findings

Defects found while reading. None were fixed at the time of measuring; no Go
file was modified then. The italic paragraphs were added afterwards, as steps
A and B landed and as F2 was investigated — each says what changed and what
deliberately did not.

**F1 — `payload.Merge` and `hapayload.Merge` share a name and disagree on
aliasing.** `internal/payload/payload.go:118` allocates a fresh map and leaves
both arguments untouched. `go-hamqtt/payload/payload.go:170` mutates `dst` in
place and returns it. Same name, same arity, same types. `payload.Merge` has
zero external callers, so the bug is latent — but the natural cleanup (delete
the local, import the shared) compiles silently and changes behaviour. Delete
the local one before anyone adds the import.

*Resolved by step A, in one direction only.* `payload.Merge` is deleted, so
the name now resolves to exactly one function — and that function is the
mutating one. The trap is no longer two same-named implementations; it is that
`Merge(a, b)` in this repository mutates `a`, where for as long as loom had
its own it did not. A call site carried over from anywhere that assumed the
fresh-map behaviour is wrong on sight and compiles. Nothing relies on it today
(there were no external callers of either), but the asymmetry is now the
shared module's to document, not loom's to delete.

**F2 — a zone slugs to two different identities depending on the code path.**
`routingkey.ZoneSlugStem`'s doc comment says it exists so *"the two cannot
fall back to different stems and hand one zone two identities."* There is a
third path. `internal/security/subscribe.go:622`'s `zoneSlugFallback` falls
back to `"zone-" + id[:8]`, and assigns the result to `z.Slug` at lines 381,
422 and 494 — the same field `routingkey.EffectiveSlug` reserves against with
the stem `"zone"`. A zone named only with emoji is `zone-abcd1234` on one path
and `zone` on the other, and `securityZoneTopic` builds a retained MQTT topic
from it.

*Investigated, recorded, deliberately not fixed.* Which path wins: the stored
value, eventually, but only for a zone the zone store has a row for.
`refreshZoneSlugs` derives through `UniqueSlug`, persists, and overwrites the
in-memory value; it iterates store rows only, so it never reaches an engine
zone that was not created through the alarm-config REST API — and for such a
zone `zoneSlugFallback` is the only identity there is, permanently. Two
`onAlarm*` handlers (lines 422 and 494) do not even call the refresh first.
There is a second facet: the fallback has no view of a zone's siblings, so two
zones sharing a name share one slug where `UniqueSlug` would give `x` and
`x-2`.

Why it is left standing: `securityZoneEntity` builds the key `zone_<slug>`,
`security_discovery.go:243` turns that into the `unique_id`
`loom_security_zone_<slug>`, and this plane's `ObjectID` returns the
`unique_id` itself, so the slug also seeds `default_entity_id`. Home Assistant
keys its entity registry on `unique_id` and the MQTT integration has no
migration path (ADR 0068). Changing the spelling therefore orphans every zone
entity on any installation currently on the fallback path, with its history,
area and customisations. A safe repair needs three things, not a one-line
change: a survey of the affected population (measurable — it is exactly the
zones absent from the zone store), the store seeding every engine zone so the
fallback stops being reachable at all rather than being made to agree, and the
ADR 0068 process for whatever identities that seeding moves. Harmonising the
two functions without the seeding would move the identity *and* leave the
second path in place, which is the worst of both. Recorded on the function
itself and pinned by
`internal/security/zone_slug_fallback_divergence_test.go`.

**F3 — `Café` and `Caf` collide into one discovery node id.**
`naming.DiscoverySlug` drops non-German accented Latin rather than
transliterating it, so two differently named centrals (or devices) can produce
the same `node_id`. Known and documented on `naming.TopicSafe`; recorded here
because it is a live collision, not only a migration obstacle, and because no
fixture covered it.

*Fixture gap closed.* `sysvar/hazard-accent-twin-a` and `-twin-b` in
`internal/north/mqtt/testdata/discovery_golden_hub.json` are two system
variables named `Café Terrasse` and `Caf Terrasse`; both render to the object
id `caf_terrasse` and therefore to one retained discovery topic. The collision
is now a pinned fact rather than a measured one. The collision itself is still
unfixed — fixing it is step E.

**F4 — `naming.DiscoverySlug` passes a literal `__` through where
`topic.Slug` collapses it.** Second class of the same divergence, and the
riskier one: `Watchdog:_CCU-Jack` is cited by `DiscoverySlug`'s own doc
comment as a real CCU name, and it slugs differently under the two functions.

*Fixture gap closed.* `sysvar/hazard-literal-double-underscore` pins
`watchdog__ccu-jack` on the hub plane, and
`internal/model/naming/discovery_slug_divergence_test.go` pins all eight
divergences of both classes directly against `topic.Slug`, in both directions,
plus the four German cases where the two already agree. Step E now fails
loudly instead of shipping green.

**F5 — seven exports have no reference anywhere in the repository.**
`routingkey.PseudoAddresses` (its doc says it exists "for the schema
exporter", but `script/export_schemas.go:295` enumerates the four constants
individually instead), `routingkey.EventGroupFamilyPrefix`, `payload.For`,
`payload.Merge`, `payload.ChannelState`, `payload.DRGDaliLightState`,
`naming.MQTTChannelAggregateState`. *Six of the seven confirmed and deleted in step A; the seventh was
miscounted.* `routingkey.EventGroupFamilyPrefix` has no reference outside its
own package, which is what the table above measured, but
`canonical.go:115` builds `EventGroupUniqueID` out of it. It is unexported
rather than removed. `payload.DRGDaliLightState` is an orphan:
`light.DRGDaliLight` exists and is exercised by tests, but it embeds
`ColorTempLight` and returns a `ColorTempLightState`, so the DTO declared for
it is never constructed.

**F6 — `routingkey.GenerateUniqueID` and `GenerateChannelUniqueID` are
exported for tests only.** Zero production callers; the production path always
goes through `CanonicalUniqueID`. Their two callers are
`tests/contract/routing_key_contract_test.go` (which must keep reaching them,
since the golden fixtures pin the unnamespaced key) and one test in
`internal/north/mqtt`. Not a defect to fix — the contract test is the reason —
but worth naming so a future "unused export" sweep does not remove them.

**F7 — eleven `naming` exports are package-private in effect.** The six
`*PathRoot` constants are used only twenty lines below their own declaration;
`naming.Bucket` and three of its constants have no caller at all. ADR 0070's
consequence *"the two `Bucket` enums become one"* has landed as one type
behind two aliases in two packages, which is the shape the ADR was trying to
remove.

*Resolved by step B, as far as it can go.* `naming.Bucket` and its five
constant aliases are deleted and the six `*PathRoot` constants are unexported.
`payload.Bucket` stays as the daemon's single alias: it has 89 references
across 28 files, two of which were off limits during this change, and the
`hamodel.Bucket` type identity means the alias costs nothing but a name.

**F8 — publishing this document under `docs/` requires a nav entry.**
`.github/workflows/docs.yml` runs `mkdocs build --strict` and its own comment
states that *"a page under `docs/` that is missing from the nav … fails the
build here … Documents that should not be published belong under `notes/`,
which mkdocs never sees."* This file was first written at `docs/notes/` with a
nav entry added accordingly, but by the repository's own convention a working
measurement document of this kind belongs under `notes/`.

*Resolved.* The file is at `notes/adr0070-moveup-inventory.md` and the nav
entry is gone. `notes/` is the consistent choice of the two: it is what the
workflow comment prescribes, it is where every other working document in this
repository lives, and it means a later edit to this file can never fail
`mkdocs build --strict`.
