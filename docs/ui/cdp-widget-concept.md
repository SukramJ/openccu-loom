# CDP-Widget Concept — Custom-DP-aware UI rendering

## Status

Implemented (ADR 0016, Accepted 2026-05-20).

The SPA's device-detail surface (`DeviceDetail.svelte`) uses three
top-level tabs — `overview` / `configure` / `history` — not a
separate Übersicht/Kanäle view selector. `overview` renders the
unified `CdpTilesPanel.svelte`:

- One tile per Custom-DP, semantic operations via
  `POST /devices/{addr}/cdps/{name}/{operation}`. Driven by the
  widget tree under `assets/ui/src/lib/cdp/`.
- Channels that are *not* attached to a CDP (or whose CDP kind has no
  registered widget) render inline in the same panel via
  `ChannelControl.svelte`, the slot-aware CONTROL widget host under
  `assets/ui/src/lib/control/`. See
  [`control-widget-concept.md`](./control-widget-concept.md).

There is no per-user view preference, no `localStorage` selector, and
no "Neue Bedienelemente" master toggle — CDP tiles and CONTROL-family
orphan channels appear together in the single `overview` tab.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ DeviceDetail.svelte — "overview" top-tab                        │
│   └─ CdpTilesPanel.svelte                                       │
│      ├─ GET /devices/{addr}/cdps  →  list of CustomDPSummary     │
│      ├─ for each CDP with a registered widget:                  │
│      │    dispatch.ts → cdpWidgetFor(cdp.kind)                  │
│      │    └─ LightTile / CoverTile / ClimateTile / LockTile /   │
│      │       SirenTile / SwitchTile / TextDisplayTile / ValveTile│
│      └─ orphan channels (no CDP, or unregistered kind)          │
│           → ChannelControl.svelte (same panel, "Sonstige Kanäle")│
├─────────────────────────────────────────────────────────────────┤
│ each CDP tile:                                                  │
│   - reads cdp.kind + cdp.capabilities + cdp.channel_no          │
│   - fetches GET /devices/{addr}/channels/{no}/data-points       │
│     for the underlying state values (no CDP-state surface yet)  │
│   - subscribes to WS data-point events scoped to its channel    │
│   - writes via api.invokeCustomDataPoint(addr, name, op, params)│
├─────────────────────────────────────────────────────────────────┤
│ Backend                                                         │
│   - internal/model/custom/cdpkind/kind.go                       │
│     Of(dp) → kind string;  Capabilities(dp) → flat bool map     │
│   - internal/north/rest/handlers/custom_data_points.go          │
│     CustomDPSummary { name, category, channel_no,               │
│                       supported_operations, kind, channels,     │
│                       capabilities }                            │
│   - internal/north/rest/handlers/devices.go                     │
│     ChannelSummary.custom_dp_name (filter hook)                 │
└─────────────────────────────────────────────────────────────────┘
```

## Kind → widget mapping

| Kind | Trigger custom-DP | Widget | Service operations consumed |
|---|---|---|---|
| `light` | `*light.Light` | `LightTile` | turn_on / turn_off / set_brightness |
| `light_color` | `*light.ColorLight` (HSV) | `LightTile` | + set_color {hue, saturation} |
| `light_color_temp` | `*light.ColorTempLight` | `LightTile` | + set_color_temperature {kelvin} |
| `light_fixed_color` | `*light.FixedColorLight` (HmIP-BSL etc.) | `LightTile` | + set_color {color: "RED"…} |
| `light_rgbw` | `*light.RGBWLight` (HmIP-RGBW) | `LightTile` | + Color/Weiß toggle, set_effect |
| `light_dali` | `*light.DRGDaliLight` | `LightTile` | brightness + colour-temp variant |
| `light_effect` | `*light.EffectLight` | `LightTile` | + set_effect |
| `light_sound_led` | `*light.SoundPlayerLED` (HmIP-MP3P) | `LightTile` | (same surface as fixed-colour) |
| `cover` | `*cover.Cover` | `CoverTile` | open / close / stop / set_position |
| `cover_blind` | `*cover.Blind` | `CoverTile` | + set_tilt (LEVEL_2 / LEVEL_SLATS) |
| `cover_garage` | `*cover.Garage` | `CoverTile` | open / close / stop / ventilate |
| `climate_simple` | `*climate.Climate` (Kind=SimpleRF) | `ClimateTile` | set_temperature + heat on/off toggle |
| `climate_rf` | `*climate.Climate` (Kind=RF) | `ClimateTile` | set_temperature + set_mode + enable_boost / disable_boost / disable_away |
| `climate_hmip` | `*climate.Climate` (Kind=IP) | `ClimateTile` | set_temperature + set_mode + enable_boost / disable_boost + presets |
| `lock` | `*lock.Lock` | `LockTile` | lock / unlock / open |
| `siren` | `*siren.Siren` | `SirenTile` | turn_on / turn_off |
| `switch` | `*switchdev.Switch` | `SwitchTile` | turn_on / turn_off / turn_on_for |
| `text_display` | `*textdisplay.TextDisplay` | `TextDisplayTile` | write {id, text, icon?, color?} |
| `valve_irrigation` | `*valve.Irrigation` | `ValveTile` | open / close |
| `valve_modulating` | `*valve.Modulating` | `ValveTile` | open / close / set_level |

Every kind in the dispatcher (`assets/ui/src/lib/cdp/dispatch.ts`)
has a registered widget. Kinds the backend ships that are NOT (yet)
in the dispatcher fall through to the orphan-channel section in
`CdpTilesPanel`; the CONTROL-aware tree there handles them via the
channel-side widgets.

## Capability flags

The backend's `Capabilities(dp)` flattens each category's Go
capability struct (`internal/model/custom/mixins.go`) into a
string→bool map. Each tile reads the flags it needs and ignores
the rest, so the wire shape stays category-agnostic.

| Category | Flags exposed |
|---|---|
| Light | `dimmable`, `color`, `color_temp`, `effects`, `transition` |
| Cover | `position`, `tilt`, `stop`, `vent`, `inverted_control` |
| Climate | `boost`, `profile`, `auto`, `heat`, `cool`, `off`, `away`, `comfort`, `eco` |
| Lock | `open`, `child_safe` |
| Siren | `acoustic`, `optical`, `duration`, `soundfiles`, `volume_set` |

## State sourcing

The CDP detail endpoint (`GET /devices/{addr}/cdps/{name}`) returns
a `state` field that is currently populated only when the Custom-DP
implements `DataPointState() any` — which none of them do yet. To
keep the rollout unblocked, every CDP tile fetches the underlying
channel's data-points (`GET .../channels/{no}/data-points`) and
reads its slot values from there. WS events arrive on a per-DP
channel-scoped topic; the tile patches `dataPoints` in place when
the event matches its channel.

A future step (see ADR 0016 follow-ups) lifts state assembly out of
the SPA either by populating `DataPointState()` on every Custom-DP
type or by emitting a per-CDP WS topic. The decision lands once
real-traffic experience with the current path surfaces the actual
bottleneck.

## Orphan-channel fallback

`CdpTilesPanel` renders a "Sonstige Kanäle" section below the CDP
grid for every channel that is *not* attached to a CDP whose widget
is registered. Practically this means:

- Maintenance / Identification / device-root channels (which never
  carry a CDP).
- Channels whose CDP kind has no registered widget yet (new kinds the
  backend ships ahead of a dispatcher entry).
- Status-only channels that AutoTile renders as read-only cards (see
  [`auto-tile-concept.md`](./auto-tile-concept.md)) rather than
  through a full CONTROL widget.

The orphan section uses the same `ChannelControl.svelte` host,
embedded directly in `CdpTilesPanel` — there is no separate route or
view to switch to; orphan channels simply render alongside the CDP
tiles in the one `overview` panel.

## Why semantic operations rather than per-slot writes

Orphan CONTROL channels write through `PUT .../data-points/{param}/value`;
each widget maps user gestures into per-slot value changes. The CDP
tiles write through `POST .../cdps/{name}/{operation}` — semantic
verbs like `set_temperature {temperature}` or
`set_color {hue, saturation}`. Two benefits:

1. **Server-side authority over multi-slot commands.** Setting a
   colour on HmIP-BSL means writing COLOR + COLOR_BEHAVIOUR
   atomically. The daemon's CDP dispatcher does that; the SPA never
   has to know the wire-level recipe.
2. **One contract across north-bound surfaces.** MQTT discovery
   already publishes the same operation set as `*_command_topic`s;
   the matter-bridge maps cluster commands to the same vocabulary.
   The CDP REST surface is the canonical write path — adding a new
   north-bound interface is "drive the existing operations", not
   "find another slot recipe".

## Open follow-ups

- WS event aggregation decision (per-CDP topic vs SPA-side
  assembly).
- New CDP kinds the backend ships ahead of a dispatcher entry render
  via the orphan-channel `ChannelControl` fallback until a dedicated
  widget lands.
