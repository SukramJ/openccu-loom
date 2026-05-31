# ADR 0016 — Custom-DP-Aware UI Rendering

- **Status**: Accepted
- **Date**: 2026-05-20
- **Related**: `docs/ui/control-widget-concept.md`,
  `internal/model/custom/`, `internal/north/rest/handlers/custom_data_points.go`,
  `assets/ui/src/lib/control/`

## Context

`OpenCCU-Loom` carries a two-layer model that mirrors aiohomematic:

- **Generic data points** (one per CCU parameter on one CCU channel)
  — the wire-level shape, identical to what XML-RPC and BIN-RPC
  publish. Stored on `*model/device.Channel`.
- **Custom data points** (CDPs) — typed semantic façades that compose
  one or more generic DPs into a single domain object (light, cover,
  climate, lock, …). Lives under `internal/model/custom/<category>/`.
  A CDP attaches to a primary channel (`Channel.CustomDataPoint()`)
  but can pull from sibling channels of the same device via the
  `RebasedChannelGroupConfig` mechanism — e.g. the HmIP-BSL bundle
  reaches LEVEL on the actor channel and COLOR / COLOR_BEHAVIOUR /
  RAMP_TIME on the virtual receivers.

The Svelte SPA today renders **one tile per CCU channel**.
`ControlTilesPanel.svelte` walks `detail.channels`, filters via
`isOverviewExcluded(ch)`, then hands each surviving channel to
`ChannelControl.svelte`, which resolves the dominant CONTROL family
on the channel's DPs and picks a widget from the registry. The
custom-DP layer is invisible to the SPA — there is no SPA-side path
through `GET /devices/{addr}/cdps`, no semantic aggregation, no
notion of "this lamp = these three channels".

Concrete consequences on real devices:

| Device | Custom-DP | Channels surfaced today |
|---|---|---|
| HmIP-RGBW | `IPRGBW` on the UniversalLight channel | 1 channel → 1 tile (no fragmentation) |
| HmIP-BSL | `IPFixedColorLight` on the actor channel | 1 actor + 3 virtual receivers → 4 tiles |
| HM-LC-RGBW-WM | `RfDimmer_Color_Fixed` spanning 3 channels | 3 tiles (DIMMER actor + RGBW_COLOR + RGBW_AUTOMATIC) for one logical lamp |
| HM-DW-WM | `RfDimmer_Color` (HSV across channels) | 6 channels (actor + 5 virtual dimmers) |
| HmIP-BWTH | `IPThermostat` | 1 channel → 1 tile (no fragmentation) |
| HmIP-FBL blind | `IPCover` blind | 1 actor + 3 virtual receivers → 4 tiles |

The fragmentation pattern is consistent: physical devices with one
load (light / blind / climate) have one logical CDP, but the CCU
splits the data points across many channels, and the SPA currently
mirrors the CCU shape rather than the CDP shape. Users see
"Lampe RGBW" once in the device list and three (or six) tiles when
they open the device detail.

