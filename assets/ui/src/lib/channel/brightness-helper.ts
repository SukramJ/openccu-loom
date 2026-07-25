import type { DataPointSummary } from "$lib/api/types";

/**
 * Motion-detector direct links gate their transitions on the
 * brightness the sender transmits: the receiver's LINK paramset carries
 * SHORT_COND_VALUE_LO / _HI (and the LONG_ variants) as the threshold
 * the transmitted value is compared against. Reading the sender
 * channel's current brightness and dropping it into that field spares
 * the operator from looking the reading up by hand.
 *
 * Mirrors the CCU WebUI's config/ic_md.cgi "use current brightness"
 * helper, which takes the sender's current BRIGHTNESS as
 * SHORT_COND_VALUE_LO/_HI and hides the button until a reading exists.
 */

// Sender data-point names that carry a brightness / illuminance
// reading, in preference order. BRIGHTNESS (0..255 on classic BidCos
// motion detectors) maps directly onto the COND_VALUE range; the
// illuminance variants (lux) cover the HmIP sensors.
export const BRIGHTNESS_DATA_POINTS = [
  "BRIGHTNESS",
  "ILLUMINATION",
  "CURRENT_ILLUMINATION",
  "AVERAGE_ILLUMINATION",
  "LUX",
] as const;

const BRIGHTNESS_SET: ReadonlySet<string> = new Set(BRIGHTNESS_DATA_POINTS);

/** True for a data-point name that reports a brightness reading. */
export function isBrightnessDataPoint(name: string): boolean {
  return BRIGHTNESS_SET.has(name);
}

// Receiver-side LINK condition-threshold parameters the helper can
// fill: SHORT_COND_VALUE_LO/_HI and their LONG_ counterparts.
const CONDITION_VALUE_RE = /^(?:SHORT|LONG)_COND_VALUE_(?:LO|HI)$/;

/** True for a LINK condition-value threshold parameter the helper fills. */
export function isConditionValueParam(name: string): boolean {
  return CONDITION_VALUE_RE.test(name);
}

export type BrightnessReading = {
  parameter: string;
  value: number;
  unit: string | null;
};

/**
 * Pick the brightness reading off a sender channel's data points, or
 * null when the channel exposes none with a usable numeric value.
 * A data point qualifies only when it is readable and currently holds
 * a finite value — mirroring the CCU, which hides the helper until the
 * sender has reported a reading at least once.
 */
export function pickBrightnessReading(
  dataPoints: readonly DataPointSummary[],
): BrightnessReading | null {
  for (const name of BRIGHTNESS_DATA_POINTS) {
    const dp = dataPoints.find((d) => d.parameter === name);
    if (!dp) continue;
    if (dp.operations && !dp.operations.read) continue;
    const value = coerceNumber(dp.value);
    if (value === null) continue;
    return { parameter: name, value, unit: dp.unit ? dp.unit : null };
  }
  return null;
}

/** Coerce a wire value (number or numeric string) to a finite number. */
export function coerceNumber(raw: unknown): number | null {
  if (typeof raw === "number") return Number.isFinite(raw) ? raw : null;
  if (typeof raw === "string" && raw.trim() !== "") {
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

/** Human-readable rendering of a reading for a button label / tooltip. */
export function formatReading(value: number, unit: string | null): string {
  const num = Number.isInteger(value) ? String(value) : value.toFixed(1);
  return unit ? `${num} ${unit}` : num;
}
