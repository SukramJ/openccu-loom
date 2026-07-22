// SPDX-License-Identifier: MIT
//
// Primary-DP lookup table for the sensor + actor tile. For every CCU
// channel type that has a well-known "headline" parameter, the table
// names that parameter so the tile can render it as the big top-row
// value. Tile composition rules live in docs/ui/sensor-actor-tile-concept.md.
//
// The table is small on purpose. Adding a row when the upstream
// device-profile registry adds a new sensor channel type is a one-line
// edit; the fallback chain below covers everything else.

import type { DataPointSummary } from "$lib/api/types";

/** Channel-type → primary parameter name (uppercase). */
const PRIMARY_DP_BY_CHANNEL_TYPE: Record<string, string> = {
  // motion / presence
  MOTIONDETECTOR: "MOTION",
  MOTIONDETECTOR_TRANSCEIVER: "MOTION",
  PRESENCEDETECTOR: "PRESENCE_DETECTION_STATE",
  PRESENCEDETECTOR_TRANSCEIVER: "PRESENCE_DETECTION_STATE",

  // contacts / tilt
  SHUTTER_CONTACT: "STATE",
  TILT_SENSOR: "STATE",
  ROTARY_HANDLE_SENSOR: "STATE",

  // smoke / water / rain
  SMOKE_DETECTOR: "SMOKE_DETECTOR_ALARM_STATUS",
  WATER_DETECTOR: "ALARMSTATE",
  RAIN_DETECTOR: "RAINING",

  // brightness / climate / weather
  BRIGHTNESS_TRANSMITTER: "CURRENT_ILLUMINATION",
  CLIMATE_TRANSCEIVER: "ACTUAL_TEMPERATURE",
  CLIMATECONTROL_FLOOR_TRANSCEIVER: "ACTUAL_TEMPERATURE",
  WEATHER: "ACTUAL_TEMPERATURE",
  WEATHER_RECEIVER: "ACTUAL_TEMPERATURE",
  WEATHER_TRANSMITTER: "ACTUAL_TEMPERATURE",

  // particulate matter (HmIP-SFD)
  TEMP_HUMIDITY_PARTICULATE_MATTER_TRANSMITTER: "MASS_CONCENTRATION_PM_2_5",
  PARTICULATE_MATTER_TRANSMITTER: "MASS_CONCENTRATION_PM_2_5",

  // energy
  ENERGIE_METER_TRANSMITTER: "POWER",
  POWERMETER: "POWER",
};

/**
 * Pick the primary DP for a channel.
 *
 * Order:
 *  1. PRIMARY_DP_BY_CHANNEL_TYPE entry, when present and the parameter
 *     exists in `dps`.
 *  2. First DP that is readable + emits events (a "live state" DP).
 *  3. First DP overall.
 *
 * Returns `undefined` only for the empty-list edge case.
 */
export function findPrimaryDP(
  channelType: string | undefined,
  dps: readonly DataPointSummary[],
): DataPointSummary | undefined {
  if (dps.length === 0) return undefined;
  const target = PRIMARY_DP_BY_CHANNEL_TYPE[(channelType ?? "").toUpperCase()];
  if (target) {
    const hit = dps.find((d) => d.parameter === target);
    if (hit) return hit;
  }
  const readable = dps.find((d) => d.operations.read && d.operations.event);
  if (readable) return readable;
  // Never promote a write-only DP (e.g. an action press) to the
  // headline — it has no value to display. Prefer anything readable;
  // only a pure-action channel falls back to its first DP.
  const anyReadable = dps.find((d) => d.operations.read);
  if (anyReadable) return anyReadable;
  return dps[0];
}

/**
 * Is this channel a candidate for the sensor + actor tile?
 * Yes when it has at least two DPs the tile can usefully surface
 * (i.e. would render more than a one-line status badge).
 */
export function channelIsTileCandidate(dps: readonly DataPointSummary[]): boolean {
  if (dps.length < 2) return false;
  let useful = 0;
  for (const d of dps) {
    if (d.operations.read || d.operations.write) {
      useful++;
      if (useful >= 2) return true;
    }
  }
  return false;
}
