// Shared domain heuristics for the Übersicht tab. Both
// QuickControlTab (actors) and SensorChannelList (read-only telemetry)
// route their per-channel rendering decisions through these helpers
// so a single source of truth exists. Mirrors the channel-type
// classification on the backend's `adapter/schedules.go`.

export type Domain =
  | "switch"
  | "light"
  | "cover"
  | "climate"
  | "lock"
  | "valve"
  | "siren"
  | null;

/**
 * Classify a channel's type string into a homematic actor domain.
 * Sensor / transmitter channels share prefixes with their actor
 * counterparts (SWITCH_TRANSMITTER vs SWITCH_VIRTUAL_RECEIVER, etc.);
 * the writability of the operative parameter must be checked
 * separately by the caller.
 */
export function detectDomain(type: string | undefined): Domain {
  const s = (type ?? "").toUpperCase();
  if (!s) return null;
  if (s.startsWith("DIMMER") || s.includes("DIMMER") || s.startsWith("LIGHT"))
    return "light";
  if (s.startsWith("BLIND") || s.startsWith("SHUTTER") || s.startsWith("COVER"))
    return "cover";
  if (s.startsWith("DOOR_LOCK") || s.startsWith("KEYMATIC")) return "lock";
  if (
    s.startsWith("CLIMATECONTROL") ||
    s.includes("HEATING_CLIMATECONTROL") ||
    s.includes("THERMOSTAT")
  )
    return "climate";
  if (s.startsWith("VALVE") || s === "WATER_VALVE" || s.startsWith("WATER_VALVE"))
    return "valve";
  if (
    s.startsWith("SIREN") ||
    s.startsWith("ACOUSTIC_SIGNAL") ||
    s.startsWith("ALARM_ACTUATOR")
  )
    return "siren";
  if (s.startsWith("SWITCH") || s.startsWith("ENERGIE_METER")) return "switch";
  return null;
}

/**
 * The CCU parameter the QuickControl widget would write when the
 * user interacts. Used to check `operations.write` on the relevant
 * data point — channels whose operative param is read-only or absent
 * are routed to the read-only sensor pane instead.
 */
export function operativeParameter(d: Exclude<Domain, null>): string {
  switch (d) {
    case "switch":
    case "lock":
    case "siren":
      return "STATE";
    case "light":
    case "cover":
    case "valve":
      return "LEVEL";
    case "climate":
      return "SET_POINT_TEMPERATURE";
  }
}

/**
 * Channels that the Übersicht tab skips entirely — they have their
 * own dedicated UI block (maintenance grid, schedule sub-tab, device-
 * pseudo channel goes to the Konfigurieren tab).
 */
export function isOverviewExcluded(args: {
  address: string;
  number: number;
  type?: string;
}): boolean {
  if (args.address.endsWith(":0")) return true;
  if (args.number < 0) return true; // device-level pseudo-channel
  const t = (args.type ?? "").toUpperCase();
  if (t.endsWith("WEEK_PROFILE")) return true;
  return false;
}

/**
 * True for CCU channel types that only emit events / read-only state and
 * never accept a setValue write. The Übersicht panel renders these as a
 * dense status-badge stripe instead of a full control tile — they should
 * not occupy the same vertical space as a switch / dimmer / blind.
 *
 * The catch covers every observable sensor channel: the obvious
 * `*_TRANSMITTER` / `*_TRANSCEIVER` suffixes, plus pure read-only sensor
 * types that don't carry the suffix (SHUTTER_CONTACT, SMOKE_DETECTOR,
 * various WEATHER / CLIMATE inputs). Anything that doesn't classify as an
 * actor domain (`detectDomain`) falls into the same bucket — the rule of
 * thumb is "if the user can't write to it, it doesn't deserve a full
 * tile".
 */
export function isStatusOnlyChannelType(type: string | undefined): boolean {
  const t = (type ?? "").toUpperCase();
  if (!t) return false;
  if (t.endsWith("_TRANSMITTER") || t.endsWith("_TRANSCEIVER")) return true;
  if (t === "MAINTENANCE") return true;
  if (STATUS_CHANNEL_TYPES.has(t)) return true;
  // Channels that don't map onto any known actor domain are read-only by
  // definition for our purposes — they live in the status stripe.
  if (detectDomain(t) === null) return true;
  return false;
}

/**
 * Explicit allow-list of sensor channel types whose name does not follow
 * the `_TRANSMITTER` / `_TRANSCEIVER` convention. Most of these come from
 * the older RF / BidCos firmware vocabulary.
 */
const STATUS_CHANNEL_TYPES = new Set([
  "SHUTTER_CONTACT",
  "TILT_SENSOR",
  "SMOKE_DETECTOR",
  "WATER_DETECTOR",
  "RAIN_DETECTOR",
  "MOTION_DETECTOR",
  "MOTIONDETECTOR",
  "PRESENCEDETECTOR",
  "WEATHER",
  "WEATHER_RECEIVER",
  "CLIMATECONTROL_FLOOR_TRANSCEIVER",
  "POWERMETER",
  "POWERMETER_IGL",
  "ROTARY_HANDLE_SENSOR",
  "DOOR_SENSOR",
  "WINDOW_SENSOR",
  "ACCESS_TRANSCEIVER",
]);

/**
 * Returns the value-keys to show inline on a collapsed status badge for
 * the given channel. Priority order: TEMPERATURE + HUMIDITY (climate
 * sensors), STATE (binary contacts / smoke), LEVEL, ILLUMINATION, then
 * the first observed value as a fallback. Caps at two values so the
 * badge row stays scannable.
 */
export const STATUS_HEADLINE_KEYS = [
  "ACTUAL_TEMPERATURE",
  "TEMPERATURE",
  "HUMIDITY",
  "STATE",
  "LEVEL",
  "ILLUMINATION",
  "BRIGHTNESS",
  "MOTION",
  "RAINING",
  "POWER",
] as const;
