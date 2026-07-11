import type { UISchemaParameter } from "$lib/api/types";

/**
 * Some CCU paramsets carry two parameters that share an identical
 * human label and differ only by their machine name — the canonical
 * case being an upper / lower threshold pair such as
 * `COND_TX_CYCLIC_ABOVE` / `COND_TX_CYCLIC_BELOW`, both labelled
 * "Entscheidungswert zyklisch senden". The server owns the label
 * (`UISchemaParameter.label`), so the SPA cannot rename it; instead it
 * disambiguates on the client by either appending a directional
 * qualifier derived from the parameter name, or — when the name
 * carries no recognizable direction — asking the field to surface the
 * machine name prominently.
 */

/** Direction inferred from a threshold-style parameter-name suffix. */
export type ThresholdDirection = "upper" | "lower";

export type LabelDisambiguation = {
  /**
   * Directional qualifier to append to the (otherwise identical)
   * label. Null when the colliding parameter carries no recognizable
   * direction — `emphasizeName` is set in that case instead.
   */
  direction: ThresholdDirection | null;
  /**
   * When true the machine name is the only differentiator, so the
   * field should render it prominently (a badge) rather than as the
   * usual muted inline suffix.
   */
  emphasizeName: boolean;
};

// Name-suffix families that map onto an upper / lower threshold. The
// two sets are disjoint, so evaluation order is irrelevant.
const UPPER_SUFFIXES = ["_ABOVE", "_HI", "_TOP"];
const LOWER_SUFFIXES = ["_BELOW", "_LO", "_BOTTOM"];

function directionOf(name: string): ThresholdDirection | null {
  const upper = name.toUpperCase();
  if (UPPER_SUFFIXES.some((s) => upper.endsWith(s))) return "upper";
  if (LOWER_SUFFIXES.some((s) => upper.endsWith(s))) return "lower";
  return null;
}

function displayLabel(p: Pick<UISchemaParameter, "name" | "label">): string {
  return (p.label ?? p.name).trim();
}

/**
 * Compute per-parameter disambiguation for one rendered group. Only
 * parameters whose display label collides with another parameter's in
 * the same group receive an entry; unique labels are left untouched
 * (absent from the map → render verbatim). Pure and side-effect free
 * so it can be unit-tested in isolation.
 */
export function disambiguateLabels(
  params: Pick<UISchemaParameter, "name" | "label">[],
): Map<string, LabelDisambiguation> {
  const labelCounts = new Map<string, number>();
  for (const p of params) {
    const key = displayLabel(p);
    labelCounts.set(key, (labelCounts.get(key) ?? 0) + 1);
  }
  const out = new Map<string, LabelDisambiguation>();
  for (const p of params) {
    if ((labelCounts.get(displayLabel(p)) ?? 0) <= 1) continue;
    const direction = directionOf(p.name);
    out.set(p.name, { direction, emphasizeName: direction === null });
  }
  return out;
}
