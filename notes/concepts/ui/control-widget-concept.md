# Control-Widget Concept — HA-aligned UI for CCU CONTROL semantics

## Status

Implemented. The Svelte primitives + widget tree live under
`assets/ui/src/lib/control/`. The CONTROL family inventory is in
`notes/reference/control-inventory.md`. The DeviceDetail surface routes
channels through `ChannelControl.svelte` (single-channel host),
embedded directly inside `CdpTilesPanel.svelte`'s "Sonstige Kanäle"
section for channels that have no CDP or whose CDP kind has no
registered widget. There is no separate grid component, no runtime
toggle, and no legacy-ParameterField revert path.

> **Context:** the CONTROL-aware widget tree documented here backs the
> orphan-channel section of the unified device-detail `overview` tab.
> The primary surface of `overview` renders one tile per Custom-DP
> (semantic operations rather than per-slot writes) — see
> [`cdp-widget-concept.md`](./cdp-widget-concept.md) and
> [ADR 0016](../../../docs/adr/0016-custom-dp-aware-ui-rendering.md). The two
> trees share their primitives (`ControlTile`, `ToggleFeature`,
> `NumericInputFeature`, slider primitives) — only the data binding
> and write path differ.

## Problem

Every CCU paramset descriptor carries a `CONTROL` attribute of the form
`<WIDGET_FAMILY>.<SLOT>`, e.g. `HEATING_CONTROL_HMIP.SETPOINT`,
`DIMMER.LEVEL`, `BLIND.LEVEL`. Together with the channel's category this
tells a UI what kind of widget the parameter should drive and which slot
within that widget it fills. The OCCU WebUI uses it via
`cObj.DPByControl("FAMILY.SLOT")` to compose per-channel widgets from
slots — but its visual layer is the dated CCU look-and-feel.

OpenCCU-Loom's SPA currently classifies channels by their `type` string
(`detectDomain` in `assets/ui/src/lib/quickcontrol/domain.ts`). That's
coarse-grained and drifts for HmIP-W vs HmIP-RF variants. The `Control`
field already lives in `pkg/hmproto.ParameterData.Control` but is not
exposed via REST. The opportunity is twofold:

1. **Replace channel-type heuristics with per-parameter CONTROL lookup** —
   precise, automatic disambiguation between HmIP / RF / Wired variants.
2. **Adopt the Home Assistant frontend's visual idiom** for the widgets —
   tile cards, ha-control primitives, state-coloured accents — instead of
   re-skinning the OCCU look.

## Inventory: 84 CONTROL families

Extracted from `../occu/` via `grep -rhoE 'control="[A-Z_]+\.[A-Z_]+"'`.
Full table in `notes/reference/control-inventory.md`. The Top-15 cover ~90 % of
typical residential CCU installations:

| Family | Slots seen on real devices |
|---|---|
| `SWITCH` | `STATE` |
| `DIMMER` | `LEVEL`, `LEVEL_REAL`, `OLD_LEVEL` |
| `BLIND` / `JALOUSIE` / `SHUTTER` | `LEVEL`, `LEVEL_SLATS`, `STOP` |
| `HEATING_CONTROL` | `SETPOINT`, `TEMPERATURE`, `CONTROL_MODE`, `AUTO`, `MANU`, `BOOST`, `COMFORT`, `LOWERING`, `PARTY_*` |
| `HEATING_CONTROL_HMIP` | `SETPOINT`, `TEMPERATURE`, `HUMIDITY`, `CONTROL_MODE`, `SETPOINT_MODE`, `BOOST_MODE`, `FROST_PROTECTION`, `HEATING_COOLING`, `WINDOW_STATE`, `VALVE_STATE`, `ACTIVE_PROFILE`, `PARTY_*` |
| `BUTTON` / `BTN_SHORT_ONLY` | `SHORT`, `LONG` |
| `LOCK` / `DOOROPENER` | `STATE`, `OPEN`, `UNCERTAIN` |
| `POWERMETER` | `POWER`, `ENERGY_COUNTER`, `VOLTAGE`, `CURRENT`, `FREQUENCY`, `BOOT` |
| `RGBW_COLOR` / `RGB_COLOR` | `COLOR` |
| `DUAL_WHITE_COLOR` / `DUAL_WHITE_BRIGHTNESS` | colour temp + level |
| `WIN_SC` / `WIN_SC_SENSOR` / `WIN_SC_SECURE` | `LEVEL`, `STATE`, `HANDLE_LOCK`, … |
| `DANGER` / `SMOKE_DETECTOR` / `WATER_DETECTION_TRANSMITTER` | `STATE` |
| `BRIGHTNESS_TRANSMITTER` / `WEATHER_TRANSMIT` / `TEMP` | per-sensor reading |
| `BATTERIE` / `DEVICE` / `MAINTENANCE` | `LOWBAT`, `SABOTAGE`, … |
| `RGBW_AUTOMATIC` | `PROGRAM`, `BRIGHTNESS`, `ON_TIME`, `RAMP_TIME` |

