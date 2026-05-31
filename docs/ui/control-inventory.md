# CCU CONTROL inventory

Every CCU paramset descriptor carries a `CONTROL` attribute of the form
`<WIDGET_FAMILY>.<SLOT>`. The family names below were extracted from
`../occu/` (eQ-3 HMSL — non-commercial; we use only the identifier
strings, which are factual interface metadata, not creative content).

The list is the union over RF firmware (`occu/firmware/rftypes/*.xml`),
the CCU WebUI source (`occu/WebUI/www/rega/esp/controls/*.fn` —
informational reference only, no code reproduced here), and direct
`grep` over the rest of the OCCU source tree.

Regenerate with:

```sh
grep -rhoE 'control="[A-Z][A-Z_]*\.[A-Z_]+"' ../occu/ | sort -u
```

## Families

| Family | Widget intent | Typical slots |
|---|---|---|
| `ACCELERATION_TRANSCEIVER` | acceleration sensor | `ABSOLUTE_ANGLE`, `ABSOLUTE_ANGLE_STATUS`, `MOTION`, `STATE` |
| `ACCESSPOINT_GENERIC_RECEIVER` | HmIP-AP generic readout | `CURRENT`, `VOLTAGE` and `*_STATUS` siblings |
| `ACCESS_RECEIVER` / `ACCESS_TRANSCEIVER` | access-control endpoint | `ACCESS_AUTHORIZATION`, `STATE` |
| `ACOUSTIC_DISPLAY_RECEIVER` | RC19 LCD-with-sound | `COMBINED_PARAMETER` |
| `ACOUSTIC_SIGNAL_TRANSMITTER` / `ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER` | beep/siren-light | `LEVEL`, `LEVEL_REAL`, `OLD_LEVEL`, `SOUNDFILE` |
| `ALARM_SWITCH_VIRTUAL_RECEIVER` / `_ALARM_SWITCH_VIRTUAL_RECEIVER` | full siren | `ACOUSTIC_ALARM_SELECTION`, `OPTICAL_ALARM_SELECTION`, `DURATION_*`, `ZONE_*` |
| `ANALOG_INPUT` | analog-in sensor | `VOLTAGE` |
| `ARMING` | alarm system arm state | `ARMSTATE` |
| `AUTO_RELOCK_TRANSCEIVER` | relock door | `AUTO_RELOCK_STATE` |
| `BACKLIGHTING_RECEIVER` | wall-display backlight | `LEVEL`, `ON_TIME` |
| `BATTERIE` | battery low | `LOWBAT` |
| `BLIND` / `BLIND_TRANSMITTER` / `BLIND_VIRTUAL_RECEIVER` | shading drive | `LEVEL`, `LEVEL_SLATS`, `LEVEL_COMBINED`, `STOP` |
| `BRIGHTNESS_TRANSMITTER` | lux sensor | per-sensor BRIGHTNESS |
| `BTN_SHORT_ONLY` / `BUTTON` | physical button | `SHORT`, `LONG` |
| `CAPACITIVE_FILLING_LEVEL_SENSOR` | tank level | `FILLING_LEVEL` |
| `CARBON_DIOXIDE_RECEIVER` | CO₂ readout | per-sensor `CONCENTRATION` |
| `CLIMATECONTROL_FLOOR_TRANSCEIVER` / `CLIMATE_TRANSCEIVER` | floor-loop thermostat | `*_TEMP` slots |
| `COLORTEMP` | colour temperature only | per-slot |
| `COND_SWITCH_TRANSMITTER_TEMPERATURE` | conditional switch by temp | `*_TEMPERATURE` |
| `DANGER` | hazard sensor | `STATE` |
| `DEVICE` | device-level | `LOWBAT`, `SABOTAGE` |
| `DIGITAL_ANALOG_OUTPUT` | freq output | `FREQUENCY` |
| `DIGITAL_STATE` | bare on/off readout | `STATE` |
| `DIMMER` / `DIMMER_REAL` | continuous lamp | `LEVEL`, `LEVEL_REAL`, `OLD_LEVEL` |
| `DISTANCE_TRANSMITTER` | distance sensor | per-sensor reading |
| `DOOROPENER` | electric door release | `STATE` |
| `DOOR_LOCK_STATE_TRANSCEIVER` / `_TRANSMITTER` / `DOOR_LOCK_TRANSCEIVER` | door lock | `LOCK_STATE`, `DIRECTION`, `ERROR_JAMMED` |
| `DOOR_RECEIVER` / `DOOR_SENSOR` / `DOOR_STATE_TRANSCEIVER` | door contact | `STATE` |
| `DUAL_WHITE_BRIGHTNESS` / `DUAL_WHITE_COLOR` | tunable white | `LEVEL`, `COLOR_TEMPERATURE` |
| `EVENT_INTERFACE` | generic event source | `TRIGGER` |
| `FLOW_METER_TRANSMITTER` | water flow | per-meter reading |
| `GENERIC_INPUT_TRANSMITTER` / `GENERIC_MEASURING_TRANSMITTER` | generic input | per-sensor |
| `HEATING_CONTROL` | classic HM thermostat | `SETPOINT`, `TEMPERATURE`, `CONTROL_MODE`, `AUTO`, `MANU`, `BOOST`, `COMFORT`, `LOWERING`, `PARTY_*` |
| `HEATING_CONTROL_HMIP` | HmIP thermostat | `SETPOINT`, `TEMPERATURE`, `HUMIDITY`, `CONTROL_MODE`, `SETPOINT_MODE`, `BOOST_MODE`, `ACTIVE_PROFILE`, `FROST_PROTECTION`, `HEATING_COOLING`, `LEVEL`, `VALVE_STATE`, `WINDOW_STATE`, `PARTY_MODE`, `PARTY_SETPOINT_TEMP`, `PARTY_TIME_END`, `PARTY_TIME_START` |
| `IDENTIFICATION` | device identify | per-device |
| `JALOUSIE` | venetian blind | `LEVEL`, `LEVEL_SLATS`, `LEVEL_COMBINED`, `STOP` |
| `LOCK` | combined lock | `OPEN`, `STATE`, `UNCERTAIN` |
| `MAINTENANCE` | device service | per-device |
| `MOTIONDETECTOR_TRANSCEIVER` | motion sensor | per-channel |
| `OPTICAL_SIGNAL_RECEIVER` | LED indicator | per-channel |
| `POWERMETER` / `POWERMETER_IEC` / `POWERMETER_IGL` / `POWERMETER_PSM` | electrical meter | `POWER`, `ENERGY_COUNTER`, `VOLTAGE`, `CURRENT`, `FREQUENCY`, `BOOT`, `GAS_ENERGY_COUNTER`, `GAS_POWER` |
| `RAIN_DETECTION_TRANSMITTER` | rain sensor | per-sensor |
| `RC` / `RC19_*` | remote-control display | per-button |
| `RGBW_AUTOMATIC` | RGBW with program | `BRIGHTNESS`, `MAX_BORDER`, `MIN_BORDER`, `ON_TIME`, `PROGRAM`, `RAMP_TIME` |
| `RGBW_COLOR` / `RGB_COLOR` | RGBW colour control | `COLOR` |
| `RHS` | door/window-rotary handle | `STATE` |
| `SERVO_TRANSMITTER` / `SERVO_VIRTUAL_RECEIVER` | servo | per-channel |
| `SHUTTER_TRANSMITTER` / `SHUTTER_VIRTUAL_RECEIVER` | shutter | `LEVEL`, `STOP` |
| `SIMPLE_SWITCH_RECEIVER` | toggle | `STATE` |
| `SMOKE_DETECTOR` | smoke sensor | `STATE`, alarm-specific |
| `SOIL_MOISTURE_TRANSMITTER` | soil sensor | per-channel |
| `SWITCH` / `SWITCH_TRANSMITTER` | binary actor | `STATE` |
| `TEMP` / `TEMP_HUM_PARTICLE_MATTER_TRANSMITTER` | sensor cluster | per-channel |
| `UNIVERSAL_LIGHT_RECEIVER` | generic lamp | mixed |
| `WATER_DETECTION_TRANSMITTER` | leak sensor | `STATE` |
| `WATER_SWITCH` | water valve | `STATE` |
| `WEATHER_TRANSMIT` | weather station | per-channel |
| `WEEK_PROFILE` | weekly schedule | profile-specific |
| `WINDOW` / `WINDOW_DRIVE_RECEIVER` | window drive | `LEVEL`, `STOP`, `UNCERTAIN` |
| `WIN_SC` / `WIN_SC_SECURE` / `WIN_SC_SENSOR` | window contact / handle / drive | `STATE`, `LEVEL`, `HANDLE_LED_MODE`, `HANDLE_LOCK`, `RELEASE`, `STOP`, `TIPTRONIC_STATE`, `WINTER_MODE`, `WINDOW_TYPE` |

