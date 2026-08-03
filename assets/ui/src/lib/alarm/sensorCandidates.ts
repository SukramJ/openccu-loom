import { makeTextMatcher } from "$lib/utils";
import type { AlarmOutputCandidate, AlarmSensorType, DeviceSummary } from "$lib/api/types";

// Add-sensor assist for AlarmSensors.svelte's device picker (docs/alarm-
// concept.md §12.2 / §6.1): which devices are surfaced by default, and
// which channel/parameter/type a picked device pre-fills. Extracted so
// the picker's guessing logic is unit-testable independent of the view.
// The search/filter/sort helpers below back both the wizard's sensor
// picker (step 2) and its output-candidate picker (step 3, docs/alarm-
// concept.md §12.3) — kept here, shape-agnostic where possible, so they
// stay testable without a component harness.

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

/**
 * Default STATE-family parameter for a sensor type.
 *
 * Hazard resolves to the derived boolean SMOKE_ALARM rather than the raw
 * SMOKE_DETECTOR_ALARM_STATUS enumeration. The raw status carries
 * INTRUSION_ALARM in its value list, which means the installation drove
 * that detector as a siren for a burglary — the default "anything but
 * index 0 is active" rule would read the alarm system's own output back
 * as a smoke detection. Pre-selecting the boolean keeps an operator out
 * of that trap without having to understand it.
 *
 * The server-side candidate list (`GET /alarm/sensor-candidates`) makes
 * the same recommendation and additionally supplies active_values for
 * anyone who does pick the raw enumeration.
 */
export function guessSensorParameter(type: AlarmSensorType): string {
  switch (type) {
    case "motion":
      return "MOTION";
    case "tamper":
      return "SABOTAGE";
    case "hazard":
      return "SMOKE_ALARM";
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

/** Resolves which Area id owns a (central, room) pair — the shape
 *  `areasStore.areaIdOf` implements. Passed in rather than imported so
 *  this module stays store-free and unit-testable with a plain stub. */
export type AreaIdOf = (central: string, room: string) => string | undefined;

export type CandidateOptions = {
  query?: string;
  showAll?: boolean;
  limit?: number;
  /** Narrows to devices assigned to this CCU room (picker room filter). */
  room?: string;
  /** Narrows to devices assigned to this CCU function/"Gewerk" (picker
   *  function filter). */
  func?: string;
  /** Narrows to devices whose central+room resolves to this Area id
   *  (picker area filter) — an operator-defined grouping ABOVE CCU
   *  rooms, distinct from alarm zones. Requires `areaIdOf`. */
  area?: string;
  areaIdOf?: AreaIdOf;
};

/**
 * Build the add-sensor device candidate list: security filter (unless
 * showAll), optional room/function/area narrowing, then free-text search
 * over name/address/model/model_label/rooms/functions, capped at `limit`.
 * Mirrors AlarmSensors.svelte's picker pipeline exactly.
 */
export function buildCandidates(
  devices: DeviceSummary[],
  {
    query = "",
    showAll = false,
    limit = 60,
    room = "",
    func = "",
    area = "",
    areaIdOf,
  }: CandidateOptions = {},
): DeviceSummary[] {
  const match = makeTextMatcher(query);
  return devices
    .filter((d) => {
      if (!showAll && !isSecurityDevice(d)) return false;
      if (room && !(d.rooms ?? []).includes(room)) return false;
      if (func && !(d.functions ?? []).includes(func)) return false;
      if (area) {
        const central = d.central ?? "";
        if (!(d.rooms ?? []).some((r) => areaIdOf?.(central, r) === area)) return false;
      }
      if (query) {
        return (
          match(d.name ?? "") ||
          match(d.address) ||
          match(d.model) ||
          match(d.model_label ?? "") ||
          (d.rooms ?? []).some(match) ||
          (d.functions ?? []).some(match)
        );
      }
      return true;
    })
    .slice(0, limit);
}

export type OutputCandidateFilterOptions = {
  query?: string;
  /** Narrows to channels assigned to this CCU room (picker room filter). */
  room?: string;
  /** Narrows to channels assigned to this CCU function/"Gewerk" (picker
   *  function filter). */
  func?: string;
  /** Narrows to channels whose central+room resolves to this Area id
   *  (picker area filter). Requires `areaIdOf`. */
  area?: string;
  areaIdOf?: AreaIdOf;
};

/**
 * Free-text search + room/function/area narrowing over the alarm output-
 * candidate list (docs/alarm-concept.md §12.3 step 3). Search matches the
 * channel/device name, both addresses, the raw model, and any room or
 * function assignment — the output-candidate mirror of buildCandidates'
 * device search.
 */
export function filterOutputCandidates(
  candidates: AlarmOutputCandidate[],
  { query = "", room = "", func = "", area = "", areaIdOf }: OutputCandidateFilterOptions = {},
): AlarmOutputCandidate[] {
  const match = makeTextMatcher(query);
  return candidates.filter((c) => {
    if (room && !(c.rooms ?? []).includes(room)) return false;
    if (func && !(c.functions ?? []).includes(func)) return false;
    if (area) {
      if (!(c.rooms ?? []).some((r) => areaIdOf?.(c.central, r) === area)) return false;
    }
    if (query) {
      return (
        match(c.channel_name ?? "") ||
        match(c.device_name ?? "") ||
        match(c.device_address) ||
        match(c.channel_address) ||
        match(c.model) ||
        (c.rooms ?? []).some(match) ||
        (c.functions ?? []).some(match)
      );
    }
    return true;
  });
}

/**
 * Distinct, sorted values across every item's array-valued facet (rooms or
 * functions) — feeds the wizard's room/function filter selects for either
 * a DeviceSummary or an AlarmOutputCandidate pool.
 */
export function distinctValues<T>(
  items: T[],
  accessor: (item: T) => string[] | undefined,
): string[] {
  const set = new Set<string>();
  for (const item of items) {
    for (const v of accessor(item) ?? []) set.add(v);
  }
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
}

export type PickerSortField = "name" | "room" | "model";

/** The three fields a picker row can be sorted by, projected from either a
 *  DeviceSummary or an AlarmOutputCandidate via the caller's `keyOf`. */
export type PickerSortKey = {
  name: string;
  room: string;
  model: string;
};

/**
 * Sorts picker rows by name/room/model, locale-aware (case-insensitive,
 * numeric-aware) and stable. Pure and shape-agnostic: callers project
 * their row type to a PickerSortKey rather than the helper knowing about
 * DeviceSummary or AlarmOutputCandidate directly.
 */
export function sortPickerRows<T>(
  rows: T[],
  field: PickerSortField,
  keyOf: (row: T) => PickerSortKey,
): T[] {
  const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });
  return [...rows].sort((a, b) => collator.compare(keyOf(a)[field], keyOf(b)[field]));
}