aiohomematic resolves this by treating the CDP as the entity
boundary: `aiohomematic2mqtt` publishes one HA Discovery entity per
CDP, `homematicip_local` builds one HA entity per CDP, and the
HmIP-Config UI groups its channel pickers around CDPs. The CCU's
WebUI also groups by CDP-equivalent ("Geräteeinstellungen pro
Aktor", not "pro Kanal"). The CCU-channel granularity is internal
plumbing rather than a user-facing concept.

The REST API has had `GET /devices/{addr}/cdps` and friends since
ADR 0007's Source-interface work; the SPA simply does not consume
them.

### Why this surfaced now

The CONTROL-aware widget system (see
`docs/ui/control-widget-concept.md`) raised the per-channel
fidelity dramatically — every channel now gets an HA-style tile
instead of a parameter list. That made the channel duplication
visible: a user who used to see "ein Block mit Parametern" pro
Channel now sees full Dimmer/Color tiles per channel. The signal is
louder, but the underlying mismatch has existed since the model was
built.

## Options considered

### Option 1 — Status quo: render per CCU channel

Keep the channel-loop rendering; treat CDPs as backend-only
plumbing. The SPA stays simple (no additional joins, no new DTO
fields) and remains a faithful view of the CCU's wire model.

**Pros**
- Zero implementation cost.
- Power users who think in CCU channels (long-time WebUI veterans)
  find what they expect.
- No new failure modes around CDP hydration timing.

**Cons**
- The "lampe = drei Tiles" fragmentation persists. HM-LC-RGBW-WM,
  HM-DW-WM and the HmIP-BSL/FBL families look wrong to anyone who
  has used HA, Home Assistant Matter, or the eq-3 mobile app.
- Slot-aware routing can't compensate: even with `FixedColorLight`
  and `UniversalLight` upgrades, multi-channel CDPs still produce
  multiple tiles. The semantic boundary lives one layer above the
  CONTROL family.
- North-bound parity with aiohomematic is incomplete on the UI
  side. MQTT discovery already publishes per-CDP; the SPA does not
  match.

### Option 2 — CDP-first: render one tile per Custom-DP

Replace the channel loop in `ControlTilesPanel` with a CDP loop:
fetch `GET /devices/{addr}/cdps`, render one tile per CDP, and bind
the tile's controls to the CDP's REST surface
(`POST /cdps/{name}/{operation}`). The remaining channels (those
without an attached CDP — typically maintenance channels, sensors
that the model didn't promote to a CDP) render as a secondary
"Sonstige Kanäle" section using the existing per-channel widgets.

**Pros**
- "Lampe = ein Tile" matches user mental model.
- Pulls the SPA onto the same semantic surface as MQTT discovery
  and HA frontend, completing the aiohomematic-parity story.
- The widget catalogue becomes simpler: per-CDP-kind widgets
  (`Light`, `Cover`, `Climate`, `Lock`, `Siren`) instead of per-
  family widgets that fight CCU's per-channel fragmentation.
- The CDP REST surface already exists; no backend churn beyond DTO
  enrichment.

**Cons**
- Channels that are part of a CDP but carry *additional* CONTROL
  data not surfaced by the CDP API (e.g. the BSL virtual receivers'
  full programmable colour-list loop, RGBW_AUTOMATIC effect-program
  editing) become harder to reach. The SPA needs an explicit "show
  channels" affordance for power users.
- The CDP DTO is currently minimal (`Name`, `Category`, `ChannelNo`,
  `SupportedOperations`). To drive a tile it needs to expose the
  same shape the channel tiles consume: capability flags, current
  state across the bundled slots, observable changes via WS. The
  CDP detail endpoint returns a state snapshot already, but the WS
  event bus is per-DP — the SPA has to assemble the picture from
  multiple WS events into one CDP-state tile.
- The CONTROL-aware widget tree we just built becomes the
  *fallback* path (for orphan channels) rather than the primary
  path. The investment doesn't go away, but the per-family widgets
  carry less of the user experience.

### Option 3 — Hybrid: CDP-first with an opt-in channel detail

Default rendering is CDP-first (option 2): one tile per Custom-DP,
non-CDP channels grouped at the bottom. The device detail surface
gains an explicit affordance ("Kanäle anzeigen" toggle, or a
secondary tab "Kanäle") that switches into the channel-loop view
(option 1) for users who want raw channel access. The setting is
per-user, stored in localStorage like the existing
"Neue Bedienelemente" toggle.

**Pros**
- Matches the user mental model by default (option 2 strengths).
- Power-user escape hatch keeps the CCU-channel-aware audience
  served (option 1 strengths).
- The two views share the same widget tree — channel view uses the
  CONTROL-aware widgets, CDP view uses CDP-bound widgets, no
  duplicated code path per device kind.
- Migration is reversible: a user who hates the new view can flip
  back to the channel view; we collect telemetry on the toggle
  rate to validate whether the default is correct.

**Cons**
- Two views to maintain. Bug surfaces double for cross-cutting
  concerns (WS event integration, optimistic-update semantics,
  loading skeletons).
- The toggle's persistence raises a small question of scope:
  per-device or globally per user? Per-user-globally is the
  simpler answer.
- Telemetry on toggle rate requires a small instrumentation
  addition (event count) — easy but visible.

## Decision

Adopt **option 3** — Hybrid CDP-first with an opt-in channel detail.

Rationale:

- Option 1 (status quo) leaves the experience demonstrably wrong on
  every multi-channel CDP. The CCU channel decomposition is an
  implementation detail of the wire protocol, not a user concept.
- Option 2 (CDP-only) is the right default, but slamming the door on
  channel access disenfranchises the WebUI-veteran audience, where
  thinking in channels is muscle memory. They are also the audience
  most likely to file useful bug reports — alienating them costs
  signal we need.
- Option 3 gets the user-facing default right without removing the
  power-user surface.

## Implications throughout the stack

### Backend — REST DTO enrichment

The CDP DTO grows enough to drive a tile without a second
round-trip:

- `CustomDPSummary` gains:
  - `kind`: a stable widget hint (`light`, `light_color`,
    `light_color_temp`, `light_rgbw`, `cover`, `cover_blind`,
    `cover_garage`, `climate_hmip`, `climate_rf`, `climate_simple`,
    `lock`, `siren`, …). Derived from the Go type, not the CCU
    family — orthogonal to CONTROL.
  - `channels`: the full list of CCU channels the CDP composes
    (primary + group siblings). Lets the SPA hide those channels
    from the orphan-channel section.
  - `capabilities`: a `map[string]bool` for switch-on capability
    flags (e.g. `dimmable`, `color`, `color_temp`, `effects`,
    `tilt`, `boost`, `away`). Mirrors aiohomematic's per-kind
    capability struct. Drives which features the tile renders.
- `CustomDPDetail.state` keeps its current shape (kind-specific
  snapshot from `DataPointState()`), but the contract is now
  load-bearing — every kind must populate it.
- A WS topic carries CDP-level state-change events. Today the WS
  bus carries per-DP events; the SPA reassembles. ADR 0011's
  aggregated state topic in MQTT is the precedent — apply the same
  shape to WS so the SPA gets one event per CDP-state change.

### Backend — non-CDP channel surface

The `isOverviewExcluded(ch)` filter on the SPA side becomes
"channel is not bound to any CDP **and** not a maintenance / device
channel". Push the binding query into the REST `ChannelSummary`:
add a `customDpName` field (empty when the channel has no CDP, the
CDP name otherwise). The SPA's CDP view uses this to skip; the
channel view ignores it.

### SPA — view selector

`DeviceDetail.svelte` gains a tab pair / segmented control:

- **Übersicht** (default): renders CDPs as tiles (CDP-first). Below
  the CDP grid, a collapsed "Sonstige Kanäle" section lists
  channels without a CDP binding (rendered through the existing
  CONTROL-aware widget tree). Maintenance-style channels remain
  hidden behind a "Wartung" expander.
- **Kanäle**: renders one tile per CCU channel via the existing
  `ControlTilesPanel`. No semantic aggregation. Power-user mode.

The selector state lives in `localStorage` per device-detail page,
default "Übersicht". A small banner on first use ("Tipp: Kanal-
Ansicht verfügbar") points at the toggle once.

### SPA — CDP widget tree

A parallel widget tree under `assets/ui/src/lib/cdp/widgets/`
mirrors the CDP kinds (one widget per kind, not per CCU family).
Each widget consumes the CDP DTO instead of a `ResolvedChannel`:

- `LightTile.svelte` — covers Dimmer / Color / ColorTemp / RGBW /
  FixedColor / Universal; branches by `capabilities` flags. The
  existing CONTROL-aware light widgets supply the visual idiom
  (sliders, palettes, mode pickers) — only the data binding
  changes.
- `CoverTile.svelte` — Blind / Garage / Cover variants.
- `ClimateTile.svelte` — HmIP / RF / Simple-RF variants. Replaces
  the slot-aware Climate widget at the CDP level.
- `LockTile.svelte`, `SirenTile.svelte`, `SwitchTile.svelte` —
  thin wrappers over their channel-side counterparts.
- A `dispatch.ts` chooses the widget by `cdp.kind`.

The CONTROL-aware widget tree under `lib/control/` is unchanged
and continues to back the Kanäle view + orphan-channel section.

### Documentation

`docs/ui/control-widget-concept.md` gains a sister document
`docs/ui/cdp-widget-concept.md` that documents the CDP-side view.
The CONTROL doc gets a short pointer in its intro: "the
CONTROL-aware widget tree backs the Kanäle view; the default
Übersicht view consumes CDPs — see ADR 0016 and the CDP doc."

## Consequences

### Positive

- The default device-detail view matches the user mental model:
  one logical device control per Tile.
- North-bound parity with aiohomematic completes: MQTT discovery
  (per CDP), Matter Bridge (per CDP, see ADR 0012), and the SPA
  all converge on the CDP boundary.
- The widget catalogue simplifies for the average user; the
  CONTROL-aware tree remains the building blocks (so the investment
  is preserved, just rebound).
- Hidden CCU complexity (virtual receivers, RGBW_AUTOMATIC effect
  channels, BSL programmable colour lists) becomes opt-in instead
  of opt-out.

### Negative

- Two views require double maintenance. WS integration, optimistic
  updates, loading skeletons must work in both. The CDP-side widget
  tree is a few thousand LOC of fresh Svelte code.
- The CDP DTO enrichment (kind, channels, capabilities, state) is
  load-bearing: gaps surface as broken tiles. Contract tests on
  the DTO will be necessary to catch drift between the Go custom-DP
  model and the SPA's expectations.
- Telemetry on the view selector is a new surface — minor
  privacy / config consideration.

### Mitigations

- Land the migration in slices: CDP-side tiles for the most-used
  category first (light), measure, then cover / climate / lock /
  siren. The Kanäle view stays the default during each migration
  slice; the user opts in to the CDP view per category until all
  categories are covered, then the default flips.
- Contract tests in `tests/contract/` enforce: every Custom-DP kind
  exposes a `kind` string; every kind has a widget under
  `lib/cdp/widgets/`; every kind's `capabilities` map covers the
  flags its widget consumes.
- An ADR-todo entry tracks the per-category rollout and the
  default-flip moment.

## Open questions (decide during implementation, not in this ADR)

- **WS event aggregation shape.** Either expose a per-CDP WS topic
  on the daemon (Backend lifts the aggregation) or have the SPA
  assemble CDP state from per-DP WS events (SPA lifts it). MQTT
  ADR 0011 chose the latter (per-DP topics, aggregated derived
  view). Apply the same here unless WS-channel bookkeeping turns
  out to be the bottleneck.
- **Orphan-channel rendering location.** Inline in Übersicht under
  the CDP grid, or a separate "Sonstige" tab. UX call, defer.
- **Capability extension mechanism.** When a future device adds a
  new capability flag, do we ship widget changes + DTO changes in
  lockstep, or does the SPA degrade gracefully on unknown flags?
  Both are tractable; pick the simpler one during implementation.

## Follow-ups

All originally-listed follow-ups shipped during the CDP rollout:
`CustomDPSummary` carries `kind` / `channels` / `capabilities`,
`ChannelSummary.custom_dp_name` drives the orphan filter, the
LightTile + every other kind landed in `assets/ui/src/lib/cdp/widgets/`,
the concept lives in [`../ui/cdp-widget-concept.md`](../ui/cdp-widget-concept.md),
and `DeviceDetail.svelte` exposes the view selector. The WS
aggregation shape settled on the per-CDP `CustomDataPointStateEvent`
envelope.
