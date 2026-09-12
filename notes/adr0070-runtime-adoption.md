# ADR 0070 runtime adoption — what the other three planes cost

- Status: measurement, not a decision
- Date: 2026-09-12
- Measured against: openccu-loom `main` @ `79bd8362`
  ("fix(mqtt): eight measured defects … (#796)"), and go-hamqtt `v0.26.0`
  (tag at `9fa2be5`, "feat(publisher)!: the rest of the runtime layer")
- Subject: [ADR 0070](../docs/adr/0070-shared-ha-discovery-model-module.md),
  the sentence *"the bridge mechanics (hash-dedup publish, retract, orphan
  sweep, birth sync) move up"* — and the three planes v0.26.0 added
  underneath it

v0.25.0 shipped the discovery half of the runtime layer and this daemon
adopted it: PR #795 replaced `Bridge.declared` / `Bridge.announced` with a
`*hapublisher.Runtime` built in `internal/north/mqtt/discovery_runtime.go`
(110 lines), and `AnnounceOnline` / `AnnounceOffline` now delegate to it.
v0.26.0 adds the other three planes — `publisher.StatePublisher`,
`publisher.CommandRouter`, `publisher.AvailabilityPublisher` — and loom uses
none of them. `go.mod:42` still pins `v0.25.0`, in which
`AvailabilityPublisher` and `state.go` do not exist at all.

This document measures what adopting them costs, before anything moves. It
is the counterpart of
[the move-up inventory](./adr0070-moveup-inventory.md) and follows
its rule: every count below is measured, and where a symbol is called
single-caller or uncovered, the count is shown. The `.claude/worktrees/` copy
is excluded from every count.

Two corrections to the framing this measurement started from, both
load-bearing:

- **v0.26.0 is released.** It is tagged, not unreleased work. Adoption is a
  `go.mod` bump, not a vendoring exercise.
- **The transport adapter already exists.** `newDiscoveryRuntime` builds
  `hagomqtt.Split(b.client, lateSubscriber{b: b})`
  (`internal/north/mqtt/discovery_runtime.go:30`), and that value is a
  `publisher.Transport`. All three new plane types take exactly that
  interface. The plumbing cost of the three planes is therefore **zero** —
  it was paid by #795.

For scale: the discovery adoption uses 8 distinct `publisher` symbols and
calls 9 distinct `Runtime` methods across 13 call sites, on 110 lines of glue
plus 118 lines of test. That is the unit this document's estimates are
calibrated against.

## The gating question: can the command plane be adopted at all?

`CommandRouter.Handle` refuses any pair of overlapping filters with
`ErrAmbiguousRoutes`. The reasoning is in the library and is not negotiable
from here: a broker sends one copy per matching subscription, go-mqtt
re-matches each arriving copy against its whole local filter list, and the
two fan-outs compose, so one message runs N handlers. Reproduced against
Mosquitto 2.1.2 on both protocol versions.

### The thirteen filters, exactly

`CommandSubscriber.Start` (`internal/north/mqtt/command_subscriber.go`,
1416 non-test lines, 985 test lines) makes thirteen `c.sub.Subscribe` calls,
at lines 491, 498, 503, 511, 515, 522, 528, 534, 541, 548, 555, 562, 569.
Segment counts are **below the base**, because `commandParts` strips the base
first and every handler indexes from there.

| # | Filter (after `<base>/`) | Segs | Handler |
| --: | --- | --: | --- |
| 1 | `+/+/+/+/+/+/set` | 7 | `handleDataPoint` (bucket-aware) |
| 2 | `+/+/+/+/+/set` | 6 | `handleDataPoint` (legacy, bucket-less) |
| 3 | `+/hub/sysvars/+/set` | 5 | `handleSysvar` |
| 4 | `+/hub/programs/+/set` | 5 | `handleProgramEnable` |
| 5 | `+/hub/programs/+/trigger` | 5 | `handleProgram` |
| 6 | `+/hub/install_mode/+/set` | 5 | `handleInstallMode` |
| 7 | `+/devices/+/cdps/+/+/invoke` | 7 | `handleCDPInvoke` |
| 8 | `+/+/+/+/custom/+/set/+` | 8 | `handleServiceMethod` |
| 9 | `+/+/+/+/week_profile/set` | 6 | `handleWeekProfile` |
| 10 | `+/+/+/+/combined/+/set` | 7 | `handleCombinedDP` |
| 11 | `+/+/+/+/schedule/+/set` | 7 | `handleScheduleSwitch` |
| 12 | `alarm/+/set` | 3 | `handleAlarmCommand` |
| 13 | `system/addon_update/set` | 3 | `handleAddonUpdateCommand` |

No filter carries `#`, so every overlap needs equal segment counts.

### Which pairs overlap — measured, not reasoned

Measured by a throwaway program calling the real
`publisher.CommandRouter.Handle` (and therefore the real unexported
`filtersOverlap`) against the real thirteen, all 78 pairs:

```
OVERLAP  [1 datapoint bucket-aware] x [10 combined_dp]
         gh/+/+/+/+/+/+/set     ×  gh/+/+/+/+/combined/+/set
OVERLAP  [1 datapoint bucket-aware] x [11 schedule_switch]
         gh/+/+/+/+/+/+/set     ×  gh/+/+/+/+/schedule/+/set
OVERLAP  [2 datapoint legacy]       x [ 9 week_profile]
         gh/+/+/+/+/+/set       ×  gh/+/+/+/+/week_profile/set
-> 78 pairs tested, 3 overlapping
```

