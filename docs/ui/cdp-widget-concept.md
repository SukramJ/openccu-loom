# CDP-Widget Concept — Custom-DP-aware UI rendering

## Status

Implemented (ADR 0016, Accepted 2026-05-20).

The SPA's device-detail surface has two interactive views:

- **Übersicht** (default) — one tile per Custom-DP, semantic
  operations via `POST /devices/{addr}/cdps/{name}/{operation}`.
  Driven by the widget tree under `assets/ui/src/lib/cdp/`.
- **Kanäle** — one tile per CCU channel, slot-aware CONTROL widgets.
  Driven by the widget tree under `assets/ui/src/lib/control/`. See
  [`control-widget-concept.md`](./control-widget-concept.md).

The selector lives in `DeviceDetail.svelte`; the preference is
persisted per-user in `localStorage` under
`openccu-loom.deviceDetailView`. The Übersicht / Kanäle toggle is
only visible when the user has the "Neue Bedienelemente" master
toggle enabled (legacy `QuickControlTab` users see the old surface).

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Übersicht-mode DeviceDetail.svelte                              │
│   └─ CdpTilesPanel.svelte                                       │
│      ├─ GET /devices/{addr}/cdps  →  list of CustomDPSummary     │
│      ├─ for each CDP with a registered widget:                  │
│      │    dispatch.ts → cdpWidgetFor(cdp.kind)                  │
│      │    └─ LightTile / CoverTile / ClimateTile / LockTile /   │
│      │       SirenTile / SwitchTile                             │
│      └─ orphan channels (no CDP) → ChannelControl.svelte        │
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

Every kind in the dispatcher has a registered widget — see
`assets/ui/src/lib/cdp/dispatch.ts` for the canonical mapping
(`text_display` → `TextDisplayTile`, `valve_irrigation` +
`valve_modulating` → `ValveTile`, etc.). Kinds the backend ships
that are NOT in the dispatcher fall through to the orphan-channel
section in CdpTilesPanel; the CONTROL-aware tree there handles
them via the channel-side widgets.

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
- Channels whose CDP kind has no widget yet (`text_display`,
  `valve_*` at the moment).
- Channels that the user explicitly wants to inspect at the raw
  CONTROL-family granularity (the Kanäle view stays available as
  the full-channel alternative).

The orphan section uses the same `ChannelControl.svelte` host the
Kanäle view uses, so behaviour is identical to what the user gets
from the segmented selector — only the filter set differs.

## Why semantic operations rather than per-slot writes

The Kanäle view writes through `PUT .../data-points/{param}/value`;
each tile maps user gestures into per-slot value changes. The CDP
view writes through `POST .../cdps/{name}/{operation}` — semantic
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

## Open follow-ups (tracked in `adr-todo.md`)

- WS event aggregation decision (per-CDP topic vs SPA-side
  assembly).
- `text_display` + `valve_*` CDP tiles when the use case lands.
- The view selector exposes a single per-user preference; consider a
  per-device override if the default ratio turns out off.
- Telemetry on the Übersicht ↔ Kanäle toggle rate to validate
  whether the default is correct in real installations.
