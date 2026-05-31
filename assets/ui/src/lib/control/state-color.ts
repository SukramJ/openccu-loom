// Mirrors HA frontend's state-color resolution
// (frontend/src/common/entity/state_color.ts +
// frontend/src/resources/theme/color/color.globals.ts, Apache-2.0).
// We adopt the --state-*-color CSS variable namespace verbatim so the
// SPA looks at home alongside HA. The mapping below picks which HA
// state token represents an "active" tile for each CCU CONTROL family.

import type { ControlFamily } from "./families";

/**
 * Resolve the CSS-variable expression that should drive the tile's
 * `--tile-color` for a given CONTROL family + observed value pair.
 * Returns the HA state-coloured token when the family is "active",
 * otherwise the neutral disabled token.
 */
export function resolveTileColor(
  family: ControlFamily,
  value: unknown,
  observed: boolean,
): string {
  if (!observed) return "var(--ha-disabled-text-color, var(--ha-secondary-text-color))";

  switch (family) {
    case "SWITCH":
    case "DOOROPENER":
      return truthy(value)
        ? "var(--state-switch-active-color, var(--ha-primary-color))"
        : "var(--ha-secondary-text-color)";

    case "DIMMER":
    case "DIMMER_REAL":
    case "DUAL_WHITE_BRIGHTNESS":
    case "RGBW_AUTOMATIC":
    case "RGBW_COLOR":
    case "RGB_COLOR":
    case "UNIVERSAL_LIGHT_RECEIVER":
      return numeric(value) > 0
        ? "var(--state-light-active-color, var(--ha-primary-color))"
        : "var(--ha-secondary-text-color)";

    case "BLIND":
    case "BLIND_TRANSMITTER":
    case "BLIND_VIRTUAL_RECEIVER":
    case "JALOUSIE":
    case "SHUTTER_TRANSMITTER":
    case "SHUTTER_VIRTUAL_RECEIVER":
    case "WINDOW":
    case "WINDOW_DRIVE_RECEIVER":
      return numeric(value) > 0 && numeric(value) < 1
        ? "var(--state-cover-active-color, var(--ha-primary-color))"
        : "var(--ha-secondary-text-color)";

    case "DOOR_RECEIVER":
      // value is truthy while the door is open or in ventilation.
      return truthy(value)
        ? "var(--state-cover-active-color, var(--ha-primary-color))"
        : "var(--ha-secondary-text-color)";

    case "HEATING_CONTROL":
    case "HEATING_CONTROL_HMIP":
      // Without slot context this resolver only flags the tile as
      // "active". Callers that need heat-vs-cool resolution should
      // read CONTROL_MODE / HEATING_COOLING slots and pass a more
      // specific token.
      return "var(--state-climate-auto-color, var(--ha-primary-color))";

    case "LOCK":
      return truthy(value)
        ? "var(--state-lock-active-color, var(--ha-error-color))"
        : "var(--ha-secondary-text-color)";

    case "DANGER":
    case "SMOKE_DETECTOR":
    case "WATER_DETECTION_TRANSMITTER":
      return truthy(value)
        ? "var(--state-siren-active-color, var(--ha-error-color))"
        : "var(--ha-secondary-text-color)";

    case "WIN_SC":
    case "WIN_SC_SENSOR":
    case "WIN_SC_SECURE":
    case "DOOR_SENSOR":
    case "DOOR_STATE_TRANSCEIVER":
    case "RHS":
      return truthy(value)
        ? "var(--state-binary_sensor-door-on-color, var(--ha-info-color))"
        : "var(--ha-secondary-text-color)";

    case "POWERMETER":
    case "POWERMETER_IEC":
    case "POWERMETER_IGL":
    case "POWERMETER_PSM":
      return numeric(value) > 0
        ? "var(--state-fan-active-color, var(--ha-info-color))"
        : "var(--ha-secondary-text-color)";

    default:
      return truthy(value)
        ? "var(--ha-primary-color)"
        : "var(--ha-secondary-text-color)";
  }
}

function truthy(v: unknown): boolean {
  if (typeof v === "boolean") return v;
  if (typeof v === "number") return v !== 0;
  if (typeof v === "string") return v !== "" && v !== "0" && v.toLowerCase() !== "false";
  return v != null;
}

function numeric(v: unknown): number {
  if (typeof v === "number") return v;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "string") {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }
  return 0;
}
