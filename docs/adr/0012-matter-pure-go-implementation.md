# ADR 0012 — Matter bridge: pure-Go implementation, prioritized cluster subset

- **Status**: accepted
- **Date**: 2026-05-05
- **Supersedes**: refines `SPECIFICATION.md` §6 (Matter Bridge —
  intended scope + earlier 7-week estimate)
- **Related**: ADR 0001 (MIT), ADR 0007 (Strong model source interface),
  ADR 0010 (Discovery payload from model), `SPECIFICATION.md` §2.2
  Non-Goals, §6 Matter Bridge

## Context

The Matter bridge is the major v1.1 feature. `SPECIFICATION.md` §6
records intent ("native partial Go") but defers implementation form and
underestimates effort (7 weeks). With v1.0 substantially complete, the
implementation form must now be locked, the cluster subset finalised,
and the existing custom/generic/calculated data-point surface mapped to
concrete Matter clusters so the forward-compat hooks documented in
SPEC §6.3 can be verified.

Three constraints frame the decision:

1. **Distribution channels**: OpenCCU-Loom ships as a Docker container,
   a Home Assistant Add-on (also a container under the supervisor), and
   a CCU/OpenCCU Addon (tarball into the CCU filesystem). All
   three are *packaged units*. The "single static binary" property is
   therefore valuable but not load-bearing — it's a build-output
   convenience, not a deployment requirement.
2. **Resource envelope**: the CCU3 / OpenCCU Addon path runs on
   1 GB RAM, ARMv7 / Cortex-A7. Anything that adds a Node.js or Rust
   runtime there has to justify ~50–100 MB resident.
3. **Device reality**: the device population this daemon targets is
   *Homematic / HomematicIP*. The Matter clusters that have non-trivial
   matches against that population are a small subset of Matter 1.5.1,
   not the full cluster catalogue.

### What changed since the spec was written

- The earlier SPEC Matter section listed 10 clusters at parity
  weighting; the actual mapping against OpenCCU-Loom's custom /
  generic / calculated DP surface (`internal/model/{custom,generic,
  calculated}`) shows that **four cluster families plus a small set
  of measurement/binary clusters** carry ~95 % of the semantic value.
  The remaining clusters are either redundant (Generic.Sensor
  variants → six Matter measurement clusters) or have no Homematic
  counterpart (Speaker, Pump).
