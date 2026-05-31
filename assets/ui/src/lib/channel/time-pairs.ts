import type { UISchemaParameter } from "$lib/api/types";

/**
 * The CCU models delay/duration config with two companion parameters
 * rather than a single seconds-typed field. Two shapes exist:
 *
 *   - HmIP style:     `<PREFIX>_UNIT` + `<PREFIX>_VALUE`
 *     Unit is an index into a ms/s/min/h table, Value is a raw int.
 *
 *   - Classic HM:     `<PREFIX>_TIME_BASE` + `<PREFIX>_TIME_FACTOR`
 *     Base is an index into an 0.1/1/5/10/60/300/600/3600 s table,
 *     Factor is the multiplier.
 *
 * Both patterns are rendered as a single preset picker (with a
 * "Benutzerdefiniert"-fallback that shows the raw fields) to match
 * the HomematicIP-Local frontend's UX and the CCU WebUI.
 *
 * Ports the detection + preset tables from:
 *   homematicip-local-frontend/packages/config-panel/src/components/config-form.ts
 *   aiohomematic-config/aiohomematic_config/link_param_metadata.py
 */

export type TimePairShape = "hmip_unit_value" | "hm_base_factor";

export type TimePreset = {
  /** unit / base component — always a 0..N index */
  a: number;
  /** value / factor component */
  b: number;
  labelEn: string;
  labelDe: string;
};

/** Pick the active-locale label for a preset. */
export function presetLabel(p: TimePreset, locale: string): string {
  return locale === "de" ? p.labelDe : p.labelEn;
}

export type TimePair = {
  prefix: string;
  shape: TimePairShape;
  /** Parameter that carries the unit / base enum index. */
  unitParam: UISchemaParameter;
  /** Parameter that carries the raw value / factor number. */
  valueParam: UISchemaParameter;
  /**
   * Optional locale-specific preset list supplied by the backend. When
   * the LINK-paramset classifier attaches `time_presets` to the unit
   * parameter, we carry them through so the picker offers the right
   * selector (TIME_ON_OFF vs DELAY vs RAMP_ON_OFF) instead of the
   * name-heuristic default.
   */
  presets?: TimePreset[];
};

/**
 * HmIP preset list. Mirrors `config-form.ts:TIME_PRESETS`; UI shows
 * the human-readable label, values are stored as the raw {unit,
 * value} pair when the user picks a preset.
 */
export const HMIP_TIME_PRESETS: TimePreset[] = [
  { a: 0, b: 0, labelEn: "Not active", labelDe: "Nicht aktiv" },
  { a: 0, b: 1, labelEn: "100 ms", labelDe: "100 ms" },
  { a: 0, b: 3, labelEn: "300 ms", labelDe: "300 ms" },
  { a: 0, b: 5, labelEn: "500 ms", labelDe: "500 ms" },
  { a: 0, b: 15, labelEn: "1500 ms", labelDe: "1500 ms" },
  { a: 1, b: 1, labelEn: "1 second", labelDe: "1 Sekunde" },
  { a: 1, b: 2, labelEn: "2 seconds", labelDe: "2 Sekunden" },
  { a: 1, b: 3, labelEn: "3 seconds", labelDe: "3 Sekunden" },
  { a: 1, b: 30, labelEn: "30 seconds", labelDe: "30 Sekunden" },
  { a: 2, b: 1, labelEn: "1 minute", labelDe: "1 Minute" },
  { a: 2, b: 2, labelEn: "2 minutes", labelDe: "2 Minuten" },
  { a: 2, b: 4, labelEn: "4 minutes", labelDe: "4 Minuten" },
  { a: 2, b: 15, labelEn: "15 minutes", labelDe: "15 Minuten" },
];

/**
 * Classic HM preset list. Taken from aiohomematic-config's
 * `_TIME_ON_OFF_PRESETS`; covers the most common ON/OFF_TIME /
 * ONDELAY_TIME / RAMP_ON_TIME configurations.
 */
export const HM_TIME_PRESETS: TimePreset[] = [
  { a: 0, b: 0, labelEn: "Not active", labelDe: "Nicht aktiv" },
  { a: 0, b: 1, labelEn: "100 ms", labelDe: "100 ms" },
  { a: 1, b: 1, labelEn: "1 s", labelDe: "1 s" },
  { a: 1, b: 2, labelEn: "2 s", labelDe: "2 s" },
  { a: 1, b: 3, labelEn: "3 s", labelDe: "3 s" },
  { a: 2, b: 1, labelEn: "5 s", labelDe: "5 s" },
  { a: 3, b: 1, labelEn: "10 s", labelDe: "10 s" },
  { a: 3, b: 3, labelEn: "30 s", labelDe: "30 s" },
  { a: 4, b: 1, labelEn: "1 min", labelDe: "1 min" },
  { a: 4, b: 2, labelEn: "2 min", labelDe: "2 min" },
  { a: 5, b: 1, labelEn: "5 min", labelDe: "5 min" },
  { a: 6, b: 1, labelEn: "10 min", labelDe: "10 min" },
  { a: 6, b: 3, labelEn: "30 min", labelDe: "30 min" },
  { a: 7, b: 1, labelEn: "1 h", labelDe: "1 h" },
  { a: 7, b: 2, labelEn: "2 h", labelDe: "2 h" },
  { a: 7, b: 3, labelEn: "3 h", labelDe: "3 h" },
  { a: 7, b: 5, labelEn: "5 h", labelDe: "5 h" },
  { a: 7, b: 8, labelEn: "8 h", labelDe: "8 h" },
  { a: 7, b: 12, labelEn: "12 h", labelDe: "12 h" },
  { a: 7, b: 24, labelEn: "24 h", labelDe: "24 h" },
  { a: 7, b: 31, labelEn: "Permanent", labelDe: "Permanent" },
];

