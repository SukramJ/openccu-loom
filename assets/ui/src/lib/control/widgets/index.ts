// Family-widget registry. Maps ControlFamily values to the Svelte
// widget that should render a channel resolved to that family.
// Unmapped families fall through to the caller's fallback (typically
// the generic ParameterField list).

import type { Component } from "svelte";
import type { ControlFamily } from "../families";
import type { ResolvedChannel } from "../resolver";

import BinarySensor from "./BinarySensor.svelte";
import Blind from "./Blind.svelte";
import ButtonEvent from "./ButtonEvent.svelte";
import ColorLight from "./ColorLight.svelte";
import ColorTempLight from "./ColorTempLight.svelte";
import Dimmer from "./Dimmer.svelte";
import FixedColorLight from "./FixedColorLight.svelte";
import Garage from "./Garage.svelte";
import Climate from "./Climate.svelte";
import Lock from "./Lock.svelte";
import Powermeter from "./Powermeter.svelte";
import Sensor from "./Sensor.svelte";
import SimpleRfThermostat from "./SimpleRfThermostat.svelte";
import Siren from "./Siren.svelte";
import Switch from "./Switch.svelte";
import UniversalLight from "./UniversalLight.svelte";

export type ControlWidgetProps = {
  resolved: ResolvedChannel;
  title: string;
  secondary?: string;
  /** Slot writer. Called with the slot's suffix and the value to send. */
  onSetSlot: (slot: string, value: unknown) => void;
};

/**
 * Every widget shares the same prop contract — `onSetSlot(slot, value)`
 * — so the registry can dispatch by family without per-widget adapters.
 * Read-only widgets (Sensor, BinarySensor, Powermeter, ButtonEvent)
 * accept the prop and ignore it.
 */
type WidgetEntry = Component<ControlWidgetProps>;