- The forward-compat claim ("DataPointValueChangedEvent carries
  enough structural data") was a guess at write-time. With the model
  layer now implemented (Phase 2 done), we can verify it concretely
  through the mapping table in §6 of this ADR.
- Cluster set scoped down means "bridge endpoint topology" (one
  Bridged Device per Homematic device, one Endpoint per channel that
  exposes one of the mapped clusters) becomes the binding constraint —
  not cluster count.

## Options considered

### Option A — pure Go, hand-rolled

The Matter substrate (TLV codec, MRP, Secure Channel, Interaction
Model, PASE/CASE, attestation chain, mDNS) is built in Go using stdlib
+ small pure-Go libraries.

- **Pros**: matches `CGO_ENABLED=0`, single static binary across all
  three distribution channels, no second runtime on the CCU Addon
  path, no second build toolchain in CI, no second security-patching
  pipeline. License-clean (MIT/Apache-2.0 deps only). Matches the
  `aiohomematic`-style ports & adapters architecture.
- **Cons**: no usable Go starting point. All Matter Go projects on
  GitHub are toy-status. Spake2+ is not available as a pure-Go
  library. AES-CCM is not in stdlib. Realistic effort 12–16 weeks
  solo, not the 7 weeks in the earlier SPEC estimate.

### Option B — matter.js sidecar (Node.js, Apache-2.0)

Daemon spawns and supervises a `matter.js` sidecar; communication via
local-loopback WebSocket / JSON-RPC; daemon stays the single
configuration / observability surface.

- **Pros**: mature implementation, broad cluster coverage, BLE
  commissioning available, certification-test-suite-aware.
  Time-to-v1.1 ≈ 4–6 weeks.
- **Cons**: adds a Node.js runtime to every distribution channel,
  ~50–100 MB RSS on the CCU Addon path (precedent exists via redmatic
  / Node-RED but it's still meaningful), introduces a third API
  surface (the sidecar IPC) that must be versioned and tested,
  doubles the security-patching pipeline, breaks the single-binary
  build output. Process-supervision and crash-recovery code becomes a
  real package, not boilerplate.

### Option C — connectedhomeip via CGo

Reference SDK linked into the daemon.

- **Pros**: certified reference impl.
- **Cons**: violates the no-CGo rule (would require a separate ADR
  rolling back ADR 0001's spirit), ARM cross-compile of the C++ tree
  is painful, license (Apache-2.0) is fine but the dep tree is large.
  Not a serious option for this project.

### Option D — rs-matter sidecar

Rust sidecar instead of Node.js.

- **Pros**: smaller footprint than Node, Apache/MIT.
- **Cons**: less mature than matter.js, second toolchain (Rust for
  arm/v7) added to release pipeline, all of Option B's downsides
  except the resident-memory size.

## Decision

Adopt **Option A: pure Go, hand-rolled, with a tightly prioritized
cluster subset**.

The subset (§5) is small enough that the substrate cost (the bulk of
the work) is no longer disproportionate to the cluster yield — once the
TLV / MRP / Secure-Channel / IM substrate exists, adding clusters is
schema-mapping work measured in days, not weeks.

**Commissioning is on-network only in v1.1.** No BLE in v1.1. BLE
commissioning is the largest single cost driver outside the substrate
itself; deferring it removes a Bluetooth stack from scope and lets
v1.1 ship.

**Effort revision**: the earlier SPEC estimate of 7 weeks is replaced
by a realistic 12–16 weeks solo for v1.1 (pure-Go substrate + P0
clusters + on-network commissioning + chip-tool conformance smoke).
P1 and P2 clusters slot into v1.1.x point releases.

## Prioritized Matter cluster subset

The cluster set is the narrow superset of (clusters with a non-trivial
Homematic mapping) ∩ (Matter clusters HA / Apple / Google bridge UIs
actually consume today). Cluster IDs and revisions from Matter 1.5.1
spec; the cluster shapes for the listed clusters are stable between
1.3 and 1.5.1, so the table below remains accurate.

### Tier P0 — v1.1 GA

Required for the four device families the user identified (Light,
Switch, Cover, Thermostat) plus the support clusters they need to be
useful in HA / Apple Home / Google Home.

| ID | Cluster | Required for |
|---|---|---|
| 0x0006 | **On/Off** | Switch, Light (all variants), Siren |
| 0x0008 | **Level Control** | Light (dimmer + ColorTemp + RGBW), Cover.Modulating-Valve fallback |
| 0x0102 | **Window Covering** | Cover, Blind, Garage |
| 0x0201 | **Thermostat** | Climate |
| 0x0204 | **Thermostat User Interface Configuration** | Climate (temp display unit, lock) |
| 0x0402 | **Temperature Measurement** | Climate, Generic temperature sensors |
| 0x0405 | **Relative Humidity Measurement** | Climate (when humidity present), Generic humidity sensors |
| 0x0029 | **OTA Software Update Requestor** | Mandatory descriptor for bridged endpoints (no real OTA — stub) |
| 0x002F | **Power Source** | Battery-powered bridged devices |
| 0x0030 | **General Commissioning** | Required for any Matter node |
| 0x0031 | **Network Commissioning** | Operational (Wi-Fi / Ethernet on-network) |
| 0x0032 | **Diagnostic Logs** | Required for certification posture, even if minimal |
| 0x0033 | **General Diagnostics** | Required |
| 0x003E | **Operational Credentials** | NOC / Fabric handling |
| 0x003F | **Group Key Management** | Required by Matter core |
| 0x001D | **Descriptor** | Required on every endpoint |
| 0x001E | **Binding** | Required on every endpoint |
| 0x0028 | **Basic Information** | Required on root endpoint |
| 0x0039 | **Bridged Device Basic Information** | Required on every bridged endpoint |

### Tier P1 — v1.1.x point releases (high yield, low marginal cost)

Cluster mappings against existing generic/calculated DPs that "fall
out" once the substrate is in place.

| ID | Cluster | Maps from |
|---|---|---|
| 0x0300 | **Color Control** | ColorLight (HS), ColorTempLight (CT mode), RGBWLight |
| 0x0406 | **Occupancy Sensing** | Generic.BinarySensor on `MOTION` / `MOTION_DETECTION_ACTIVE` |
| 0x0400 | **Illuminance Measurement** | Generic sensor on `ILLUMINATION` / `CURRENT_ILLUMINATION` / `BRIGHTNESS` |
| 0x0045 | **Boolean State** | Generic.BinarySensor (door/window contact, leak, generic alarm) |
| 0x005C | **Smoke CO Alarm** | siren.SmokeSiren, calculated `SMOKE_ALARM` |
| 0x0101 | **Door Lock** | lock.Lock |

### Tier P2 — v1.2+ (lower yield, more code per cluster)

| ID | Cluster | Maps from |
|---|---|---|
| 0x0403 | **Pressure Measurement** | Generic sensor on `AIR_PRESSURE` |
| 0x040D | **Carbon Dioxide Concentration** | Generic sensor on `CONCENTRATION` (CO2 instances only) |
| 0x042A | **PM2.5 Concentration Measurement** | Generic sensor on `MASS_CONCENTRATION_PM_2_5_24H_AVERAGE` |
| 0x042D | **PM10 Concentration Measurement** | Generic sensor on `MASS_CONCENTRATION_PM_10_24H_AVERAGE` |
| 0x0090 | **Electrical Power Measurement** | Generic sensor on `POWER` / `CURRENT` / `VOLTAGE` / `FREQUENCY` |
| 0x0091 | **Electrical Energy Measurement** | Generic sensor on `ENERGY_COUNTER` / `ENERGY_COUNTER_FEED_IN` |
| 0x003B | **Switch (momentary)** | Generic.Action (BUTTON channels, PRESS_*) |

### Out of Matter scope (no v1.x mapping)

| Custom DP | Reason |
|---|---|
| `siren.Siren` (full acoustic + optical) | Matter has no Siren cluster richer than Boolean State + OnOff; full feature parity isn't expressible. Maps to P1 Boolean State + 0x0006 OnOff for "alarm-on/off" only. |
| `siren.SoundPlayer` | No Speaker / audio-playback cluster in Matter 1.5.1. Stays MQTT-only. |
| `textdisplay.TextDisplay` | No display cluster in Matter. Stays MQTT-only. |
| `valve.Irrigation`, `valve.Modulating` | No matching cluster. Modulating valves degenerate to Level Control + OnOff if we want to expose them; Irrigation has no clean mapping. Park. |
| `light.EffectLight`, RGBW effect-mode | Color Control 0x0300 has scenes but no general "effect playlist". Effects expose a P1 read-only attribute via Color Control's `EnhancedColorMode`; full effect dispatch is MQTT/REST-only. |
| `light.DRGDaliLight` | Maps as a regular dimmable Light + Color Control if RGBW; no DALI-specific surface. |
| Calculated `INTRUSION_ALARM`, `WINDOW_OPEN` | Both are derived booleans → Boolean State 0x0045 (P1). Listed here to call out that they're not separate clusters. |

## Custom / Generic / Calculated DP ↔ Matter cluster mapping table

### Guiding principle: rich model, dumb bridge

Following ADR 0011 (MQTT) and ADR 0010 (HA Discovery), **the projection
from a DataPoint to a Matter endpoint/cluster lives on the DataPoint
itself, not in the bridge.** The bridge knows Matter wire format; the
model knows what each DP *means* in Matter terms.

Concretely:

- A Custom DP declares its Matter device type and the cluster set it
  contributes via interface methods on the Go type — same pattern as
  `payload.HAComponent()` for MQTT (see
  `internal/model/custom/light/topology.go`).
- A Generic DP declares its Matter measurement class via a typed
  classifier (the same `ParameterClass` already used by the MQTT
  payload-routing layer), so a temperature-Sensor and a
  humidity-Sensor are not switch-statement-matched by name in the
  bridge.
- A Calculated DP declares its target cluster + attribute on the
  calculated-DP definition next to the formula, so adding a new
  calculated sensor does not require touching the bridge.
- The Matter bridge is a generic transport: TLV codec, MRP framing,
  Secure Channel, IM dispatcher. It owns no cluster-attribute switch
  statements. It iterates the model and asks each DP what it
  projects to.

The mapping table below is therefore the *specification* of those
in-model declarations — every row corresponds to a method/declaration
that lives in the corresponding model package, not in
`internal/north/matter/`. Every DP type currently in
`internal/model/{custom,generic,calculated}` is accounted for.
"Endpoint type" is the Matter Bridged Device Type the source DP
materialises as.

### Custom DPs — bridged-device endpoint per `*Channel`

| Custom DP (Go type) | Matter endpoint type | Cluster set on endpoint | DP method → attribute / command |
|---|---|---|---|
| `switch.Switch` | OnOffPlugInUnit (0x010A) or OnOffLight (0x0100) per channel role | OnOff (0x0006), Descriptor, BridgedDeviceBasicInformation | `IsOn()` ↔ `OnOff` attr; `OnEvent(true/false)` ↔ `On`/`Off` cmd; `TurnOnFor(d)` ↔ `OnWithTimedOff` cmd |
| `light.Light` (dimmer) | DimmableLight (0x0101) | OnOff, LevelControl (0x0008), Descriptor | `IsOn()` ↔ OnOff; `Brightness()` (0–255) ↔ `CurrentLevel`; `SetBrightness` ↔ `MoveToLevelWithOnOff` |
| `light.ColorTempLight` | ColorTemperatureLight (0x010C) | OnOff, LevelControl, ColorControl (0x0300, CT mode), Descriptor | `Kelvin()` ↔ `ColorTemperatureMireds` (1e6/Kelvin); `SetKelvin` ↔ `MoveToColorTemperature` |
| `light.ColorLight` | ExtendedColorLight (0x010D) | OnOff, LevelControl, ColorControl (HS mode), Descriptor | `Color()` (hue,sat) ↔ `CurrentHue`/`CurrentSaturation`; `SetColor` ↔ `MoveToHueAndSaturation` |
| `light.RGBWLight` | ExtendedColorLight (0x010D) | OnOff, LevelControl, ColorControl (HS+CT modes via `EnhancedColorMode`), Descriptor | `Mode()` ↔ `EnhancedColorMode`; `Kelvin`/`SetKelvin` as ColorTemp; `CurrentHsColor`/`SetColor` as HS; `Effects()`/`Effect()` exposed via vendor-specific list — **effect dispatch P3** |
| `light.FixedColorLight` | ExtendedColorLight (0x010D) | OnOff, LevelControl, ColorControl (HS mode, palette-quantised) | `Color()` ↔ closest HS approximation per fixed-palette entry; round-trip writes snap to palette |
| `light.EffectLight` | DimmableLight (0x0101) for the OnOff+Level part | OnOff, LevelControl | Effect attribute exposed as MQTT-only; **no Matter cluster covers effect dispatch** |
| `light.DRGDaliLight` | DimmableLight (0x0101) | OnOff, LevelControl | DALI is a transport detail; surfaces identically to Light |
| `light.SoundPlayerLED` | DimmableLight (0x0101) | OnOff, LevelControl | LED on a sound device; sound playback is MQTT-only |
| `cover.Cover` | WindowCovering (0x0202) | WindowCovering (0x0102), Descriptor | `Position()` ↔ `CurrentPositionLiftPercent100ths` (inverted: HM 0=closed↔Matter 100%=closed convention); `SetPosition`/`Open`/`Close`/`Stop` ↔ `GoToLiftPercentage`/`UpOrOpen`/`DownOrClose`/`StopMotion` |
| `cover.Blind` | WindowCovering (0x0202, type=Tilt+Lift) | WindowCovering (0x0102) | Lift as `Cover`; `TiltPosition()` ↔ `CurrentPositionTiltPercent100ths`; `SetTilt` ↔ `GoToTiltPercentage`; `SetCombined` issues both |
| `cover.Garage` | WindowCovering (0x0202, type=TiltOnly is wrong → use Lift, EndProductType=GarageDoor) | WindowCovering | Door state derives from lift position thresholds; `Open`/`Close` direct |
| `climate.Climate` | Thermostat (0x0301) | Thermostat (0x0201), ThermostatUserInterfaceConfiguration (0x0204), TemperatureMeasurement (0x0402), RelativeHumidityMeasurement (0x0405 *if* humidity-DP present), Descriptor | `Setpoint()` ↔ `OccupiedHeatingSetpoint` (or Cooling per `HeatingCooling`); `CurrentTemperature()` ↔ `LocalTemperature` (and 0x0402 `MeasuredValue`); `Mode()` ↔ `SystemMode` (Auto/Heat/Cool/Off) + Boost / Away as `RunningMode` overlays via Thermostat 1.5.1 features; `Profile()` (week-program) — Matter 1.5.1 has Schedules cluster (0x0024) — **deferred to P2**; `Humidity()` ↔ 0x0405 `MeasuredValue` |
| `lock.Lock` | DoorLock (0x000A) | DoorLock (0x0101), Descriptor | `IsLocked()` ↔ `LockState`; `Lock`/`Unlock` ↔ `LockDoor`/`UnlockDoor`; `Open()` ↔ `UnboltDoor` (1.3); `IsJammed()` ↔ `LockOperationError` event |
| `siren.Siren` | OnOffPlugInUnit (0x010A) **+** vendor-specific | OnOff, BooleanState (0x0045) | `IsActive()` ↔ BooleanState `StateValue`; `TurnOn`/`TurnOff` ↔ OnOff; tone/optical selections **MQTT-only** |
| `siren.SmokeSiren` | SmokeCOAlarm (0x0076) | SmokeCOAlarm (0x005C), Descriptor | `Status()` ↔ `SmokeState` / `ExpressedState`; `IsActive()` ↔ `SmokeState` ≠ Normal; `IsPrimaryAlarm`/`IsSecondaryAlarm` distinguish via `ExpressedState` |
| `siren.SoundPlayer` | — (no endpoint type) | — | No Matter cluster; stays MQTT-only |
| `valve.Irrigation` | — (no endpoint type) | — | No Matter cluster; stays MQTT-only |
| `valve.Modulating` | OnOffPlugInUnit (0x010A) optional + LevelControl | OnOff, LevelControl | Degenerates to a dimmable on/off if exposed; flagged P3 |
| `textdisplay.TextDisplay` | — | — | No Matter cluster; stays MQTT-only |

### Generic DPs — endpoint contribution per `*Channel`

Generic DPs are *attribute carriers*, not endpoint types of their own.
The bridge groups them by `(channelAddress, parameterClass)` and
materialises them as additional clusters on the channel's endpoint, or
as standalone sensor endpoints when the channel has no Custom DP.

| Generic DP | Maps via Parameter | Matter endpoint (when standalone) | Cluster + attribute |
|---|---|---|---|
| `Sensor[float64]` | `ACTUAL_TEMPERATURE`, `TEMPERATURE` | TemperatureSensor (0x0302) | TemperatureMeasurement (0x0402) `MeasuredValue` (×100 scale) |
| `Sensor[float64]` | `HUMIDITY`, `ACTUAL_HUMIDITY` | HumiditySensor (0x0307) | RelativeHumidityMeasurement (0x0405) `MeasuredValue` (×100 scale) |
| `Sensor[float64]` | `ILLUMINATION`, `CURRENT_ILLUMINATION`, `BRIGHTNESS` | LightSensor (0x0106) | IlluminanceMeasurement (0x0400) `MeasuredValue` (log-scaled per spec) |
| `Sensor[float64]` | `AIR_PRESSURE` | (no standard sensor type) | PressureMeasurement (0x0403) — **P2** |
| `Sensor[float64]` | `CONCENTRATION` (CO2-tagged), `MASS_CONCENTRATION_PM_*` | AirQualitySensor (0x002C) | CO2Concentration (0x040D), PM2.5 (0x042A), PM10 (0x042D) — **P2** |
| `Sensor[float64]` | `WIND_SPEED`, `WIND_DIRECTION`, `RAIN_COUNTER`, `SUNSHINEDURATION` | — | No matching Matter cluster; **MQTT-only** |
| `Sensor[float64]` | `POWER`, `CURRENT`, `VOLTAGE`, `FREQUENCY` | — (added to OnOff endpoint) | ElectricalPowerMeasurement (0x0090) — **P2** |
| `Sensor[float64]` | `ENERGY_COUNTER`, `ENERGY_COUNTER_FEED_IN` | — (added to OnOff endpoint) | ElectricalEnergyMeasurement (0x0091) — **P2** |
| `Sensor[int32]` | `LEVEL`, `VALVE_STATE` (when not aggregated by Climate) | — | LevelControl (0x0008) `CurrentLevel` — context-dependent |
| `Sensor[string]` | any | — | No Matter cluster carries opaque strings; **MQTT-only** |
| `BinarySensor` | `STATE` (door/window), `SABOTAGE`, `OPEN` | ContactSensor (0x0015) | BooleanState (0x0045) `StateValue` |
| `BinarySensor` | `MOTION`, `MOTION_DETECTION_ACTIVE` | OccupancySensor (0x0107) | OccupancySensing (0x0406) `Occupancy` bitmap, `OccupancySensorType=PIR` |
| `BinarySensor` | `WATER` (leak) | WaterFreezeDetector / WaterLeakDetector (0x0041 / 0x0043) | BooleanState (0x0045) `StateValue` — endpoint type 0x0041/0x0043 wraps the same cluster |
| `BinarySensor` | `LOWBAT`, `LOW_BAT` | — (added to host endpoint) | PowerSource (0x002F) `BatChargeLevel` ∈ {OK,Warning,Critical} |
| `BinarySensor` | `UNREACH`, `STICKY_UNREACH`, `CONFIG_PENDING`, `UPDATE_PENDING` | — | No Matter cluster; surfaces via `BridgedDeviceBasicInformation.Reachable` (UNREACH) and Diagnostic Logs |
| `Switch` (writable bool) | `STATE`, channel-config booleans | — (or OnOffPlugInUnit standalone) | OnOff (0x0006) — typically rolled up by `custom.Switch` |
| `Float`, `Integer` (writable) | `LEVEL`, `SETPOINT`, `TEMPERATURE_OFFSET` | — | Routed through the hosting Custom DP's cluster (LevelControl / Thermostat); standalone numeric writers have no Matter equivalent |
| `Select` (enum write) | `CONTROL_MODE`, `BOOST_MODE`, `HEATING_COOLING`, `OPERATING_MODE`, `WEEK_PROGRAM_POINTER` | — | Routed through Thermostat / WindowCovering attributes; no generic enum cluster in Matter |
| `Button` (read-only key event) | `PRESS_SHORT`, `PRESS_LONG`, `PRESS`, `PRESS_LONG_*` | GenericSwitch (0x000F) | Switch (0x003B) `InitialPress` / `LongPress` / `ShortRelease` events — **P2** |
| `Action` (event-only) | `PRESS_SHORT`, `PRESS_LONG`, etc. | GenericSwitch (0x000F) | Switch (0x003B) — same as Button. The variants `ActionBoolean` / `ActionNumber` / `ActionSelect` / `ActionString` carry payload that has no Matter shape; surface remains MQTT-only |
| `Text` (writable string) | `DISPLAY_DATA_STRING`, `PROGRAM` | — | No Matter cluster; **MQTT-only** |
| `Dummy` | — | — | Test/internal; never bridged |

### Calculated DPs — derived sensors, no own endpoint

Calculated DPs are virtual sensors materialised on the *source*
channel's endpoint (or rolled up to the device's first sensor endpoint
when no channel applies). They do not introduce new endpoints.

| CalculatedParameter | Matter cluster + attribute | Tier |
|---|---|---|
| `APPARENT_TEMPERATURE` | TemperatureMeasurement (0x0402) `MeasuredValue` on a synthetic "feels-like" endpoint, or vendor-specific overlay | P1 (single attribute on existing temp endpoint) |
| `DEW_POINT` | TemperatureMeasurement on synthetic endpoint | P1 |
| `DEW_POINT_SPREAD` | — (delta has no clean cluster) | **MQTT-only** |
| `FROST_POINT` | TemperatureMeasurement on synthetic endpoint | P1 |
| `ENTHALPY` | — (J/kg has no Matter unit) | **MQTT-only** |
| `VAPOR_CONCENTRATION` | RelativeHumidityMeasurement equivalent? No — absolute humidity has no Matter cluster | **MQTT-only** |
| `OPERATING_VOLTAGE_LEVEL` | PowerSource (0x002F) `BatPercentRemaining` (×2 to match Matter's half-percent encoding) | P1 |
| `INTRUSION_ALARM` | BooleanState (0x0045) `StateValue` (1 = alarm) on synthetic endpoint | P1 |
| `SMOKE_ALARM` | SmokeCOAlarm (0x005C) `SmokeState` ∈ {Normal, Warning, Critical} | P1 |
| `WINDOW_OPEN` | BooleanState (0x0045) `StateValue` on synthetic endpoint with EndpointType=ContactSensor | P1 |

## Implementation strategy

### Repository layout

Following the SPEC §6.1 placeholder direction and the rich-model
principle (§6 introduction), the work splits into two layers:

**Bridge layer — generic Matter substrate, no domain knowledge:**

```
internal/north/matter/
├── server/                — fabric, sessions, message dispatcher
├── transport/             — UDP/IPv6, MRP, message framing
├── secure/                — Spake2+ (PASE), Sigma (CASE), session keys
├── tlv/                   — TLV codec
├── im/                    — Interaction Model (Read/Write/Subscribe/Invoke/Report)
├── cluster/               — cluster *protocol* impls (wire format, attr/cmd IDs)
│   ├── onoff/             — encode/decode OnOff cluster, dispatch by attr/cmd ID
│   ├── levelcontrol/
│   ├── colorcontrol/
│   ├── windowcovering/
│   ├── thermostat/
│   ├── booleanstate/
│   ├── occupancysensing/
│   ├── tempmeasurement/
│   ├── humiditymeasurement/
│   ├── illuminancemeasurement/
│   ├── doorlock/
│   ├── smokecoalarm/
│   ├── powersource/
│   └── core/              — Descriptor, Binding, BasicInfo, BridgedDeviceBasicInfo, GeneralCommissioning, NetworkCommissioning, GeneralDiagnostics, OperationalCredentials, GroupKeyMgmt, OTARequestor (stub)
├── endpoint/              — endpoint topology assembly: walks the model, asks each DP for its projection, wires cluster servers
├── commissioning/         — on-network discovery, attestation, fabric join
├── mdns/                  — DNS-SD operational + commissionable
├── attestation/           — DAC/PAI/PAA chain validation
└── store/                 — fabric / NOC / shared-secrets persistence (SQLite-backed via internal/store)
```

The cluster packages contain only the *protocol* — TLV encoding of
attribute reads, command dispatch tables, report shape — and call
back into the model via the source interfaces below to fetch values
or apply writes.

**Model layer — declares what each DP projects to:**

```
internal/model/
├── custom/
│   ├── switch/    matter.go   — declares OnOffPlugInUnit + OnOff cluster projection
│   ├── light/     matter.go   — Light/ColorTemp/Color/RGBW/FixedColor projections
│   ├── cover/     matter.go   — Cover/Blind/Garage projections (Lift, Lift+Tilt, EndProductType)
│   ├── climate/   matter.go   — Thermostat + UI-Config + Temp/Humidity composition
│   ├── lock/      matter.go   — DoorLock projection
│   └── siren/     matter.go   — OnOff + BooleanState (Siren) / SmokeCOAlarm (SmokeSiren)
├── generic/
│   └── matter.go              — ParameterClass → Matter measurement-cluster routing
└── calculated/
    └── matter.go              — per-CalculatedParameter cluster + attribute declaration
```

`pkg/interfaces/matter.go` ceases to be a placeholder and gains the
source-surface port shapes (next subsection).

### Source surface (Go interfaces)

Mirrors the ADR 0011 pattern (`payload.HAEntity`, `payload.Slotted`,
`payload.DiscoveryDynamic`). Lives in `pkg/interfaces` because both
the bridge (`internal/north/matter`) and the model
(`internal/model/...`) need to declare dependencies on the same
types.

```go
// matter.EndpointSource is implemented by Custom DPs that materialise
// as their own bridged endpoint. The endpoint assembler walks the
// model and creates one Matter endpoint per implementer.
type EndpointSource interface {
    // MatterDeviceType is the Matter Device Type ID
    // (e.g., 0x010A OnOffPlugInUnit, 0x0301 Thermostat).
    MatterDeviceType() uint16

    // MatterClusterServers returns the cluster-server contributions
    // this DP exposes on its endpoint. Order does not matter; the
    // endpoint assembler deduplicates.
    MatterClusterServers() []ClusterServer
}

// matter.ClusterServer is implemented by anything that contributes a
// Matter cluster — a Custom DP, a Generic DP grouped onto an
// endpoint, or a Calculated DP exposing a derived attribute.
type ClusterServer interface {
    // MatterClusterID identifies the cluster (e.g., 0x0006 OnOff).
    MatterClusterID() uint32

    // MatterRead resolves an attribute value at read time. The
    // returned value is in the cluster's native type (bool, uint8,
    // int16, etc.); the bridge handles TLV encoding.
    MatterRead(attrID uint32) (value any, ok bool)

    // MatterWrite applies an attribute write. The bridge has already
    // decoded the TLV; `value` is the cluster-native type.
    MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error

    // MatterInvoke dispatches a cluster command. `fields` is the
    // cluster-native struct for the command's request payload.
    MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (response any, err error)

    // MatterReportable lists attribute IDs that emit reports when
    // the underlying DP fires OnEvent. Empty slice = none.
    MatterReportable() []uint32
}

// matter.MeasurementClass classifies Generic.Sensor / BinarySensor
// instances by Matter cluster without name-matching. The model layer
// computes this once at materialisation; the bridge consumes it.
type MeasurementClass int

const (
    MeasurementNone MeasurementClass = iota
    MeasurementTemperature        // 0x0402
    MeasurementHumidity           // 0x0405
    MeasurementIlluminance        // 0x0400
    MeasurementPressure           // 0x0403
    MeasurementCO2                // 0x040D
    MeasurementPM25               // 0x042A
    MeasurementPM10               // 0x042D
    MeasurementOccupancy          // 0x0406
    MeasurementContact            // 0x0045 (BooleanState, ContactSensor endpoint)
    MeasurementLeak               // 0x0045 (BooleanState, WaterLeakDetector endpoint)
    MeasurementBattery            // 0x002F (PowerSource)
    MeasurementPower              // 0x0090
    MeasurementEnergy             // 0x0091
    MeasurementMomentarySwitch    // 0x003B (Button/Action events)
)

// matter.MeasurementSource is implemented by Generic / Calculated DPs
// that project to a single Matter measurement cluster. The bridge
// uses this to either build a standalone sensor endpoint or attach an
// extra cluster to an existing host endpoint.
type MeasurementSource interface {
    MatterMeasurementClass() MeasurementClass
}
```

Compile-time assertions in each model package (`var _ matter.EndpointSource = (*Light)(nil)`)
keep the model honest as the bridge layer evolves — the same
discipline ADR 0011 introduced for `payload.HAEntity` /
`payload.Slotted`.

### Crypto strategy (the three real risks)

| Need | Plan |
|---|---|
| **TLV codec** | Hand-roll. ~1k LOC, fully covered by Matter Core Spec §A.7. Zero dependencies. |
| **AES-CCM-128** | Hand-roll on top of `crypto/aes`. ~150 LOC. Stdlib `crypto/cipher` provides only GCM. Alternative `github.com/pschlump/aes-ccm` (BSD-3) is acceptable but vendoring our own keeps the audit surface tighter. |
| **Spake2+ (PASE)** | Port from `connectedhomeip/src/crypto/CHIPCryptoPALmbedTLS.cpp`. Use `crypto/elliptic` P-256 + `crypto/sha256` + HKDF (`golang.org/x/crypto/hkdf`). ~500 LOC. **Highest implementation risk.** Mitigation: chip-tool conformance vectors as golden tests. |
| **Sigma (CASE)** | Standard ECDH on P-256 (stdlib) + HKDF + AES-CCM. Lower risk than Spake2+ once that's done. |
| **DAC/PAI/PAA chain validation** | `crypto/x509` covers 95 %. Matter-specific OIDs (vendor ID, product ID, certification declaration) decoded with `encoding/asn1`. |
| **mDNS / DNS-SD** | `github.com/grandcat/zeroconf` (Apache-2.0, pure Go) for discovery, with our own service-record assembly for advertising the bridge — the library's advertise side has known quirks on multi-interface hosts and is worth replacing. |

### Forward-compat verification (replaces the SPEC §6.3 promise with measurement)

- Every Custom DP has a non-empty mapping in §6 → SPEC §6.3's
  `DataPointValueChangedEvent` / `DeviceCreatedEvent` /
  `DeviceRemovedEvent` shape *is* sufficient for endpoint
  build/teardown. **Confirmed.**
- Generic DP Parameter→Cluster routing rides on the same
  `ParameterClass` classifier already used by `internal/payload`
  (MQTT routing). The Matter `MeasurementClass` enum (§"Source
  surface") is computed from `ParameterClass` once at materialisation
  by the model layer — no per-publish lookup, no name-matching in the
  bridge. **Confirmed.**
- Calculated DPs sit on top of the same `OnEvent` plumbing as Generic
  sensors. Each calculated parameter declares its
  `MatterMeasurementClass()` on the Go type next to the formula, so
  adding a new calculated sensor is a model-package change, not a
  bridge-package change. **Confirmed.**

No model-layer *structural* refactor is required. The forward-compat
work is additive: each model package gains a `matter.go` file
implementing the source-surface interfaces from §"Source surface".
The Matter bridge itself owns no DP-specific code — it iterates the
model and dispatches via the interfaces.

## Risks

1. **Spake2+ correctness** (highest). The verifier is a one-author
   cryptographic implementation. Mitigation: port from the CHIP
   reference and lock against chip-tool's published test vectors as
   golden tests; gate releases on a `chip-tool pairing` smoke run.
2. **Spec drift**: Matter 1.3 / 1.4 / 1.5 introduce new mandatory
   attributes. Mitigation: pin to **Matter 1.5.1** for v1.1.0.
   Rationale: (a) Apple Home / Google Home / SmartThings have moved
   aggressively to 1.4/1.5, so a 1.3-only bridge would surface as
   version-mismatch warnings; (b) the clusters this bridge actually
   emits (OnOff, LevelControl, ColorControl, WindowCovering,
   Thermostat, DoorLock, BooleanState, BasicInformation,
   BridgedDeviceBasicInformation, Descriptor, the measurement
   clusters) have stable mandatory-set shapes between 1.3 and 1.5.1
   — the 1.4/1.5 expansions land in Energy / EVSE / Cameras, all
   out-of-scope. (c) The protocol substrate (Spake2+, CASE / Sigma,
   AES-CCM, IM, MRP) is unchanged since 1.0. Cluster servers carry
   their 1.5.1 ClusterRevision; BasicInformation.SpecificationVersion
   advertises 0x01050100. Never advertise an unsupported version.
3. **Endpoint topology size**: a CCU with 200 devices × ~3 channels =
   ~600 bridged endpoints. Matter spec allows 256 endpoints per node
   by default; the bridge may need to advertise multiple bridge nodes
   per CCU. Mitigation: stress-test the assembler at the largest
   known fleet; open ADR 0012a only if topology partitioning becomes
   necessary.
4. **HA Matter Server divergence**: Home Assistant's Matter Server
   has bridge-specific quirks (e.g. it sometimes ignores
   `Reachable=false` for fast-cycling sensors). Mitigation: HA +
   Apple Home + Google Home all run as conformance targets in the
   release checklist; ship-blocking issues are filed against the
   controller, not papered over.
5. **Effort underrun**: solo full-time estimate is 12–16 weeks; at
   the project's part-time pace expect 2×. Mitigation: phase
   boundaries are independently shippable — substrate plus chip-tool
   commissioning is a demoable milestone before any cluster work.

## Consequences

### Positive

- Single static binary preserved across all three distribution
  channels. No second runtime on CCU3 hardware.
- Architecture stays uniform: Matter is a north-bound adapter on the
  same EventBus as MQTT and REST. Forward-compat from SPEC §6.3 is
  verified by §6 mapping, not assumed.
- Cluster prioritisation matches the actual Homematic fleet —
  no "we built it but nothing maps to it" clusters in v1.1.

### Negative

- Significantly higher implementation cost than Option B (15 weeks
  vs. 4–6). The earlier SPEC estimate was wrong; this ADR records
  the right number.
- Spake2+ is a one-author cryptographic implementation. That carries
  audit weight (see §Risks). Counter-balance: the alternative
  (matter.js) is also single-vendor and not formally audited either.
- Effects, sound players, irrigation valves, and text displays do not
  surface to Matter — Matter has no clusters for them. The MQTT bridge
  remains the only path for those features. We document this clearly
  in the user-facing v1.1 release notes.

### Migration

- `pkg/interfaces/matter.go` graduates from placeholder to public
  port surface (cluster source-surface contracts).
- `internal/north/matter/` is created; SPEC §6.1's prior prohibition
  is lifted for the v1.1 line.
- `assets/openapi.yaml` gains `/api/v1/matter/...` endpoints (fabrics
  list, setup-payload, open-commissioning-window) under feature flag
  `matter.enabled`. Each route returns `503 service_unready` when the
  bridge is disabled.
- The conformance test corpus lives at
  `internal/north/matter/conformance/` (revised from the original
  `tests/conformance/matter/` plan to keep the corpus next to the
  packages it exercises).

## Follow-ups

- A separate ADR (0012a) is opened *only if* the endpoint-count
  ceiling (Risk #3) forces multi-bridge topology partitioning. Until
  then the assembler's stress-tested 600-endpoint capacity covers
  every fleet observed.