Registering all thirteen in loom's own order accepts ten and refuses filters
9, 10 and 11. **Three pairs, and the review's "two collision classes" are
exactly these:**

- **Class A — the legacy bucket-less catch-all (#2) against a 6-segment
  literal-infix filter.** One member today: `week_profile` (#9). This is the
  class `reservedLegacyParamSegments` exists to patch, and its map has
  exactly one entry (`command_subscriber.go:447-449`). Its doc comment states
  the invariant outright: *"Every literal segment used in a seven-level
  command filter MUST be listed here"* — seven counting the base.
- **Class B — the bucket-aware catch-all (#1) against a 7-segment
  literal-bucket filter.** Two members: `combined` (#10) and `schedule`
  (#11). Patched by the bucket allow-list in `handleDataPoint`'s 7-segment
  branch (`command_subscriber.go:854-868`): only `values` and `master` pass,
  and `default:` drops with a debug line. The class-A comment calls this
  *"the same protection for free"*, and it is — but it is a different
  mechanism in a different place, and only one of the two has any test.

Both hand-maintained lists are **complete today**, verified by enumeration:
the only 6-segment filters are #2 and #9, and the only 7-segment filters
ending in `set` are #1, #10 and #11. So loom is **not** double-dispatching
today. The overlap is real and the guards are real; what the library refuses
is the *shape*, not the outcome.

### The overlaps are live in pinned bytes

Running every `*_topic` value out of the 11 discovery goldens past the
thirteen filters with `publisher.MatchFilter`:

| Measurement | Result |
| --- | --: |
| Distinct non-command topics in the goldens | 225 |
| …matching some command filter (`CheckDisjoint`) | **0** |
| Distinct command topics in the goldens | 70 |
| …unroutable | 1 |
| …matched by **two** filters | **11** |

(Distinct *topic strings* across all 173 golden entries, deduplicated —
`publisher.StateTopics`' generous classification, so availability and
`json_attributes_topic` entries are in the 225. The byte-risk table below
counts *literal occurrences per key* instead, which is the number that says
how many goldens a rename breaks.)

The eleven are two `week_profile` shapes (class A) and nine
`combined` / `schedule` shapes (class B). The single "unroutable" is
`openccu-loom/system/addon_update/set`, from the `alternate-base` fixture: the
probe fixes the base at `gh` while filter #13 is built from the configured
base, so it is a probe artefact, not a gap.

The zero in the second row is worth stating plainly, because it is the defect
the library's `CheckDisjoint` doc comment describes as *"the measured
consumer"*: **loom no longer has it.** Program state sits on
`…/hub/programs/<id>/state`, not on the `…/trigger` topic the button
publishes to (`discovery_golden_hub.json`, `program/legacy-single-switch`), and
`TestProgramStatePublishMustNotEchoAsTriggerCommand` pins that. Loom would
pass `CommandRouter.CheckDisjoint` over its whole pinned advertising surface
on the first boot.

### Verdict: adoptable, and the fix does not move a single published topic

**The thirteen filters can be made disjoint without moving any published
topic.** Coalesce filters 9, 10 and 11 into the two data-point handlers and
branch on the segment the filter used to carry — which is what
`Command.Wildcards` is for, and what `reservedLegacyParamSegments` and the
bucket allow-list already compute today. The remaining ten register with zero
refusals, measured:

```
== coalesced set (9/10/11 folded into the two data-point handlers) ==
-> 10 filters registered, 0 refusals
    gh/+/+/+/+/+/+/set      gh/+/hub/install_mode/+/set
    gh/+/+/+/+/+/set        gh/+/hub/programs/+/set
    gh/+/+/+/+/custom/+/set/+   gh/+/hub/programs/+/trigger
    gh/+/devices/+/cdps/+/+/invoke  gh/+/hub/sysvars/+/set
    gh/alarm/+/set          gh/system/addon_update/set
```

Against the same 70 golden command topics the coalesced set reports 0 doubles
and 0 real unroutables. **Nothing on the wire changes.** The `command_topic`
in all 173 golden entries stays byte-identical; only which Go function the
router hands the message to changes, and the discrimination moves from a
*drop* in the wrong handler to a *dispatch* from the right one.

Concretely, the work is:

- `handleDataPoint`'s 6-segment branch: where it today drops
  `parts[4] == "week_profile"`, call `handleWeekProfile`'s body instead.
  Delete `reservedLegacyParamSegments`.
- `handleDataPoint`'s 7-segment branch: where the `default:` arm today drops
  an unknown bucket, add `case "combined":` and `case "schedule":` routing to
  the existing sink calls, keeping `calculated` and unknown buckets dropped.
- Delete three `Subscribe` calls and three error-wrapping blocks.

That is a strictly smaller change than the bundle migration was, and it is
byte-neutral by construction. The three handlers keep their bodies, their
sinks, their nil-sink debug paths and their metrics.

### What the fix breaks in the test suite — and it is instructive

`TestWeekProfileCommandDoesNotAlsoIssueADataPointWrite`
(`command_subscriber_test.go:887`) carries a **vacuity guard asserting the
overlap still exists**:

```go
if matching < 2 {
    t.Fatalf("filters matching %q = %d, want at least 2 — the overlap this test guards is gone", topic, matching)
}
```

Coalescing makes `matching` equal 1, so the test fails at the guard *before*
its assertions — which is the guard working as designed. It must be rewritten
into its successor: "the week-profile topic reaches the week-profile sink
once, and issues no CCU data-point write", with the overlap precondition
removed. That is the single required test change, and it should land in the
same commit.

### Three residual notes on the command plane

- **No Local is not a substitute.** `hagomqtt` compile-asserts
  `publisher.NoLocalSubscriber` and passes `mqtt.WithNoLocal`, so adopting the
  router through the existing `Split` transport gets it for free — on MQTT 5
  only. `north.mqtt.protocol_version: "3.1.1"` is an operator-reachable
  config key (`internal/config/config.go:1568`, honoured at
  `cmd/openccu-loom/daemon_north.go:678-687`), and go-mqtt sets the bit only
  for V50. `CheckDisjoint` stays load-bearing.
- **The router's local fan-out is the whole client's.** Loom subscribes on
  the same client the runtime sweeps on (`lateSubscriber`), and
  `retain_cleanup.go`'s snapshot filter lives under the discovery prefix, so
  it cannot reach the command tree. That holds today; it is an invariant a
  future broad subscription could break.
- **`CommandRouter.Stop` drains, and `Start` rolls back.** Loom's `Start`
  aborts on a failed subscribe and *leaves the partial set live*
  (`command_subscriber.go:491-573`); the library unsubscribes what it already
  registered and closes the router for good if the rollback fails. That is a
  behaviour improvement loom gets for free, and it is the one place the
  library deliberately does not copy loom.

## The state plane

### What loom does today

`internal/north/mqtt` is 51 non-test files / **18 418 lines** and 108 test
files / **31 448 lines**. `bridge.go` alone is **2 352** lines and carries
roughly thirty publish methods. The ones that put state or attributes on the
wire, with their non-test call-site counts:

| Method | Non-test refs | Payload | QoS | Retain |
| --- | --: | --- | --- | --- |
| `PublishSlotState` | 8 | `pload.PerDPState` JSON | `cfg.QoS.State` | yes |
| `PublishCustomDPState` | 4 | `pload.StatePayload` JSON | `cfg.QoS.State` | yes |
| `PublishSlotConfig` | 4 | `pload.ConfigPayload` JSON | `cfg.QoS.State` | yes |
| `PublishState` (legacy mirror + discovery) | 17 | inline map | `cfg.QoS.State` | yes |
| `PublishSysvar` / hub scores / install mode | — | `renderValue` scalar | `cfg.QoS.State` | yes |
| `PublishEvent` / `publishChannelEventLeaf` | — | inline map | QoS 0 | **no** |
| `publishRawRetained` (the mandated funnel) | 8 | caller's bytes | `cfg.QoS.State` | yes |
| `EvictState` | 6 | empty | `cfg.QoS.State` | yes |

`DefaultQoS = {State: QoS0, Commands: QoS1, Discovery: QoS1}`
(`bridge.go:39-43`). `StateConfig.QoS` defaults to 1 and its doc comment
calls loom's QoS 0 default "not a measurement" — correctly: the field is
reachable from config, but the default has never been weighed, and it makes
`StatePublisher.Latency` permanently blind.

**There is no dedup gate on the state path.** The only byte comparison in the
whole non-test package is `bridge.go:1152`, inside `PublishSlotConfig`
(against `b.configCache`). Every slot state, every custom-DP state, every
hub/sysvar/program topic is written unconditionally on every event.

`rawTopics` + `rememberRawTopic` (9 non-test refs) / `forgetRawTopic` (5) is
already the library's `published` index under another name, and
`RetractRawStateForDevice`'s segment-boundary address match — added by #796
after `strings.Contains(topic, "/"+addr+"/")` blanked every BidCos-RF device
when the virtual remote was unpaired — is already
`StatePublisher.EvictPrefix`'s rule, minus the case folding.

`publish_latency.go` is **144** lines with 3 non-test `NewLatencyProbe`
references, and it is the direct ancestor of the library's probe: the
library's `DefaultLatencyWindow = 128` doc comment says so. Both exclude
QoS 0 and both exclude failures. `StatePublisher.Latency()` supersedes the
file wholesale, and `StateLatency` drops loom's window-occupancy reading,
which the library measured as saturating within seconds.

`renderValue` (`bridge.go:2320`, 5 non-test refs), post-#796, is
`publisher.RenderRawValue` case for case — including the `float32` bitSize 32
subtlety and the nil refusal. It is a delete-and-import.

### `Envelope` versus the timestamp — the verdict

`publisher.Envelope` is two keys, `value` and `available`, and the doc comment
names loom as the reason it has no third:

> Here either field would make every payload unique and turn
> `StatePublisher`'s dedup gate into a no-op.

Loom's canonical envelope is `internal/payload/wrapper.go`:

```go
type PerDPState struct {
	Value                 any            `json:"value"`
	Available             bool           `json:"available"`
	ModifiedAt            float64        `json:"modified_at,omitempty"`
	RefreshedAt           float64        `json:"refreshed_at,omitempty"`
	AdditionalInformation map[string]any `json:"additional_information,omitempty"`
}
```

Populated at `internal/central/adapter/eventbridge.go:2006` and `:2153` from
the **event's** timestamp, and — a defect recorded below — with
`ModifiedAt` assigned the same epoch as `RefreshedAt` on every emission. The
legacy mirror is worse for this purpose: `renderStatePayload`
(`bridge.go:2262`) builds `{"value":…,"available":true,"modified_at":
<time.Now().UTC().RFC3339Nano>}`, a wall clock read at publish time.

**Verdict: loom cannot use `Envelope` on its per-datapoint plane, and must
not pretend it can.** Three keys the wire already carries would be silently
dropped: `modified_at`, `refreshed_at` and `additional_information`. Dropping
them is a payload break, and the `value_template` on the other side is pinned
by `tests/contract/discovery_roundtrip_test.go` against `PerDPState`'s shape.

That is not the same as "the state plane cannot be adopted". `Publish(ctx,
topic, []byte)` takes loom's own marshalled `PerDPState`, and everything
except the dedup gate still applies: the eviction index, `EvictPrefix`, the
collision guard on every write path, the latency probe, `Pulse`,
`Republish` / `Reset`. The correct reading is that loom adopts
`StatePublisher` **with the gate inert on the per-DP plane**, and the gate is
worth something on the planes whose payloads do *not* carry a timestamp:

| Plane | Payload | Timestamp? | Gate useful? |
| --- | --- | --- | --- |
| Per-DP slot state | `PerDPState` | yes (2 fields, both move) | **no** |
| Legacy alias mirror | inline map | yes (RFC3339Nano wall clock) | **no** |
| Pulse / event / impulse | inline map + `event_type` | yes | n/a (non-retained) |
| Sysvar, hub scores, install mode | `renderValue` scalar | **no** | **yes** |
| Alarm zone state | bare HA token | **no** | **yes** |
| Security aggregates | JSON state + attrs | **no** | **yes** |
| `bridge/health` | JSON snapshot | supplier-dependent | unmeasured |
| Slot `/config` | `ConfigPayload` | **no** | already gated by hand |

Counted from the other end: loom has **eleven retained state payload shapes,
and exactly two of them carry a field that changes on every emission** —
`PerDPState` and the legacy mirror. Nine would dedup immediately. But those
two are one topic per datapoint per device plus its mirror, which is
essentially the whole per-DP plane. `time.Now()` appears in exactly three
places in the non-test package (`bridge.go:1278`, `:1317`, `:2267`), two of
them pulse renderers where dedup is moot and one of them the legacy mirror.

So the honest framing is: **the gate is a win on the daemon-level and hub
planes and a no-op on the per-DP plane**, and the per-DP plane is where the
message volume is. A consumer wanting the gate there has to choose between
the timestamps and the gate. Nothing in this measurement recommends dropping
the timestamps: `modified_at` is consumed by the SPA and the WS push, and the
`omitempty` on both fields means removing them is not even a schema
simplification — it is a behaviour change on every entity.

Two smaller points while adopting:

- `StateConfig.Encoding` must be set explicitly to match whatever loom's
  `payload_format` resolves to, or the templates and the payloads disagree.
- `Pulse` and `PulseQoS` line up exactly with loom's hardcoded QoS 0 +
  `retain=false` pulse topics, and `StateConfig.PulseQoS` is the knob loom
  does not have today (it hardcodes, so the operator's state QoS is ignored —
  which is the library's stated reason for the second field).

### `topic.Layout.State` on ten of 32 platforms — is loom affected?

No, and for a structural reason worth writing down. The 173 golden entries
cover 17 platforms, and three of them are among the ten that render no
`state_topic`: `button` (16 entries), `notify` (6), `climate` (3). All three
correctly render **without** a `state_topic`, measured.

Loom is unaffected because it never derives a publish topic from a
`topic.Layout`: it publishes to `pload.MQTTTopicSet.State` off
`MQTTAddressable.MQTTTopics`, and for climate that resolves to
`…/1/custom/climate`, which the config *does* reference — as `action_topic`,
`mode_state_topic` and `preset_mode_state_topic`
(`discovery_golden_aggregate.json`, `climate/thermostat`). `button` and
`notify` are write-only and publish no state at all.

The trap becomes live the moment ADR 0070's step D lands and the model starts
returning `topic.Layout` slots instead of finished strings. `ComponentStateTopic`
and `StateTopicFor` exist for exactly that transition and should be adopted
**with** it, not before it.

## The availability plane

### What loom does today

Seven distinct availability-ish topic shapes, all retained:

| # | Topic | Payload | QoS | Producer |
| --: | --- | --- | --- | --- |
| 1 | `<base>/bridge/status` | `online` / `offline` | `cfg.QoS.Discovery` | **`hapublisher.Runtime`** since #795 |
| 2 | `<base>/bridge/health` | JSON | `cfg.QoS.State` | `b.client` |
| 3 | `<base>/<central>/<iface>/<addr>/availability` | `online` / `offline` | QoS 1 | `b.client` (`bridge.go:1237`) |
| 4 | `<base>/<central>/hub/programs/<id>/execute_available` | `online` / `offline` | `cfg.QoS.State` | `b.client` |
| 5 | `<base>/alarm/<zone>/availability` | `online` / `offline` | QoS 1 | `b.client` |
| 6 | `<base>/security/availability` | `online` / `offline` | QoS 1 (#796) | `b.client` |
| 7 | `<base>/<central>/hub/connectivity/<iface>` | **`true` / `false`** | `cfg.QoS.State` | `b.client` |

Shape 1 is already on the shared runtime, and `Bridge.LastWill()`
(`discovery_runtime.go`) makes the CONNECT will checkable against it — pinned
by `cmd/openccu-loom/daemon_north_lwt_test.go` and
`internal/north/mqtt/discovery_runtime_test.go`. That is the
`model.LevelBridge` half, done.

What `AvailabilityPublisher` would take over is shapes 3–6, i.e.
`model.LevelDevice` and the two daemon-level planes. Measured gaps it would
close:

- **Availability topics are not in `rawTopics`.** Only security availability
  was added to the index (by #796). Device and alarm availability must be
  retracted by reconstructing the topic name (`bridge.go:2143`), and
  `retain_cleanup.go` cannot reach them at all — its one `"availability"`
  mention (line 253) is a reserved-segment *exclusion*. The library's `last`
  map is simultaneously the index, the `Topics()` listing, the `Republish`
  worklist and the `Sweep` ownership set.
- **Publish and retract disagree on QoS.** Shapes 3, 5, 6 publish at QoS 1 by
  stated policy (pinned by the new `TestAvailabilityIsAlwaysQoS1` in
  `publisher_overflow_test.go`), but every retraction and shape 4 use
  `cfg.QoS.State`, default QoS 0. `AvailabilityConfig.QoS` is one field for
  both.
- **Dedup lives two packages away.** `EventBridge.markAvailability` /
  `availabilityCache` / `forgetAvailability`
  (`internal/central/adapter/eventbridge.go`) make the device edge
  edge-triggered. It works — #796 tightened its ordering in `onDeviceRemoved`
  and pinned it with a 338-line test — but it is a domain-layer cache of a
  north-layer fact, and it is the exact thing `AvailabilityPublisher`'s gate
  plus `Forget`/`Reset` is.
- **Shape 7 is not availability and should not be adopted as such.** It
  carries `true`/`false`, which is the `LevelSelf` token set, not the
  `online`/`offline` one. `SelfAvailabilityPayload` renders precisely that —
  but the topic is a connectivity *sensor*, so folding it in would be a
  category error. Leave it on the state plane.

### `legacy_alias.go` — does loom still need it?

**No. It is production-dead, and keeping it costs a measurable amount.**

- 58 lines non-test, 323 lines of test. Two types, three functions, no
  publishing of its own.
- Gated by `BridgeConfig.LegacyAlias.Enabled` (`bridge.go:605-606`). There is
  **no config key**: `grep -rn "LegacyAlias" cmd/` returns 0 hits,
  `internal/config/` has none, no YAML mentions it. The field is the Go zero
  value `false` unconditionally, with no operator-reachable way to set it.
- Every enabling site is a `_test.go` (`legacy_alias_test.go:150,197,261,300`
  and `bridge_edge_cases_test.go`, which sets the private field directly).
- Six live uses in `bridge.go`: four publishes, one retraction, one
  prefix match — all `if b.legacy != nil`, all best-effort `_ =`.
- Never announced, deprecated or scheduled: `grep -n -i "legacy alias" CHANGELOG.md`
  → 0 hits. `git log` on the file → two commits, "Initial release" and a
  license chore.
- Its documented migration window is closed. ADR 0006 §Migration describes a
  `LegacyAliasConfig.HubTopics` opt-in as the path off the `hub/` topology —
  that field no longer exists, and the `HubTopicBuilder` its §152 points at
  was already deleted without a note. `docs/mqtt-topic-schema.md`, the public
  contract, does not mention the legacy tree.

What keeping it costs, specifically: it is the **one** loom payload that
reads the wall clock at publish time (`renderStatePayload`), so it is the one
that would defeat a byte gate even if `PerDPState` lost its timestamps; it
puts entries into `rawTopics` that no discovery config references, so the
orphan sweep carries them; and `bridge.go:2019-2024` already records that a
multi-CCU deployment has its two CCUs overwriting each other there. Deleting
it removes 58 + 323 lines and one of the two obstacles to a per-DP byte gate.
The library was right not to port it.

## Byte risk

Home Assistant keys the entity registry on `unique_id` and the device
registry on `identifiers`, with no migration path (ADR 0070's first
amendment). So the question for each plane is not "is it tested" but "would a
wrong byte be caught".

### What is pinned

| Surface | Pinned by | Caught? |
| --- | --- | --- |
| Discovery payloads | **173** exact-body entries across 11 goldens, always-on | yes — any field change |
| `unique_id` / `identifiers` | all 173 entries carry both, plus ~8 literal unit tests | yes, for the 173 shapes |
| State / command / availability **topic strings** | the same goldens, as literals: 165 state-side, 70 command-side, 91 availability sources | yes — a segment rename breaks 11 goldens and two contract doctests |
| Availability payload tokens | 4+ unit tests with literal `"online"`/`"offline"` + `payload_available` in the goldens | yes |
| Availability QoS/retain | `TestAvailabilityIsAlwaysQoS1` (new, #796), 3 shapes + vacuity guard | yes |
| LWT ↔ online marker ↔ declared source | `discovery_runtime_test.go` (new, #795) | yes |
| Float value encoding | `TestRenderValueFloatPrecision` (new, #796), 9 exact-byte cases | yes |
| The 13 command filters, reworded or removed | `command_subscriber_topic_base_test.go` — exact-string `DeliverInbound` map lookup, under two bases | yes |
| Declared-vs-carried, per plane | 5 `*_topics_roundtrip_test.go` via `planeRoundTrip` | relational only, by design |
| Hub state ⇹ command filters | `command_state_disjoint_test.go` (197 lines) | yes, for the hub plane |

### What has no pin at all

This is the more useful half.

1. **No state message is byte-pinned.** The only state-payload coverage is
   key *presence* (`payload_format_test.go`, `internal/payload/wrapper_test.go`
   assert `"value"` and `"available"` exist) plus #796's float-encoding cases.
   Adding, removing or reordering a field of `PerDPState`, or changing any
   non-float value encoding, passes `make test`. The tests that would notice
   — `tests/integration/mqtt_roundtrip_test.go`,
   `tests/e2e/mqtt_binary_sensor_payload_test.go` — are behind
   `//go:build integration` / `e2e` and do not run in `make test`.
   **This is the plane a `StatePublisher` adoption changes, and it is the one
   with no pin.**
2. **Collision class B has zero coverage.** `combined` and `schedule` are
   exercised only through `NoopClient.DeliverInbound(filter, topic, …)`, which
   delivers through **one named filter** and therefore cannot see a double
   dispatch by construction. Only `week_profile` gets the `echoClient`
   treatment. `newEchoClient` has exactly two users
   (`command_subscriber_test.go`, `command_state_disjoint_test.go`).
   Nine of the eleven double-matched golden command topics are class B.
3. **Adding a 14th command filter is invisible.** No test asserts the set or
   the count; `len(filters)` appears only in vacuity guards
   (`command_subscriber_test.go:960`, `command_state_disjoint_test.go:161`,
   `plane_topic_observation_test.go:189`). A new filter that overlaps an
   existing one would reintroduce class A or B silently, and the invariant
   `reservedLegacyParamSegments`' doc comment states is enforced by nothing.
4. **The disjointness sweep covers the hub plane only.** It drives
   `PublishProgram`, `PublishRoleAvailability`, `PublishSysvar` and
   `PublishInstallMode`. Not the per-DP `values`/`master`/`calculated`/`custom`
   plane, not alarm, not security, not `addon_update`, not the legacy mirror.
   A published per-DP topic whose last segment happened to be `set` at 6 or 7
   segments below the base would be a self-inflicted CCU write, and nothing
   would notice.
5. **`published but not declared` is a `t.Logf`, not a failure**
   (`plane_topic_observation_test.go`). A plane writing to a topic no entity
   references produces a log line in a green run. This is loom's local form
   of the `ErrNoComponentStateTopic` hazard.
6. **`tests/contract/wire_snapshots/` is not the MQTT wire.** Despite the
   name, its snapshots record southbound `SetValue`/`PutParamset` calls and
   are diffed against the aiohomematic reference. Zero MQTT content. It is
   the wrong place to look for a state- or command-plane pin.
7. **`tests/integration/broker_snapshot_test.go` calls itself "the real
   regression net" and blanks the key.** Its committed reference
   `testdata/broker_discovery_reference.json` carries `"unique_id": ""` for
   every entity and never captures `identifiers`; its own doc comment says
   state and availability are "not compared". It is `integration`-tagged
   besides.

## Recommended sequencing

The ordering principle is the predecessor's: every step that cannot change a
published byte goes first, so the byte-risky ones arrive alone.

### Step 1 — pin what the later steps will change (no byte risk)

Before anything moves. Four additions, all of them tests:

- **A class-B echo test.** `TestCombinedDPCommandDoesNotAlsoIssueADataPointWrite`
  and the `schedule` twin, through `echoClient`, with the same vacuity guard
  the week-profile test has. Without them, step 2 is a change to an untested
  code path in the one plane that reaches the CCU.
- **A filter-set pin.** Assert the exact sorted set of registered filters, not
  the count. It is what makes a 14th filter visible and what makes step 2's
  deletion of three filters a deliberate, reviewed diff.
- **Byte pins for `PerDPState`.** One golden per payload shape — per-DP
  envelope with and without `additional_information`, sysvar scalar, alarm
  token, security aggregate, pulse — recorded from a real publisher run, the
  way the discovery goldens are. Without them the state plane can be
  reshaped by accident.
- **Widen the disjointness sweep** from the hub plane to the per-DP, alarm,
  security and addon planes. It is the same test with more publish calls, and
  it is the local equivalent of `CommandRouter.CheckDisjoint`.

**Unblocks:** every later step. Step 1 is the only step that is
unconditionally worth doing regardless of whether anything else here is
adopted.

### Step 2 — coalesce the three overlapping filters (byte-neutral, measured)

Fold 9/10/11 into `handleDataPoint`, delete `reservedLegacyParamSegments`,
rewrite the week-profile test's vacuity guard. Thirteen filters become ten.
Measured to change no published byte and no advertised `command_topic`.

**Pinned by:** step 1's filter-set pin and class-B echo tests; the 173
goldens catch any `command_topic` drift.

**Unblocks:** `CommandRouter` adoption, which is otherwise impossible —
`Handle` refuses the current set. This step is the gate.

### Step 3 — adopt `CommandRouter` (no byte risk)

Replace `CommandSubscriber.Start`'s thirteen — now ten — `Subscribe` calls and
`boundedDispatcher` with a `CommandRouter` over the existing
`hagomqtt.Split` transport. Loom gains: No Local on v5, `Start` rollback,
`Stop` draining, `Command.Wildcards` in place of thirteen hand-rolled
positional splits, and `CheckDisjoint` at boot. It keeps its sinks, its
`resolveCentral`, its per-central metrics via `OnUnroutable` and its
`WithLifecycleContext` semantics, which `CommandConfig.Lifecycle` was written
to match.

**Pinned by:** step 1's filter-set pin, the disjointness sweep, the existing
`command_subscriber_topic_base_test.go` (which will need its
`DeliverInbound` calls repointed at the router).

**Unblocks:** `StateConfig.CommandFilters` / `AvailabilityConfig.CommandFilters`
become fillable from `router.Filters()`, which is what makes the state and
availability guards non-empty rather than silently vacuous.

### Step 4 — delete `legacy_alias.go` (byte risk only on a path no operator can reach)

58 + 323 lines, zero production enable sites, no config key, no changelog
entry. Removing it removes the wall-clock payload and the unreferenced
`rawTopics` entries.

**Pinned by:** nothing needs to catch anything — the code is unreachable in
production. `legacy_alias_test.go` goes with it. Follow ADR 0068's process
anyway if the verdict is that an undocumented, unreachable feature still
counts as a contract; this document's reading is that it does not.

**Unblocks:** one of the two obstacles to a per-DP byte gate, and it makes
`renderStatePayload` (3 non-test refs) deletable.

### Step 5 — adopt `StatePublisher` for the daemon-level and hub planes (no byte risk)

Sysvar, hub scores, install mode, alarm state, security aggregates, slot
`/config`. Their payloads carry no per-emission timestamp, so the dedup gate
is live and worth having. Delete `publish_latency.go` (144 lines) in favour
of `Latency()`, and `renderValue` in favour of `RenderRawValue` — the latter
is case-for-case identical after #796.

**Pinned by:** step 1's payload goldens; `TestRenderValueFloatPrecision`
already pins the nine float cases that the `RenderRawValue` swap must
preserve; `TestAvailabilityIsAlwaysQoS1` guards the QoS that
`StateConfig.QoS` defaulting to 1 would otherwise change silently.

**Note the one real trap here:** `StateConfig.QoS` defaults to **1** and
loom's `cfg.QoS.State` defaults to **0**. Set it explicitly from
`cfg.QoS.State` or the whole state plane's delivery guarantee changes on the
first boot after adoption. That is a wire change with no byte difference, and
no test in the repo would see it.

### Step 6 — adopt `AvailabilityPublisher` for shapes 3–6 (byte risk, well guarded)

Device, program-role, alarm and security availability. Move the
`availabilityCache` out of `internal/central/adapter/eventbridge.go` and onto
the publisher's own gate; unify the retraction QoS with the publish QoS.
Leave shape 7 (`hub/connectivity`, `true`/`false`) on the state plane.

**Pinned by:** `TestAvailabilityIsAlwaysQoS1`, the literal `online`/`offline`
unit tests, the 91 availability sources in the goldens, and
`eventbridge_device_availability_test.go` for the edge semantics. This is the
best-guarded of the six steps.

**Unblocks:** availability topics enter an index the sweep can walk, which is
the gap `SweepRequest.Inspect` was added for — a retained `online` on a
removed device currently stands until `RetractRawStateForDevice` happens to
reconstruct its name.

### Steps that are explicitly not on this list

- **Putting the per-DP plane on `Envelope`.** It drops `modified_at`,
  `refreshed_at` and `additional_information` from every datapoint payload, on
  a plane with no byte pin and with the SPA and the WS push reading
  `modified_at`. The dedup gate is not worth that. Publish `PerDPState`
  through `Publish([]byte)` and accept the gate is inert there.
- **Removing `PerDPState`'s timestamps to make the gate work.** Same
  objection, stated as the change it actually is. If it is ever wanted, it is
  an ADR 0068 break in its own right, sequenced alone, after step 1's payload
  goldens exist.
- **Restructuring the command topic tree.** The measurement says it is not
  needed: the three overlaps resolve inside two handler functions. Moving a
  published `command_topic` would break 11 goldens, two contract doctests and
  every operator automation, for nothing.
- **A narrower pair instead of coalescing.** Narrowing #2 or #1 to exclude the
  literal segments means enumerating the literals in the *filter* — which is
  `reservedLegacyParamSegments` promoted to the wire, and MQTT has no
  exclusion wildcard. It cannot be expressed.
- **Adopting `ComponentStateTopic` / `StateTopicFor` now.** They solve a
  problem loom does not have, because loom does not derive publish topics
  from a `topic.Layout`. They belong with ADR 0070 step D, not before it.
- **Folding `bridge/health` into the availability plane.** Its payload is a
  JSON snapshot from `HealthSupplier()`, not an availability token, and
  whether it carries a per-emission field is unmeasured. Measure it first.

## Findings

Defects found while reading. **None were fixed; no Go file in either
repository was modified.**

**F1 — `PerDPState.ModifiedAt` does not mean what it says.** Its doc comment
(`internal/payload/wrapper.go`) reads *"Updated only when the new value
differs from the previous one."* Both population sites assign it the same
epoch as `RefreshedAt` unconditionally:
`internal/central/adapter/eventbridge.go:2006` (`state.RefreshedAt = ts;
state.ModifiedAt = ts`) and `:2153`. There is no last-value comparison on
that path. So a datapoint re-reporting an unchanged value advertises a new
modification time on every poll, every consumer reading `modified_at` to mean
"last change" is wrong, and the field cannot serve the purpose it documents.
This is also the direct reason a byte gate is impossible on this plane — it
would be possible if the comment were true.

**F2 — collision class B is guarded by a `default:` arm and covered by
nothing.** `handleDataPoint`'s bucket allow-list is what stops `combined` and
`schedule` command topics from also being dispatched as CCU parameter writes,
and no test exercises the double delivery. The sibling class A has
`TestWeekProfileCommandDoesNotAlsoIssueADataPointWrite`. The asymmetry is
invisible from either file: the class-A guard's doc comment says class B
"gets the same protection for free", which is true of the mechanism and false
of the coverage.

**F3 — nothing enforces `reservedLegacyParamSegments`' stated invariant.**
Its comment says *"Every literal segment used in a seven-level command filter
MUST be listed here, or that topic is dispatched a second time as a data-point
write to a parameter that does not exist."* No test asserts the
correspondence, and no test would fail if a 14th filter were added with a
literal in position 4. The list is correct today by inspection only.

**F4 — the state-versus-command disjointness sweep covers one plane of six.**
`TestHubStatePublishesAreDisjointFromCommandSubscriptions` drives four hub
publish calls. The per-DP plane — where the 7- and 8-segment catch-all filters
live and where nearly all the traffic is — is not swept, nor are alarm,
security, addon_update or the legacy mirror.

**F5 — availability publishes and availability retractions use different
QoS.** Device (`bridge.go:1237`), alarm (`alarm_publisher.go:713`) and
security (`security_publisher.go:300`) publish at QoS 1 by stated policy,
pinned by `TestAvailabilityIsAlwaysQoS1`. Their retractions
(`bridge.go:2143`, `:2150`, `alarm_publisher.go`) and `PublishRoleAvailability`
(`bridge.go:1394`) use `cfg.QoS.State`, default QoS 0. A lost retraction
leaves a retained `online` standing for a device that no longer exists — the
exact failure the QoS 1 policy comment is about, on the write that matters
most.

**F6 — device and alarm availability topics are absent from `rawTopics`.**
#796 added `rememberRawTopic` to security availability only
(`security_publisher.go`). Device and alarm availability can therefore be
retracted only by reconstructing the topic name, and no sweep path can reach
them: `retain_cleanup.go:253` lists `"availability"` among the segments the
matcher *excludes*. An availability topic orphaned by a config removed while
the daemon was down is unreachable by any code in the repository.

**F7 — three RFC3339 spellings of the same field.** `modified_at` is
`time.RFC3339Nano` in the MQTT legacy mirror (`bridge.go:2262`), a fixed-width
`"2006-01-02T15:04:05.000000000Z07:00"` in the REST/SPA projection
(`internal/payload/state.go:379`, populated at
`internal/model/generic/payload.go:172`, whose own comment names the
divergence), and an unformatted string on the WebSocket push
(`internal/north/rest/ws/payloads.go:70`). And `float64` epoch seconds on the
canonical MQTT plane. Four representations of one concept, one of them
already documented as a known inconsistency.

**F8 — `legacy_alias.go` is unreachable in production and documented as a
live migration path.** No config key exists (`grep -rn "LegacyAlias" cmd/` →
0), so `BridgeConfig.LegacyAlias.Enabled` is permanently `false`. ADR 0006's
Consequences still tell operators they *"must opt-in to LegacyAlias during
the migration window"*, and its §152 points at
`legacy_alias.go::HubTopicBuilder`, which was deleted. Three in-code doc
references also describe mirrors that no longer exist
(`internal/model/naming/pathdata.go:726`, `internal/model/hub/sysvar.go:693`,
`internal/payload/mqtt_addressable.go:53`).

**F9 — `TopicBuilder.HubStatus()` has zero non-test callers.**
`grep -rn "HubStatus()" --include=*.go . | grep -v _test.go` → 0.
`topics.go:316`. Recorded as a count, not a conclusion: the predecessor
document's lesson is that "zero references outside the package" is not "dead",
and this one is zero references outside its own tests.

**F10 — the repository's self-described "real regression net" for discovery
blanks `unique_id`.** `tests/integration/testdata/broker_discovery_reference.json`
carries `"unique_id": ""` for every entity and never captures
`device.identifiers`, because `writeBrokerReference()` projects only
`model_id`, `device_class`, `entity_category`, `enabled_by_default` and
`name`. Its own comment says MQTT-only wire fields are "not compared". The
173 discovery goldens are the actual net; the file that claims the title is
not, and it is `integration`-tagged besides.

**F11 — `published but not declared` is reported as a log line.**
`planeRoundTrip` (`plane_topic_observation_test.go`) fails on a declared topic
nothing carries, and calls `t.Logf` on a carried topic nothing declares. The
second direction is the one that hides a plane writing into a topic no entity
references, which is silent in every other direction too — the broker accepts
it and Home Assistant never reads it.

**F12 — #796's index-and-counter fix was applied to the security plane and
not to the alarm plane.** `dc351cd4` routed security state, event,
availability and retraction through four named `Bridge` publishers so each
records `rememberRawTopic` / `forgetRawTopic` and increments
`messages_sent` / `publish_errors`. The alarm plane still calls
`b.client.Publish` directly from `alarm_publisher.go:702`, `:713`, `:720`
and `:730`, so `<base>/alarm/<zone>/state` and `/availability` enter neither
`rawTopics` nor any counter. Only `RetractAlarmDiscovery` goes through the
runtime (`alarm_publisher.go:690`). It is the same defect, in the same
release, one file over — and it is a second instance of F6.

## A note on where this file lives

`.github/workflows/docs.yml` runs `mkdocs build --strict`, and its own comment
states that a page under `docs/` missing from the nav fails the build, while
*"documents that should not be published belong under `notes/`, which mkdocs
never sees."* This one is at `notes/` by that convention and needs no nav
entry. Its predecessor was first written at `docs/notes/` with a nav entry,
recorded that as its own finding F8, and has since moved here — so the two
now sit side by side, and the link above is a sibling link.

Worth knowing before editing either: `tests/contract/markdown_links_test.go`'s
`TestMarkdownLinksValid` checks every Markdown link in **both** trees and runs
in `make test` and in the pre-commit hook. It caught the stale
`../docs/notes/` link in the first draft of this file, which is the guard
working exactly as `notes/README.md` advertises.