export function presetsFor(shape: TimePairShape): TimePreset[] {
  return shape === "hmip_unit_value" ? HMIP_TIME_PRESETS : HM_TIME_PRESETS;
}

/**
 * Detect every time pair inside a flat parameter list. The returned
 * map is keyed by prefix; a single parameter belongs to at most one
 * pair. `paired` contains the parameter names that should be hidden
 * from the generic per-parameter renderer because their pair covers
 * them.
 */
export function detectTimePairs(
  params: UISchemaParameter[],
): { pairs: TimePair[]; paired: Set<string> } {
  const byName = new Map(params.map((p) => [p.name, p]));
  const pairs: TimePair[] = [];
  const paired = new Set<string>();

  // Metadata-driven pairing first: when the schema exposes the LINK
  // classifier's `time_pair_id`, prefer that over the name heuristic —
  // the schema knows e.g. that `ONDELAY_TIME` uses the DELAY selector,
  // which the name heuristic can't tell apart from a regular ON_TIME.
  const byPairID = new Map<string, { base?: UISchemaParameter; factor?: UISchemaParameter }>();
  for (const p of params) {
    const pairID = p.time_pair_id;
    if (!pairID) continue;
    const slot = byPairID.get(pairID) ?? {};
    if (p.name.endsWith("_BASE") || p.name.endsWith("_UNIT")) slot.base = p;
    else if (p.name.endsWith("_FACTOR") || p.name.endsWith("_VALUE")) slot.factor = p;
    byPairID.set(pairID, slot);
  }
  for (const [pairID, slot] of byPairID) {
    if (!slot.base || !slot.factor) continue;
    const shape: TimePairShape = slot.base.name.endsWith("_UNIT")
      ? "hmip_unit_value"
      : "hm_base_factor";
    const presets = (slot.base.time_presets ?? slot.factor.time_presets)?.map((tp) => ({
      a: tp.base,
      b: tp.factor,
      labelEn: tp.label,
      labelDe: tp.label,
    }));
    pairs.push({
      prefix: pairID,
      shape,
      unitParam: slot.base,
      valueParam: slot.factor,
      presets,
    });
    paired.add(slot.base.name);
    paired.add(slot.factor.name);
  }

  // Name heuristic for anything the classifier did not tag — e.g.
  // MASTER-paramset params that reach this renderer.
  for (const p of params) {
    if (paired.has(p.name)) continue;
    if (p.name.endsWith("_UNIT")) {
      const prefix = p.name.slice(0, -5);
      const companion = byName.get(`${prefix}_VALUE`);
      if (companion && !paired.has(companion.name)) {
        pairs.push({
          prefix,
          shape: "hmip_unit_value",
          unitParam: p,
          valueParam: companion,
        });
        paired.add(p.name);
        paired.add(companion.name);
      }
    } else if (p.name.endsWith("_TIME_BASE")) {
      const prefix = p.name.slice(0, -10);
      const companion = byName.get(`${prefix}_TIME_FACTOR`);
      if (companion && !paired.has(companion.name)) {
        pairs.push({
          prefix: `${prefix}_TIME`,
          shape: "hm_base_factor",
          unitParam: p,
          valueParam: companion,
        });
        paired.add(p.name);
        paired.add(companion.name);
      }
    }
  }

  return { pairs, paired };
}

/**
 * Derive a display label for the pair. Given both parameters are
 * labelled "X Value" / "Wert X", strip the redundant suffix so the
 * combined row reads "X". Falls back to the value parameter's label
 * when the heuristic misses.
 */
export function derivePairLabel(
  pair: TimePair,
  locale: string,
): string {
  const base = pair.valueParam.label ?? pair.valueParam.name;
  if (locale === "de") {
    if (base.startsWith("Wert ")) return base.slice(5);
  } else if (base.endsWith(" Value")) {
    return base.slice(0, -6);
  }
  return base;
}

/**
 * Return the index of the preset that exactly matches the current
 * {unit, value} pair, or -1 if no preset fits (custom mode).
 */
export function matchPresetIndex(
  shape: TimePairShape,
  a: unknown,
  b: unknown,
): number {
  return matchPresetIndexIn(presetsFor(shape), a, b);
}

/**
 * Same as [matchPresetIndex] but operates on a caller-supplied preset
 * list. Used by LINK-paramset renderers where the backend ships the
 * selector-specific list (TIME_ON_OFF / DELAY / RAMP_ON_OFF) rather
 * than deriving it from the shape alone.
 */
export function matchPresetIndexIn(
  presets: TimePreset[],
  a: unknown,
  b: unknown,
): number {
  const na = Number(a);
  const nb = Number(b);
  if (!Number.isFinite(na) || !Number.isFinite(nb)) return -1;
  return presets.findIndex((p) => p.a === na && p.b === nb);
}