## Visual design language: Home Assistant frontend

Reference: `../frontend/src/components/ha-control-*.ts`,
`../frontend/src/panels/lovelace/cards/hui-tile-card.ts`,
`../frontend/src/panels/lovelace/card-features/hui-*-card-feature.ts`,
`../frontend/src/resources/theme/color/color.globals.ts`. All
Apache-2.0; we use them as visual + structural reference, not as code
to copy verbatim. Each Svelte primitive ships with a comment citing
the HA-side counterpart so the lineage stays traceable.

### Layer cake

```
┌─────────────────────────────────────────────────────────────────┐
│ widgets/<Family>.svelte                                         │
│   composes a `ControlTile` + a list of feature-Slot-Widgets      │
│   = HA's hui-tile-card with its card-features array              │
├─────────────────────────────────────────────────────────────────┤
│ features/<Feature>.svelte                                        │
│   one slot's interaction surface (e.g. NumericInputFeature for   │
│   DIMMER.LEVEL, TargetTemperatureFeature for SETPOINT)           │
│   = HA's hui-*-card-feature.ts                                   │
├─────────────────────────────────────────────────────────────────┤
│ controls/<Primitive>.svelte                                      │
│   styled atomic input: ControlSlider, ControlButton, etc.        │
│   = HA's ha-control-*.ts                                         │
├─────────────────────────────────────────────────────────────────┤
│ tile/{ControlTile,ControlTileIcon,ControlTileInfo}.svelte        │
│   state-coloured tile container with icon + label                │
│   = HA's hui-tile-card layout                                    │
├─────────────────────────────────────────────────────────────────┤
│ resolver.ts                                                      │
│   groupDPsByControlFamily(channel.dataPoints)                    │
│ families.ts                                                      │
│   FAMILY -> widget-component map                                 │
│ slot-mapping.ts                                                  │
│   SLOT-suffix -> feature-component for unknown families          │
│ state-color.ts                                                   │
│   (family, value) -> HA --state-* CSS var                        │
├─────────────────────────────────────────────────────────────────┤
│ DataPointSummary.Control exposed by REST (Step 1 of this plan)   │
└─────────────────────────────────────────────────────────────────┘
```

### Primitive sizes (1:1 from HA)

`../frontend/src/components/ha-control-slider.ts:401-407`:

```
--control-slider-thickness: 40px
--control-slider-border-radius: var(--ha-border-radius-md)
--control-slider-background-opacity: 0.2
--control-slider-color: var(--primary-color)
```

`../frontend/src/components/ha-control-button.ts:35-42`:

```
height: 40px
width: 40px (square minimum; flex:1 inside a button-group expands)
--control-button-padding: 8px
--control-button-background-opacity: 0.2
--mdc-icon-size: 20px
```

`../frontend/src/components/ha-control-button-group.ts:21-23`:

```
--control-button-group-spacing: 12px
--control-button-group-thickness: 40px
```

`../frontend/src/panels/lovelace/cards/tile/tile-card-style.ts:5-9`:

```
border-color: var(--tile-color)
box-shadow: 0 0 0 1px var(--tile-color) on focus
```

### State colours (HA tokens; we keep the names verbatim)

From `../frontend/src/resources/theme/color/color.globals.ts`:

| CONTROL family / state | HA token | Note |
|---|---|---|
| `SWITCH` on | `--state-switch-active-color` | amber |
| `DIMMER` level>0 | `--state-light-active-color` | amber |
| `BLIND`/`JALOUSIE`/`SHUTTER` active | `--state-cover-active-color` | purple |
| `HEATING_CONTROL[_HMIP]` heat | `--state-climate-heat-color` | deep-orange |
| `HEATING_CONTROL[_HMIP]` cool | `--state-climate-cool-color` | blue |
| `HEATING_CONTROL[_HMIP]` auto | `--state-climate-auto-color` | green |
| `LOCK` unlocked | `--state-lock-active-color` | red |
| `DANGER`/`SMOKE_DETECTOR` alarm | `--state-siren-active-color` | red |
| `WIN_SC`/door open | `--state-binary_sensor-door-on-color` | blue |
| `WATER_DETECTION` alert | `--state-binary_sensor-moisture-on-color` | red |

