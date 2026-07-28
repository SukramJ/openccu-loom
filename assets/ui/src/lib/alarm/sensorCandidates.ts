import { makeTextMatcher } from "$lib/utils";
import type { AlarmSensorType, DeviceSummary } from "$lib/api/types";

// Add-sensor assist for AlarmSensors.svelte's device picker (docs/alarm-
// concept.md §12.2 / §6.1): which devices are surfaced by default, and
// which channel/parameter/type a picked device pre-fills. Extracted so
// the picker's guessing logic is unit-testable independent of the view.

/** Minimal device shape the guessing/filtering helpers below read from. */
type DeviceLike = Pick<DeviceSummary, "model" | "model_label" | "name">;

// Which devices look security-relevant by model or name. The "show all"
// toggle in the picker widens the candidate pool past this filter.
export const SECURITY_DEVICE_RE =
  /swdo|sci|smo|smi|spi|sec|rc[ -]?\d|krca|wrc|wgc|motion|pir|presence|prescence|bewegung|sabot|tamper|contact|kontakt|fenster|window|door|t[üu]r|smoke|rauch|swsd|water|wasser|leak|co2|gas/i;

/** True when a device's model/label/name looks security-relevant. */
export function isSecurityDevice(device: DeviceLike): boolean {
  const hay = `${device.model} ${device.model_label ?? ""} ${device.name ?? ""}`;
  return SECURITY_DEVICE_RE.test(hay);
}

// Best-guess AlarmSensorType from a device's model/label/name (§6.1
// presets). Falls back to "door" when nothing more specific matches.
export function guessSensorType(device: DeviceLike): AlarmSensorType {
  const s = `${device.model} ${device.model_label ?? ""} ${device.name ?? ""}`.toLowerCase();
  if (/sabot|tamper/.test(s)) return "tamper";
  if (/smoke|rauch|swsd|water|wasser|leak|co2|gas/.test(s)) return "hazard";
  if (/motion|pir|presence|prescence|bewegung|smi|spi/.test(s)) return "motion";
  if (/window|rotary|handle|swdo|fenster/.test(s)) return "window";
  if (/rc[ -]?\d|krca|wrc|remote|panic|taster/.test(s)) return "panic";
  return "door";
}

/** Default STATE-family parameter for a sensor type. */
export function guessSensorParameter(type: AlarmSensorType): string {
  switch (type) {
    case "motion":
      return "MOTION";
    case "tamper":
      return "SABOTAGE";
    case "hazard":
      return "SMOKE_DETECTOR_ALARM_STATUS";
    case "panic":
      return "PRESS_SHORT";
    default:
      return "STATE";
  }
}

export type SensorBinding = {
  channel: string;
  parameter: string;
  type: AlarmSensorType;
};

/**
 * Guess the channel/parameter/type a picked device pre-fills the add-sensor
 * form with. Channel 1 is the picker's fixed default — the operator can
 * still edit it before confirming. Returns null only for a device with no
 * usable address (defensive; real DeviceSummary rows always carry one).
 */
export function guessSensorBinding(device: DeviceSummary): SensorBinding | null {
  if (!device.address) return null;
  const type = guessSensorType(device);
  return {
    channel: `${device.address}:1`,
    parameter: guessSensorParameter(type),
    type,
  };
}

export type CandidateOptions = {
  query?: string;
  showAll?: boolean;
  limit?: number;
};

/**
 * Build the add-sensor device candidate list: security filter (unless
 * showAll), then free-text search over name/address/model/model_label,
 * capped at `limit`. Mirrors AlarmSensors.svelte's picker pipeline exactly.
 */
export function buildCandidates(
  devices: DeviceSummary[],
  { query = "", showAll = false, limit = 60 }: CandidateOptions = {},
): DeviceSummary[] {
  const match = makeTextMatcher(query);
  return devices
    .filter((d) => {
      if (!showAll && !isSecurityDevice(d)) return false;
      if (query) {
        return (
          match(d.name ?? "") ||
          match(d.address) ||
          match(d.model) ||
          match(d.model_label ?? "")
        );
      }
      return true;
    })
    .slice(0, limit);
}
