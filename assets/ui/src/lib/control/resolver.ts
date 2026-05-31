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