These map seamlessly to our `app.css` HA-token block.

## Family-to-widget composition examples

### `DIMMER`

```
ControlTile
├── ControlTileIcon (lightbulb, state-coloured when LEVEL > 0)
├── ControlTileInfo ("Küchenstrahler" / "60 %")
└── features:
    ├── ToggleFeature             ← derived: LEVEL > 0 ↔ on/off
    └── NumericInputFeature       ← slot LEVEL, slider 0–100 %
```

### `HEATING_CONTROL_HMIP`

```
ControlTile
├── ControlTileIcon (thermostat icon, coloured by mode/state)
├── ControlTileInfo ("Wohnzimmer" / "21.0 °C → 22.0 °C")
└── features:
    ├── TargetTemperatureFeature  ← slot SETPOINT, large stepper
    ├── HvacModesFeature          ← slot CONTROL_MODE: AUTO/MANU/PARTY/BOOST
    ├── PresetModesFeature        ← slot BOOST_MODE / SETPOINT_MODE / FROST_PROTECTION
    └── (badges)
        ├── current temperature   ← slot TEMPERATURE
        ├── humidity              ← slot HUMIDITY
        ├── window open?          ← slot WINDOW_STATE
        └── valve %               ← slot VALVE_STATE
```

### `BLIND`

```
ControlTile
├── ControlTileIcon (blind icon)
├── ControlTileInfo ("Rollo Nord" / "50 % geöffnet")
└── features:
    ├── CoverOpenCloseFeature     ← slots LEVEL + STOP: up/stop/down buttons
    └── CoverPositionFeature      ← slot LEVEL: position slider
```

## Implementation status

| Step | Component | State |
|---|---|---|
| 1 | REST DTO — `DataPointSummary` carries `control`, `type` and `value_list` | ✅ shipped (`internal/north/rest/handlers/devices.go`, `assets/openapi.yaml`, `lib/api/types.ts`) |
| 2 | Primitives — `ControlSlider`, `ControlButton`, `ControlButtonGroup`, `ControlNumberButtons`, `ControlHueSlider`, `ControlSaturationSlider`, `ControlColorTempSlider`, `ControlColorPalette`, `ControlEnumSelect` | ✅ shipped (`lib/control/controls/`) |
| 3 | Tile — `ControlTile`, `ControlTileIcon`, `ControlTileInfo`, `state-color.ts` | ✅ shipped (`lib/control/tile/`) |
| 4 | Features — `ToggleFeature`, `NumericInputFeature`, `TargetTemperatureFeature`, `HvacModesFeature`, `PresetModesFeature`, `StatReadoutFeature`, `CoverOpenCloseFeature`, `LockCommandsFeature` | ✅ shipped (`lib/control/features/`) |
| 5 | Widgets — Switch, Dimmer, Blind, Climate, Lock, Powermeter, Sensor, BinarySensor, ColorLight, ColorTempLight, FixedColorLight, UniversalLight, Garage, Siren, ButtonEvent, SimpleRfThermostat | ✅ 16 widgets shipped (`lib/control/widgets/`) |
| 6 | Resolver + Family Registry — `families.ts`, `resolver.ts`, `widgets/index.ts` | ✅ shipped; 81 of 84 families routed |
| 7 | DeviceDetail integration — `ChannelControl.svelte` embedded in `CdpTilesPanel.svelte`'s orphan-channel section | ✅ shipped (`lib/control/`, `lib/cdp/CdpTilesPanel.svelte`, `routes/DeviceDetail.svelte`) |
| 8 | Icon harmonisation — all tile glyphs via `$lib/icons` (Lucide / mdi-style names) | ✅ shipped |
| 9 | Unit tests — Vitest covering resolver, state-color, widget registry, slot-aware routing | ✅ 56 cases |
| 10 | Slot-aware routing — `widgetForResolved()` upgrades widgets based on the channel's slot inventory in addition to the dominant family | ✅ shipped (see §"Slot-aware routing" below) |
| 11 | REST PUT value path — body coerced against the descriptor's TYPE via `parameter.Coerce`, so integer-valued JSON numbers reach FLOAT parameters without collapsing to int | ✅ shipped (`internal/north/rest/handlers/devices.go`) |

### Widget × family routing (81 / 84)

