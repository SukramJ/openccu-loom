// CONTROL-aware data-point resolver. Given a channel's data-points
// (each carrying `control: "FAMILY.SLOT"` from the REST DTO), group
// by FAMILY and key the slot-suffix → data-point map. Returns null
// when the channel has no CONTROL-tagged data-points (caller falls
// back to the generic ParameterField renderer).

import type { DataPointSummary } from "$lib/api/types";
import type { ControlFamily } from "./families";
import { parseControl } from "./families";

export type ResolvedChannel = {
  family: ControlFamily;
  /** Slot suffix → data-point. Same suffix may appear at most once
   *  on a channel (CCU paramset constraint). */
  slots: Record<string, DataPointSummary>;
  /** Slot maps for every non-dominant CONTROL family observed on the
   *  same channel. Lets slot-aware routing recognise multi-family
   *  channels (HM-CC-TC simple thermostat: SWITCH.STATE + TEMP.SETPOINT)
   *  without losing the secondary slots. */
  siblings: Partial<Record<ControlFamily, Record<string, DataPointSummary>>>;
};

/**
 * Resolve the dominant CONTROL family on a channel's data-points.
 *
 * Strategy: the family that owns the most CONTROL-tagged slots wins.
 * Ties are broken by descending slot count, then alphabetically. In
 * practice every CCU channel either has zero CONTROL-tagged DPs (no
 * widget) or one family covering most/all of its writable DPs.
 *
 * Returns null when no data-point carries CONTROL.
 */
export function resolveChannel(
  dataPoints: DataPointSummary[],
): ResolvedChannel | null {
  const byFamily = new Map<ControlFamily, Record<string, DataPointSummary>>();

  for (const dp of dataPoints) {
    const parsed = parseControl(dp.control);
    if (!parsed) continue;
    let bucket = byFamily.get(parsed.family);
    if (!bucket) {
      bucket = {};
      byFamily.set(parsed.family, bucket);
    }
    // First-write-wins on duplicate slot suffix: a CCU channel may
    // surface multiple parameters whose CONTROL slot collides (e.g.
    // legacy + new). The first match is what the widget renders.
    if (!(parsed.slot in bucket)) {
      bucket[parsed.slot] = dp;
    }
  }

  if (byFamily.size === 0) return null;

  // Pick the family with the most slots (ties → alphabetical).
  let bestFamily: ControlFamily | null = null;
  let bestSlots: Record<string, DataPointSummary> = {};
  for (const [family, slots] of byFamily) {
    const count = Object.keys(slots).length;
    if (
      bestFamily === null ||
      count > Object.keys(bestSlots).length ||
      (count === Object.keys(bestSlots).length && family < bestFamily)
    ) {
      bestFamily = family;
      bestSlots = slots;
    }
  }
  if (bestFamily === null) return null;

  // Record every other observed family as a sibling so slot-aware
  // routing can recognise multi-family channels (e.g. HM-CC-TC simple
  // thermostat surfaces SWITCH.STATE + TEMP.SETPOINT side by side).
  const siblings: Partial<Record<ControlFamily, Record<string, DataPointSummary>>> = {};
  for (const [family, slots] of byFamily) {
    if (family !== bestFamily) {
      siblings[family] = slots;
    }
  }
  return { family: bestFamily, slots: bestSlots, siblings };
}

/** Convenience: look up a specific slot on the resolved channel. */
export function slot(
  resolved: ResolvedChannel,
  suffix: string,
): DataPointSummary | undefined {
  return resolved.slots[suffix];
}

// ─── Widget selection from CONTROL ──────────────────────────────────

/**
 * Which widget the parameter editor should render for one parameter.
 * `"auto"` means "no CONTROL-derived answer" and hands the decision back
 * to ParameterField's own type/range heuristics.
 */
export type WidgetKind = "switch" | "level" | "trigger" | "auto";

/**
 * Decide a parameter's widget from its CCU `CONTROL` hint.
 *
 * The rules below are the slot-semantics table of
 * `notes/reference/control-inventory.md`, narrowed to the three widget
 * families the editor renders differently from its own heuristics. The
 * suffix is what carries the render hint — the family disambiguates
 * meaning, not shape — so this switches on the slot and consults the
 * family only where the inventory says the same suffix means two things.
 *
 * Two of those disambiguations are load-bearing here:
 *
 *   - `LEVEL` is a position for `DIMMER` / `BLIND` / `JALOUSIE` but an
 *     enumerated handle position for `WIN_SC`. Rather than naming the
 *     families, this reads the parameter's own `value_list`: a parameter
 *     the CCU describes with choices is a selector whatever its slot is
 *     called, and that is a property the DTO carries rather than one this
 *     table would have to keep in sync with the firmware.
 *   - `STATE` is a toggle only where it is writable and BOOL. On
 *     `DOOR_SENSOR`, `DANGER` or `SMOKE_DETECTOR` the same slot is a
 *     read-only status, and rendering a switch for it would offer a write
 *     the CCU refuses.
 */
export function widgetFor(param: {
  type: string;
  control?: string;
  value_list?: unknown[];
  operations?: { write?: boolean };
}): WidgetKind {
  // An ACTION parameter has no value to display — it is a pulse the
  // operator sends. That holds regardless of what CONTROL says, and for
  // parameters carrying no CONTROL at all.
  if (param.type === "ACTION") return "trigger";

  const parsed = parseControl(param.control);
  if (!parsed) return "auto";

  // A parameter the CCU describes with choices is a selector, whatever
  // its slot suffix suggests.
  if (param.value_list && param.value_list.length > 0) return "auto";

  const writable = param.operations?.write !== false;
  const { slot: suffix } = parsed;

  if (suffix === "STATE") {
    return param.type === "BOOL" && writable ? "switch" : "auto";
  }
  if (suffix === "LEVEL" || suffix.startsWith("LEVEL_")) {
    return writable ? "level" : "auto";
  }
  // BUTTON.SHORT / BUTTON.LONG are event pulses the device sends, not
  // commands the operator issues, so they are not triggers here — an
  // ACTION-typed parameter already took that branch above.
  return "auto";
}
