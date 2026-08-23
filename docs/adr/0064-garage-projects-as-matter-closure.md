# ADR 0064 — A garage drive projects as a Matter Closure, not a WindowCovering

- **Status**: Accepted
- **Date**: 2026-08-22
- **Related**:
  [ADR 0012 — Matter pure-Go implementation](./0012-matter-pure-go-implementation.md)
  (the rich-model / dumb-bridge surface this follows),
  [ADR 0049 — Matter exposes one endpoint per device](./0049-matter-one-endpoint-per-device.md)
  (the endpoint budget this stays within),
  By-design catalogue:
  [`notes/parity/by_design.md`](https://github.com/SukramJ/openccu-loom/blob/main/notes/parity/by_design.md)
  (`BD-Matter-GarageIsClosureNotWindowCovering`,
  `BD-Matter-ClosureWithoutTagList`)

## Context

A Hörmann-style garage drive (`HmIP-MOD-HO`, `HmIP-MOD-TM`) has three
physical resting positions, not a continuum. It reports them on
`DOOR_STATE` as `CLOSED`, `VENTILATION_POSITION` and `OPEN`, and accepts
`CLOSE`, `PARTIAL_OPEN` and `OPEN` on the separate `DOOR_COMMAND`
parameter. While travelling it reports `POSITION_UNKNOWN` — not a
position, but the absence of one.

The drive projected onto Matter's WindowCovering cluster (0x0102), whose
only axis is a lift percentage. The three stops were encoded as 0, 5000
and 10000 percent100ths. That mapping is a faithful reading of the axis
WindowCovering offers, and it loses the property that matters:

1. **The ventilation stop has no name.** A controller renders a lift
   percentage as a slider. Nothing tells a user that 50 % is a distinct
   position the drive can hold, so reaching it means dragging a slider to
   roughly the right region — an interaction no ecosystem surfaces in
   automations, voice control, or a scene.
2. **A travelling door has to claim a position.** Every percentage is
   some position, so `POSITION_UNKNOWN` had to be reported as a number.
   A read could not distinguish "moving" from "resting halfway".
3. **The same gap existed on the other planes.** Home Assistant's cover
   platform has no ventilation state either, so the MQTT projection
   carried a `vent_command_topic` that HA's closed key schema dropped
   before any entity saw it.

Matter 1.4 added ClosureControl (0x0104) for exactly this device class: a
closure whose travel has named stops. Its `CurrentPositionEnum` carries
`OpenedForVentilation` and its `TargetPositionEnum` carries
`MoveToVentilationPosition`, both gated behind the `VT` (Ventilation)
feature, which itself carries conformance `[PS]` — it is never advertised
without Positioning.

## Decision

**A garage drive projects as the Matter Closure device type (0x0230)
carrying a ClosureControl (0x0104) server with `FeatureMap = PS | VT`.**

Four parts:

1. **The WindowCovering projection is removed, not supplemented.** The
   Closure device type lists WindowCovering with conformance `X` —
   disallowed (matter.js `closure-device.element.ts:20`). A drive
   carrying both would describe itself two ways at once.

2. **Named stops replace percentages.** `DOOR_STATE` maps onto
   `CurrentPositionEnum`; `POSITION_UNKNOWN` maps onto a **null**
   Position with `MainState = Moving`, which is what the spec's
   quality-`X` field is for. `MoveTo` maps `TargetPositionEnum` back onto
   `DOOR_COMMAND`.

3. **The lift-percentage machinery goes with it.** The garage no longer
   carries `matterTarget` (the commanded lift target) or `matterGoTo`
   (the slider-gesture debouncer). Neither has an analogue: a
   ClosureControl command names a stop, so there is no gesture to
   coalesce and no intermediate target to infer.

4. **`SECTION` no longer feeds the Matter target.** The drive's motion
   phase was used to snap a stale lift target back to the current
   position. ClosureControl drops its target when the drive reports a
   named stop, which arrives on `DOOR_STATE`.

Cover and Blind are unaffected — they have a genuine continuous axis and
stay on WindowCovering.

## Consequences

- **The ventilation position becomes reachable and legible.** It is a
  named enum value on the Matter side and a `select` entity on the MQTT
  side, rather than a slider region and a dropped discovery key.

- **This is a one-way migration for paired devices.** An endpoint's
  device type changing is treated by ecosystems as a different accessory.
  A drive already paired as a window covering has to be re-added. The
  cost is accepted because the previous projection could not express the
  device: no amount of re-pairing later recovers a stop that was never
  named.

- **Controller rendering is unverified at the time of writing.** How
  Apple Home and Google Home present a Closure endpoint was not
  established here — the port was made against matter.js's cluster
  definitions, not against a live controller. The nightly chip-tool suite
  (`needs-chiptool` label) is the only guard in this repo that speaks to
  a real commissioner, and it is what has to confirm the projection. If
  it turns out no ecosystem renders a Closure endpoint, the decision to
  revisit is which cluster to project — not whether to return to
  encoding a named stop as a percentage.

- **The Closure device type's mandatory TAGLIST feature is not
  advertised.** The Descriptor cluster is shared by every endpoint and
  has TagList disabled for a separate, observed Apple interop failure.
  Recorded as `BD-Matter-ClosureWithoutTagList`; the two are one switch
  and are revisited together.

- **The parity surface grew a cluster.** ClosureControl's IDs, feature
  bits and conformance are pinned against the matter.js schema snapshot
  by name rather than against hand-typed constants, so an upstream rename
  or a feature inserted ahead of `VT` fails the build instead of shifting
  a bit silently.