Total: 84 families.

## Slot semantics (per suffix)

Independent of family, the suffix carries a render hint:

| Suffix | Default widget |
|---|---|
| `LEVEL`, `LEVEL_*` | numeric slider (0–100 % typical) |
| `STATE` | boolean toggle / status badge |
| `SETPOINT`, `*_TEMP` | temperature stepper |
| `TEMPERATURE`, `HUMIDITY`, `BRIGHTNESS`, `POWER`, `VOLTAGE`, `CURRENT`, `FREQUENCY`, `ENERGY_COUNTER` | numeric readout with unit |
| `CONTROL_MODE`, `SETPOINT_MODE`, `MODE`, `ARMSTATE` | segmented selector |
| `BOOST_MODE`, `PARTY_MODE`, `FROST_PROTECTION`, `AUTO`, `MANU`, `COMFORT`, `LOWERING` | action button (toggle-on-press) |
| `SHORT`, `LONG` | event pulse indicator (read-only) |
| `STOP` | stop button (paired with `LEVEL` widget) |
| `OPEN` | momentary action button |
| `UNCERTAIN` | status badge ("unknown") |
| `COLOR` | colour picker |
| `COLOR_TEMPERATURE` | kelvin slider |
| `SOUNDFILE`, `ACOUSTIC_ALARM_SELECTION`, `OPTICAL_ALARM_SELECTION` | dropdown selector |
| `WINDOW_STATE`, `VALVE_STATE`, `HEATING_COOLING` | status badge |
| `PROGRAM`, `ACTIVE_PROFILE` | profile selector |
| `LOWBAT`, `SABOTAGE` | status badge in BridgedDeviceBasicInformation row |
| `*_TIME`, `PARTY_TIME_*`, `PARTY_TIME_END`, `RAMP_TIME`, `ON_TIME` | time picker |
| `*_YEAR`, `*_MONTH`, `*_DAY` | date picker components |
| `RESET`, `BOOT` | reset action |
| `FILLING_LEVEL` | gauge |
| `TRIGGER` | manual trigger button |

Unknown suffixes fall through to a generic ParameterField — the SPA's
existing renderer.

## Disambiguating same-suffix slots

Some suffixes appear in multiple families with different meanings. The
`(family, suffix)` pair always carries the disambiguating context:

| Suffix | Family-dependent meaning |
|---|---|
| `LEVEL` | `DIMMER` → brightness 0–1, `BLIND` → position 0–1, `WIN_SC` → handle-position enum |
| `STATE` | `SWITCH` → on/off, `DOOR_SENSOR` → open/closed, `DANGER` → alarm/normal |
| `COLOR` | `RGB_COLOR` → RGB tuple, `RGBW_COLOR` → RGBW |

The Svelte family widgets handle these via per-family slot maps —
neither the resolver nor the primitives need to know about the
semantics, only the family widget does.