| Widget | Families routed |
|---|---|
| `Switch` | SWITCH, SWITCH_TRANSMITTER, SIMPLE_SWITCH_RECEIVER, WATER_SWITCH, DIGITAL_STATE |
| `Dimmer` (base) | DIMMER, DIMMER_REAL, DUAL_WHITE_BRIGHTNESS, BACKLIGHTING_RECEIVER, SERVO_TRANSMITTER, SERVO_VIRTUAL_RECEIVER |
| `Blind` | BLIND, BLIND_TRANSMITTER, BLIND_VIRTUAL_RECEIVER, JALOUSIE, SHUTTER_TRANSMITTER, SHUTTER_VIRTUAL_RECEIVER, WINDOW, WINDOW_DRIVE_RECEIVER — slat tilt slider lights up when `LEVEL_2` (HmIP) or `LEVEL_SLATS` (RF) is present |
| `Climate` | HEATING_CONTROL_HMIP, HEATING_CONTROL, CLIMATECONTROL_FLOOR_TRANSCEIVER, CLIMATE_TRANSCEIVER — slot-aware: HmIP writes CONTROL_MODE directly (AUTO/MANU/AWAY), RF fires AUTO/BOOST/COMFORT/LOWERING action slots |
| `Garage` | DOOR_RECEIVER (HmIP-MOD-HO, HmIP-MOD-TM) — DOOR_COMMAND action buttons filtered against VALUE_LIST |
| `Lock` | LOCK, DOOROPENER, DOOR_LOCK_TRANSCEIVER |
| `Powermeter` | POWERMETER, POWERMETER_IEC, POWERMETER_IGL, POWERMETER_PSM |
| `Sensor` (read-only numeric grid) | ANALOG_INPUT, BRIGHTNESS_TRANSMITTER, CARBON_DIOXIDE_RECEIVER, DISTANCE_TRANSMITTER, FLOW_METER_TRANSMITTER, GENERIC_MEASURING_TRANSMITTER, SOIL_MOISTURE_TRANSMITTER, TEMP, TEMP_HUM_PARTICLE_MATTER_TRANSMITTER, WEATHER_TRANSMIT, ACCESSPOINT_GENERIC_RECEIVER, ACCELERATION_TRANSCEIVER, CAPACITIVE_FILLING_LEVEL_SENSOR, COND_SWITCH_TRANSMITTER_TEMPERATURE, DIGITAL_ANALOG_OUTPUT, GENERIC_INPUT_TRANSMITTER |
| `BinarySensor` | DANGER, DOOR_SENSOR, DOOR_STATE_TRANSCEIVER, MOTIONDETECTOR_TRANSCEIVER, RAIN_DETECTION_TRANSMITTER, RHS, SMOKE_DETECTOR, WATER_DETECTION_TRANSMITTER, WIN_SC, WIN_SC_SECURE, WIN_SC_SENSOR, BATTERIE, ACCESS_RECEIVER, ACCESS_TRANSCEIVER, ARMING, AUTO_RELOCK_TRANSCEIVER, DEVICE, DOOR_LOCK_STATE_TRANSCEIVER, DOOR_LOCK_STATE_TRANSMITTER, EVENT_INTERFACE, MAINTENANCE |
| `ColorLight` (HSV hue index) | RGBW_COLOR, RGB_COLOR |
| `ColorTempLight` | COLORTEMP, DUAL_WHITE_COLOR |
| `FixedColorLight` (slot-aware upgrade) | DIMMER + COLOR (HmIP-BSL Aktor + virt. Receiver), DIMMER_REAL + COLOR — see slot-aware routing |
| `UniversalLight` (slot-aware) | UNIVERSAL_LIGHT_RECEIVER (HmIP-RGBW, HmIP-DRG-DALI) — HSV + ColorTemp + EFFECT rendered conditionally |
| `Siren` | ACOUSTIC_SIGNAL_TRANSMITTER, ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER, ALARM_SWITCH_VIRTUAL_RECEIVER, _ALARM_SWITCH_VIRTUAL_RECEIVER, OPTICAL_SIGNAL_RECEIVER |
| `ButtonEvent` | BUTTON, BTN_SHORT_ONLY |
| `SimpleRfThermostat` (slot-aware, multi-family) | SWITCH + TEMP siblings (HM-CC-TC: `SWITCH.STATE` + `TEMP.SETPOINT` on one channel) — see "Slot-aware routing" below |

### Resolver tie-break

`resolveChannel()` (`lib/control/resolver.ts`) walks every CONTROL-tagged
data-point on a channel, groups them by family, then picks the dominant
family by descending slot count, ties broken alphabetically. Duplicate
slot suffixes within a family follow first-write-wins, so legacy
parameters listed earlier in the descriptor keep priority over their
modern peers. Channels with zero CONTROL-tagged DPs return `null` and
the caller falls back to the generic ParameterField list.

### Slot-aware routing

