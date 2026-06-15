// Family-widget registry. Maps ControlFamily values to the Svelte
// widget that should render a channel resolved to that family.
// Unmapped families fall through to the caller's fallback (typically
// the generic ParameterField list).

import type { Component } from "svelte";
import type { ControlFamily } from "../families";
import type { ResolvedChannel } from "../resolver";

// Every widget accepts the same ControlWidgetProps contract. Svelte's
// Component<P> is generic over its exact prop shape, which means
// Component<ControlWidgetProps> and Component<Props & ControlWidgetProps>
// are not assignable without a cast even when the actual props are a
// superset. `asWidget` concentrates that one intentional variance cast in
// a single documented place; a future props-shape change surfaces here
// rather than silently across every registry entry.
function asWidget<P extends ControlWidgetProps>(c: Component<P>): WidgetEntry {
  return c as unknown as WidgetEntry;
}

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
  SWITCH: asWidget(Switch),
  SWITCH_TRANSMITTER: asWidget(Switch),
  SIMPLE_SWITCH_RECEIVER: asWidget(Switch),
  WATER_SWITCH: asWidget(Switch),
  DIGITAL_STATE: asWidget(Switch),

  // DIMMER-shaped: LEVEL slot. The widget internally wires
  // onSetSlot("LEVEL", v) so the registry's contract holds.
  DIMMER: asWidget(Dimmer),
  DIMMER_REAL: asWidget(Dimmer),
  DUAL_WHITE_BRIGHTNESS: asWidget(Dimmer),
  // UNIVERSAL_LIGHT_RECEIVER (HmIP-RGBW, HmIP-DRG-DALI) carries the
  // richest light surface in the CCU catalogue — brightness, HSV
  // colour, colour temperature and effects on a single channel.
  // UniversalLight reads each capability slot conditionally so a
  // simple HmIP-DRG-DALI channel with only LEVEL still works.
  UNIVERSAL_LIGHT_RECEIVER: asWidget(UniversalLight),

  // BLIND-shaped: LEVEL + STOP slots.
  BLIND: asWidget(Blind),
  BLIND_TRANSMITTER: asWidget(Blind),
  BLIND_VIRTUAL_RECEIVER: asWidget(Blind),
  JALOUSIE: asWidget(Blind),
  SHUTTER_TRANSMITTER: asWidget(Blind),
  SHUTTER_VIRTUAL_RECEIVER: asWidget(Blind),
  WINDOW: asWidget(Blind),
  WINDOW_DRIVE_RECEIVER: asWidget(Blind),

  // Thermostats — Climate is slot-aware and covers both HmIP
  // (HEATING_CONTROL_HMIP, CONTROL_MODE writable integer) and RF
  // (HEATING_CONTROL, CONTROL_MODE read-only with separate AUTO /
  // BOOST / COMFORT / LOWERING action slots).
  HEATING_CONTROL_HMIP: asWidget(Climate),
  HEATING_CONTROL: asWidget(Climate),

  // Lock + door opener.
  LOCK: asWidget(Lock),
  DOOROPENER: asWidget(Lock),
  DOOR_LOCK_TRANSCEIVER: asWidget(Lock),

  // Garage door (HmIP-MOD-HO, HmIP-MOD-TM) — DOOR_COMMAND + DOOR_STATE.
  DOOR_RECEIVER: asWidget(Garage),

  // Power meters — all read-only variants share the same layout.
  POWERMETER: asWidget(Powermeter),
  POWERMETER_IEC: asWidget(Powermeter),
  POWERMETER_IGL: asWidget(Powermeter),
  POWERMETER_PSM: asWidget(Powermeter),

  // Read-only numeric sensors. Routed to the generic Sensor widget,
  // which iterates whatever slots the channel exposes and renders a
  // StatReadout grid. Per-family glyphs / units / labels remain
  // sensible defaults until family-specific overrides land.
  ANALOG_INPUT: asWidget(Sensor),
  BRIGHTNESS_TRANSMITTER: asWidget(Sensor),
  CARBON_DIOXIDE_RECEIVER: asWidget(Sensor),
  DISTANCE_TRANSMITTER: asWidget(Sensor),
  FLOW_METER_TRANSMITTER: asWidget(Sensor),
  GENERIC_MEASURING_TRANSMITTER: asWidget(Sensor),
  SOIL_MOISTURE_TRANSMITTER: asWidget(Sensor),
  TEMP: asWidget(Sensor),
  TEMP_HUM_PARTICLE_MATTER_TRANSMITTER: asWidget(Sensor),
  WEATHER_TRANSMIT: asWidget(Sensor),

  // Read-only binary sensors.
  BUTTON: asWidget(ButtonEvent),
  BTN_SHORT_ONLY: asWidget(ButtonEvent),
  DANGER: asWidget(BinarySensor),
  DOOR_SENSOR: asWidget(BinarySensor),
  DOOR_STATE_TRANSCEIVER: asWidget(BinarySensor),
  MOTIONDETECTOR_TRANSCEIVER: asWidget(BinarySensor),
  RAIN_DETECTION_TRANSMITTER: asWidget(BinarySensor),
  RHS: asWidget(BinarySensor),
  SMOKE_DETECTOR: asWidget(BinarySensor),
  WATER_DETECTION_TRANSMITTER: asWidget(BinarySensor),
  WIN_SC: asWidget(BinarySensor),
  WIN_SC_SECURE: asWidget(BinarySensor),
  WIN_SC_SENSOR: asWidget(BinarySensor),

  // Device-level status (LOWBAT, SABOTAGE) — rendered with the
  // BinarySensor layout; the resolver picks the dominant slot.
  BATTERIE: asWidget(BinarySensor),

  // Colour lighting.
  RGBW_COLOR: asWidget(ColorLight),
  RGB_COLOR: asWidget(ColorLight),
  COLORTEMP: asWidget(ColorTempLight),
  DUAL_WHITE_COLOR: asWidget(ColorTempLight),

  // Sirens + acoustic signal devices.
  ACOUSTIC_SIGNAL_TRANSMITTER: asWidget(Siren),
  ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER: asWidget(Siren),
  ALARM_SWITCH_VIRTUAL_RECEIVER: asWidget(Siren),
  _ALARM_SWITCH_VIRTUAL_RECEIVER: asWidget(Siren),

  // OPTICAL_SIGNAL_RECEIVER (HmIP-WRC6-230 channels 12-18,
  // HmIPW-WRC6 channels 7-13) is the RGB feedback LED on a wall
  // remote — the slot inventory matches HmIP-BSL exactly (LEVEL +
  // COLOR + COLOR_BEHAVIOUR + DURATION + RAMP_TIME), so the
  // fixed-colour light tile is the correct surface, not the
  // Siren widget the older mapping had it on.
  OPTICAL_SIGNAL_RECEIVER: asWidget(FixedColorLight),

  // Continuous-write actuators that share the Dimmer LEVEL slot:
  // BACKLIGHTING wraps a wall-display backlight (0–100 %), SERVO
  // drives a generic positioner (e.g. a fan louver). Both write
  // LEVEL semantically the same way as a dimmer.
  BACKLIGHTING_RECEIVER: asWidget(Dimmer),
  SERVO_TRANSMITTER: asWidget(Dimmer),
  SERVO_VIRTUAL_RECEIVER: asWidget(Dimmer),

  // Read-only numeric long tail.
  ACCESSPOINT_GENERIC_RECEIVER: asWidget(Sensor),
  ACCELERATION_TRANSCEIVER: asWidget(Sensor),
  CAPACITIVE_FILLING_LEVEL_SENSOR: asWidget(Sensor),
  COND_SWITCH_TRANSMITTER_TEMPERATURE: asWidget(Sensor),
  DIGITAL_ANALOG_OUTPUT: asWidget(Sensor),
  GENERIC_INPUT_TRANSMITTER: asWidget(Sensor),

  // Read-only binary long tail. Door-side state transceivers join
  // the WIN_SC binary-sensor pattern; ACCESS_* surfaces "armed",
  // EVENT_INTERFACE is a generic event source, AUTO_RELOCK shows
  // the relock state of a lock.
  ACCESS_RECEIVER: asWidget(BinarySensor),
  ACCESS_TRANSCEIVER: asWidget(BinarySensor),
  ARMING: asWidget(BinarySensor),
  AUTO_RELOCK_TRANSCEIVER: asWidget(BinarySensor),
  DEVICE: asWidget(BinarySensor),
  DOOR_LOCK_STATE_TRANSCEIVER: asWidget(BinarySensor),
  DOOR_LOCK_STATE_TRANSMITTER: asWidget(BinarySensor),
  EVENT_INTERFACE: asWidget(BinarySensor),
  MAINTENANCE: asWidget(BinarySensor),

  // Floor-loop + chamber thermostats share the HEATING_CONTROL
  // slot inventory closely enough to reuse its layout. RF
  // thermostats (HEATING_CONTROL) already map there too.
  CLIMATECONTROL_FLOOR_TRANSCEIVER: asWidget(Climate),
  CLIMATE_TRANSCEIVER: asWidget(Climate),
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
      return asWidget(FixedColorLight);
    }
    if (resolved.slots.COLOR_TEMPERATURE || resolved.slots.COLORTEMPERATURE) {
      return asWidget(ColorTempLight);
    }
  }

  // HM-CC-TC simple thermostat (CLIMATECONTROL_REGULATOR channel)
  // carries SWITCH.STATE + TEMP.SETPOINT on the same channel — two
  // families with one slot each. The dominant-family resolver picks
  // SWITCH alphabetically, but the user needs both surfaces.
  if (resolved.family === "SWITCH" && resolved.siblings.TEMP?.SETPOINT) {
    return asWidget(SimpleRfThermostat);
  }
  if (resolved.family === "TEMP" && resolved.siblings.SWITCH?.STATE) {
    return asWidget(SimpleRfThermostat);
  }

  return controlWidgets[resolved.family];
}