export const controlWidgets: Partial<Record<ControlFamily, WidgetEntry>> = {
  // SWITCH-shaped: STATE slot only.
  SWITCH: Switch as unknown as WidgetEntry,
  SWITCH_TRANSMITTER: Switch as unknown as WidgetEntry,
  SIMPLE_SWITCH_RECEIVER: Switch as unknown as WidgetEntry,
  WATER_SWITCH: Switch as unknown as WidgetEntry,
  DIGITAL_STATE: Switch as unknown as WidgetEntry,

  // DIMMER-shaped: LEVEL slot. The widget internally wires
  // onSetSlot("LEVEL", v) so the registry's contract holds.
  DIMMER: Dimmer as unknown as WidgetEntry,
  DIMMER_REAL: Dimmer as unknown as WidgetEntry,
  DUAL_WHITE_BRIGHTNESS: Dimmer as unknown as WidgetEntry,
  // UNIVERSAL_LIGHT_RECEIVER (HmIP-RGBW, HmIP-DRG-DALI) carries the
  // richest light surface in the CCU catalogue — brightness, HSV
  // colour, colour temperature and effects on a single channel.
  // UniversalLight reads each capability slot conditionally so a
  // simple HmIP-DRG-DALI channel with only LEVEL still works.
  UNIVERSAL_LIGHT_RECEIVER: UniversalLight as unknown as WidgetEntry,

  // BLIND-shaped: LEVEL + STOP slots.
  BLIND: Blind as unknown as WidgetEntry,
  BLIND_TRANSMITTER: Blind as unknown as WidgetEntry,
  BLIND_VIRTUAL_RECEIVER: Blind as unknown as WidgetEntry,
  JALOUSIE: Blind as unknown as WidgetEntry,
  SHUTTER_TRANSMITTER: Blind as unknown as WidgetEntry,
  SHUTTER_VIRTUAL_RECEIVER: Blind as unknown as WidgetEntry,
  WINDOW: Blind as unknown as WidgetEntry,
  WINDOW_DRIVE_RECEIVER: Blind as unknown as WidgetEntry,

  // Thermostats — Climate is slot-aware and covers both HmIP
  // (HEATING_CONTROL_HMIP, CONTROL_MODE writable integer) and RF
  // (HEATING_CONTROL, CONTROL_MODE read-only with separate AUTO /
  // BOOST / COMFORT / LOWERING action slots).
  HEATING_CONTROL_HMIP: Climate as unknown as WidgetEntry,
  HEATING_CONTROL: Climate as unknown as WidgetEntry,

  // Lock + door opener.
  LOCK: Lock as unknown as WidgetEntry,
  DOOROPENER: Lock as unknown as WidgetEntry,
  DOOR_LOCK_TRANSCEIVER: Lock as unknown as WidgetEntry,

  // Garage door (HmIP-MOD-HO, HmIP-MOD-TM) — DOOR_COMMAND + DOOR_STATE.
  DOOR_RECEIVER: Garage as unknown as WidgetEntry,

  // Power meters — all read-only variants share the same layout.
  POWERMETER: Powermeter as unknown as WidgetEntry,
  POWERMETER_IEC: Powermeter as unknown as WidgetEntry,
  POWERMETER_IGL: Powermeter as unknown as WidgetEntry,
  POWERMETER_PSM: Powermeter as unknown as WidgetEntry,

  // Read-only numeric sensors. Routed to the generic Sensor widget,
  // which iterates whatever slots the channel exposes and renders a
  // StatReadout grid. Per-family glyphs / units / labels remain
  // sensible defaults until family-specific overrides land.
  ANALOG_INPUT: Sensor as unknown as WidgetEntry,
  BRIGHTNESS_TRANSMITTER: Sensor as unknown as WidgetEntry,
  CARBON_DIOXIDE_RECEIVER: Sensor as unknown as WidgetEntry,
  DISTANCE_TRANSMITTER: Sensor as unknown as WidgetEntry,
  FLOW_METER_TRANSMITTER: Sensor as unknown as WidgetEntry,
  GENERIC_MEASURING_TRANSMITTER: Sensor as unknown as WidgetEntry,
  SOIL_MOISTURE_TRANSMITTER: Sensor as unknown as WidgetEntry,
  TEMP: Sensor as unknown as WidgetEntry,
  TEMP_HUM_PARTICLE_MATTER_TRANSMITTER: Sensor as unknown as WidgetEntry,
  WEATHER_TRANSMIT: Sensor as unknown as WidgetEntry,

  // Read-only binary sensors.
  BUTTON: ButtonEvent as unknown as WidgetEntry,
  BTN_SHORT_ONLY: ButtonEvent as unknown as WidgetEntry,
  DANGER: BinarySensor as unknown as WidgetEntry,
  DOOR_SENSOR: BinarySensor as unknown as WidgetEntry,
  DOOR_STATE_TRANSCEIVER: BinarySensor as unknown as WidgetEntry,
  MOTIONDETECTOR_TRANSCEIVER: BinarySensor as unknown as WidgetEntry,
  RAIN_DETECTION_TRANSMITTER: BinarySensor as unknown as WidgetEntry,
  RHS: BinarySensor as unknown as WidgetEntry,
  SMOKE_DETECTOR: BinarySensor as unknown as WidgetEntry,
  WATER_DETECTION_TRANSMITTER: BinarySensor as unknown as WidgetEntry,
  WIN_SC: BinarySensor as unknown as WidgetEntry,
  WIN_SC_SECURE: BinarySensor as unknown as WidgetEntry,
  WIN_SC_SENSOR: BinarySensor as unknown as WidgetEntry,

  // Device-level status (LOWBAT, SABOTAGE) — rendered with the
  // BinarySensor layout; the resolver picks the dominant slot.
  BATTERIE: BinarySensor as unknown as WidgetEntry,

  // Colour lighting.
  RGBW_COLOR: ColorLight as unknown as WidgetEntry,
  RGB_COLOR: ColorLight as unknown as WidgetEntry,
  COLORTEMP: ColorTempLight as unknown as WidgetEntry,
  DUAL_WHITE_COLOR: ColorTempLight as unknown as WidgetEntry,

  // Sirens + acoustic signal devices.
  ACOUSTIC_SIGNAL_TRANSMITTER: Siren as unknown as WidgetEntry,
  ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER: Siren as unknown as WidgetEntry,
  ALARM_SWITCH_VIRTUAL_RECEIVER: Siren as unknown as WidgetEntry,
  _ALARM_SWITCH_VIRTUAL_RECEIVER: Siren as unknown as WidgetEntry,

  // OPTICAL_SIGNAL_RECEIVER (HmIP-WRC6-230 channels 12-18,
  // HmIPW-WRC6 channels 7-13) is the RGB feedback LED on a wall
  // remote — the slot inventory matches HmIP-BSL exactly (LEVEL +
  // COLOR + COLOR_BEHAVIOUR + DURATION + RAMP_TIME), so the
  // fixed-colour light tile is the correct surface, not the
  // Siren widget the older mapping had it on.
  OPTICAL_SIGNAL_RECEIVER: FixedColorLight as unknown as WidgetEntry,

  // Continuous-write actuators that share the Dimmer LEVEL slot:
  // BACKLIGHTING wraps a wall-display backlight (0–100 %), SERVO
  // drives a generic positioner (e.g. a fan louver). Both write
  // LEVEL semantically the same way as a dimmer.
  BACKLIGHTING_RECEIVER: Dimmer as unknown as WidgetEntry,
  SERVO_TRANSMITTER: Dimmer as unknown as WidgetEntry,
  SERVO_VIRTUAL_RECEIVER: Dimmer as unknown as WidgetEntry,

  // Read-only numeric long tail.
  ACCESSPOINT_GENERIC_RECEIVER: Sensor as unknown as WidgetEntry,
  ACCELERATION_TRANSCEIVER: Sensor as unknown as WidgetEntry,
  CAPACITIVE_FILLING_LEVEL_SENSOR: Sensor as unknown as WidgetEntry,
  COND_SWITCH_TRANSMITTER_TEMPERATURE: Sensor as unknown as WidgetEntry,
  DIGITAL_ANALOG_OUTPUT: Sensor as unknown as WidgetEntry,
  GENERIC_INPUT_TRANSMITTER: Sensor as unknown as WidgetEntry,

  // Read-only binary long tail. Door-side state transceivers join
  // the WIN_SC binary-sensor pattern; ACCESS_* surfaces "armed",
  // EVENT_INTERFACE is a generic event source, AUTO_RELOCK shows
  // the relock state of a lock.
  ACCESS_RECEIVER: BinarySensor as unknown as WidgetEntry,
  ACCESS_TRANSCEIVER: BinarySensor as unknown as WidgetEntry,
  ARMING: BinarySensor as unknown as WidgetEntry,
  AUTO_RELOCK_TRANSCEIVER: BinarySensor as unknown as WidgetEntry,
  DEVICE: BinarySensor as unknown as WidgetEntry,
  DOOR_LOCK_STATE_TRANSCEIVER: BinarySensor as unknown as WidgetEntry,
  DOOR_LOCK_STATE_TRANSMITTER: BinarySensor as unknown as WidgetEntry,
  EVENT_INTERFACE: BinarySensor as unknown as WidgetEntry,
  MAINTENANCE: BinarySensor as unknown as WidgetEntry,

  // Floor-loop + chamber thermostats share the HEATING_CONTROL
  // slot inventory closely enough to reuse its layout. RF
  // thermostats (HEATING_CONTROL) already map there too.
  CLIMATECONTROL_FLOOR_TRANSCEIVER: Climate as unknown as WidgetEntry,
  CLIMATE_TRANSCEIVER: Climate as unknown as WidgetEntry,
};