`widgetForResolved(resolved)` (`lib/control/widgets/index.ts`) sits
between `resolveChannel()` and the family registry. It looks at the
slot inventory and upgrades the widget when the dominant family is
enriched beyond its base shape:

| Family | Slot trigger | Upgraded widget | Real device |
|---|---|---|---|
| `DIMMER` or `DIMMER_REAL` | `COLOR` slot present | `FixedColorLight` | HmIP-BSL (LEVEL + COLOR + COLOR_BEHAVIOUR) |
| `DIMMER` or `DIMMER_REAL` | `COLOR_TEMPERATURE` slot present | `ColorTempLight` | (currently unobserved; reserved for kelvin-extended dimmer variants) |
| `SWITCH` | sibling `TEMP.SETPOINT` on the same channel | `SimpleRfThermostat` | HM-CC-TC (CLIMATECONTROL_REGULATOR channel: `SWITCH.STATE` + `TEMP.SETPOINT`) |
| `TEMP` | sibling `SWITCH.STATE` on the same channel | `SimpleRfThermostat` | HM-CC-TC (same channel, resolved from either dominant family) |

`resolveChannel()` exposes a `siblings` map alongside the dominant
family so multi-family channels (one CONTROL family per slot, same
channel) can be detected without changing the tie-break itself. The
same mechanism is the place for future slot-driven disambiguations
(e.g. switched powermeter combinations).

### Climate dual-family handling

`Climate.svelte` is the slot-aware joint widget for HmIP and RF
thermostats. The `isRf` flag (`family === "HEATING_CONTROL"`) selects
between two mode-picker code paths:

- **HmIP** (`HEATING_CONTROL_HMIP`): `CONTROL_MODE` is a writable
  integer enum without VALUE_LIST. Labels mirror aiohomematic's
  `_ModeHmIP` (climate.py:76-81): 0 = Auto, 1 = Manuell, 2 = Abwesend.
  `BOOST_MODE` and `FROST_PROTECTION` ride alongside as preset
  toggles.
- **RF** (`HEATING_CONTROL`): `CONTROL_MODE` is read-only ENUM with
  VALUE_LIST `[AUTO-MODE, MANU-MODE, PARTY-MODE, BOOST-MODE]`. The
  mode change happens by firing the matching ACTION slot
  (`AUTO`/`BOOST`/`COMFORT`/`LOWERING`); the widget renders these as
  a button group with the current mode highlighted.

### Long-tail (4 families intentionally unrouted)

`IDENTIFICATION`, `RC`, `ACOUSTIC_DISPLAY_RECEIVER`, `WEEK_PROFILE`
carry semantics outside the tile / button / slider vocabulary (UI-id
ping, IR remote-control teach-in, OLED text display, schedule
editor). They render through the generic ParameterField list until a
dedicated widget lands.

`RGBW_AUTOMATIC` (HM-LC-RGBW-WM channel 3) is a programmed effect
editor (PROGRAM index + BRIGHTNESS + MIN/MAX_BOARDER + ON_TIME +
RAMP_TIME). Semantically a configuration surface rather than a
quick-control tile; rendered through the generic ParameterField list
for now.

## Open architecture work

These items are deliberately deferred — each needs an ADR-level
discussion or a non-trivial code change beyond a per-widget addition.

- **Custom-DP channel aggregation.** HmIP-RGBW / HM-LC-RGBW-WM /
  HM-DW-WM split logical light entities across multiple CCU
  channels (actor + colour + effect, or HSV + brightness). aiohomematic
  aggregates these into a single `CustomDp*Light` model; the SPA
  today renders one tile per CCU channel and the user sees the
  fragmentation. A future SPA layer could group channels by
  custom-DP identity and present one tile.
- **Hub-entities and wall-displays.** Sysvars / Programs (CCU hub)
  and HmIP-WRC* wall-display rendering live outside the
  channel-CONTROL pipeline and have their own surfaces. Not in scope
  for this widget tree.

## Licensing & attribution

- **HA frontend (Apache-2.0)**: source-of-truth for visual design + sizes.
  Each Svelte primitive opens with a comment naming the HA file + line
  range it mirrors. No verbatim Lit code lands in the tree.
- **OCCU (eQ-3 HMSL, non-commercial)**: the CONTROL family inventory is
  factual API metadata extracted via `grep`. No ReGa code, no `.fn`
  files. `notes/reference/control-inventory.md` documents the extraction.
- **homematicip-local-frontend (MIT)**: structural reference for
  HA-panel-context, identical licence.
- **OpenCCU-Loom itself**: MIT, unchanged.

An entry in `docs/attribution.md` records the inspiration and licence
of HA frontend for future audit.
