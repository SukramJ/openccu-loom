# ADR 0049 — Matter exposes one endpoint per physical device by default

- **Status**: Accepted
- **Date**: 2026-07-10
- **Related**:
  [ADR 0012 — Matter pure-Go implementation](./0012-matter-pure-go-implementation.md)
  (supersedes its `valve.Irrigation` "stays MQTT-only" rows),
  [ADR 0015 — DataPointUsage `Ignored` split](./0015-datapoint-usage-ignored.md)
  (the usage taxonomy this extends),
  By-design catalogue:
  [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md) (`BD-Visibility-CDPStateGroupStatus`)

## Context

A HomeMatic custom entity spans several CCU channels off one physical
device. A `valve.Irrigation` (ELV-SH-WSM), for example, groups a primary
actor channel (`WATER_SWITCH_VIRTUAL_RECEIVER`, its bool `STATE`), two
sibling actor channels (offsets +1/+2, classified `ce_secondary`), a
group-STATE transmitter channel (offset -1, whose bool `STATE` merely
**restates** the primary's on/off), and read-only metering channels
(`WATER_FLOW`, `WATER_VOLUME` — genuine extra sensors, `ce_visible`). The
same shape recurs across `light`, `lock`, `switch`, `cover`, `siren`, ….

Two facts collided:

1. **The primary channel already projects to Matter.** `valve.Irrigation`
   embeds `*generic.Switch`, which implements `MatterEndpointSource` to map
   `STATE` onto OnOff (0x0006) / OnOffPlugInUnit (0x010A). Go method
   promotion carries that projection onto the valve unchanged. ADR 0012 had
   parked the valve as "stays MQTT-only", but the code bridged it — a
   documented gap the chiptool negative suite had to `t.Skip`.

2. **Every constituent channel with a bool `STATE` projects the same way.**
   Left unfiltered, one physical valve materialises three-to-four separate
   on/off endpoints in Apple/Google Home — the primary, its two secondary
   actors, and the redundant group-STATE transmitter — which reads to a user
   as four lamps for one device. Meanwhile a genuine extra sensor on the same
   custom entity (a wall thermostat's HUMIDITY, a contact's STATE) is *also*
   `ce_visible` and SHOULD surface. `ce_visible` alone cannot tell the
   redundant transmitter apart from a real second sensor.

## Decision

**Matter exposes one endpoint per physical device by default: the primary
channel's projection.** Three parts:

1. **Bridge `valve.Irrigation` as OnOff on its primary channel.** This
   supersedes ADR 0012's two `valve.Irrigation` "stays MQTT-only" rows (the
   Out-of-scope table and the Custom-DP mapping table). The valve is a
   first-class OnOff / OnOffPlugInUnit endpoint like any other switch.

2. **New `DataPointUsage` value `ce_state`** (`DataPointUsageCDPState`).
   The materializer marks the `FieldGroupState` constituent (the -1 offset
   group-STATE transmitter) `ce_state` instead of `ce_visible`. HA / MQTT /
   REST treat `ce_state` **identically** to `ce_visible` — it stays a visible
   data point there, no behaviour change — but the Matter projection drops it
   by default, keeping genuine extra `ce_visible` sensors (HUMIDITY, a contact
   STATE). This is the classification that lets the filter distinguish "a
   redundant restatement of the primary" from "a real second measurement".

3. **Expert flag `north.matter.expose_secondary_channels`** (default `false`,
   `cfg:"expert"`). When enabled, the Matter projection *also* surfaces the
   secondary actor channels (`ce_secondary`) and the group-STATE (`ce_state`)
   as their own endpoints, for power users who want per-channel control. The
   default keeps the device presenting as one endpoint.

4. **Align the Matter candidate set with the entity-creation gate.** MQTT
   (raw + HA-Discovery), REST and the SPA already skip entity creation for
   `ignored` and `no_create` data points (service / status / overflow params
   the visibility gate suppresses — `INSTALL_TEST`, `*_STATUS`, `*_OVERFLOW`,
   `PROCESS`, `CONFIG_PENDING`, … — and raw constituents an aggregating parent
   consumes). The Matter eligibility collector previously did *not* apply that
   gate, so every such data point leaked into `GET /api/v1/matter/exposable`
   as an "unmappable" candidate, cluttering the exposure UI with dozens of
   rows an operator can never meaningfully act on. The collector (and the
   endpoint assembler) now drop them: a Matter candidate is a data point that
   would be a standalone entity elsewhere — `data_point`, `event`,
   `ce_primary`, `ce_visible` — never `ignored`, never the `no_create` raw
   constituent of a parent that already projects. A DP an operator later
   un-ignores flips to `data_point` and reappears automatically.

**This is a Matter-only concern.** MQTT (raw + HA-Discovery), REST, and the
Config UI are unaffected — they continue to surface every channel and data
point exactly as before. The filter lives in the Matter eligibility collector
(`internal/north/matter/eligibility`) and endpoint assembler
(`internal/north/matter/endpoint`); both drop `ce_secondary` + `ce_state`
constituents (and whole secondary channels) unless the flag is set.

## Consequences

- **One clean Matter endpoint per device by default.** A valve is one OnOff
  endpoint, not four. Power users opt into the full channel fan-out with a
  single expert flag; the flag is plumbed through
  `bridge.Config` → `endpoint.Config` and `eligibility.CollectCandidates`.
- **`ce_state` has no Python-reference equivalent.** aiohomematic's model has
  no such usage, so the cross-stack model-parity snapshot would flag every
  group-STATE DP as drift. `script/model_snapshot_diff.py` canonicalises
  `ce_state` → `ce_visible` on both sides (`canon_state_usage`), matching the
  behavioural equivalence HA/MQTT/REST already honour. Documented as
  `BD-Visibility-CDPStateGroupStatus` in `notes/parity/by_design.md`.
- **Standing guards.** `TestHideFromMatter`
  (`internal/north/matter/eligibility`) pins the full usage × flag ×
  channel-owns-custom matrix the filter applies; the chiptool
  `TestNegative_UnmappableDeviceClasses` valve cases assert the ELV-SH-WSM
  surfaces a mappable `STATE` candidate on exactly its primary channel (the
  group-STATE transmitter + secondary actors stay hidden while the flag is
  off) and that its `ignored` / `no_create` service params never appear as
  candidates. The old `t.Skip` documenting the ADR-0012 gap is gone.
- **The negative suite reframes the valve.** It is no longer an "unmappable
  device class"; it is a "device whose custom-entity plumbing must not
  multiply endpoints" — the boundary the suite now guards.