/** Look up the widget component for a family, or undefined when no
 *  widget is registered (caller renders fallback). */
export function widgetFor(family: ControlFamily): WidgetEntry | undefined {
  return controlWidgets[family];
}

/**
 * Slot-aware widget selection. Some families overload their slot set —
 * the same DIMMER family covers a plain dimmer, a fixed-colour light
 * (HmIP-BSL: DIMMER.LEVEL + DIMMER.COLOR + DIMMER.COLOR_BEHAVIOUR), and
 * a tunable-white light (DIMMER.LEVEL + DIMMER.COLOR_TEMPERATURE). The
 * routing here looks at the resolved channel's slot inventory and
 * upgrades the widget when the dominant family is enriched.
 */
export function widgetForResolved(resolved: ResolvedChannel): WidgetEntry | undefined {
  // DIMMER / DIMMER_REAL channels surface fixed colour or colour
  // temperature on top of LEVEL — upgrade the widget when the slot
  // inventory grows beyond plain brightness. The CCU lumps these
  // capabilities into the same family so this branch carries the
  // disambiguation.
  if (resolved.family === "DIMMER" || resolved.family === "DIMMER_REAL") {
    if (resolved.slots.COLOR) {
      return FixedColorLight as unknown as WidgetEntry;
    }
    if (resolved.slots.COLOR_TEMPERATURE || resolved.slots.COLORTEMPERATURE) {
      return ColorTempLight as unknown as WidgetEntry;
    }
  }

  // HM-CC-TC simple thermostat (CLIMATECONTROL_REGULATOR channel)
  // carries SWITCH.STATE + TEMP.SETPOINT on the same channel — two
  // families with one slot each. The dominant-family resolver picks
  // SWITCH alphabetically, but the user needs both surfaces.
  if (resolved.family === "SWITCH" && resolved.siblings.TEMP?.SETPOINT) {
    return SimpleRfThermostat as unknown as WidgetEntry;
  }
  if (resolved.family === "TEMP" && resolved.siblings.SWITCH?.STATE) {
    return SimpleRfThermostat as unknown as WidgetEntry;
  }

  return controlWidgets[resolved.family];
}
