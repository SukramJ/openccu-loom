import type { UISchemaParameter } from "$lib/api/types";
import { t } from "$lib/i18n";

/**
 * Projection between a parameter's raw CCU wire value and the value an
 * operator sees.
 *
 * This lives apart from `ParameterField.svelte` because two surfaces have to
 * agree on it: the field itself, and the write preview, which reports "from
 * X to Y" about values the operator only ever saw in display form. A second
 * implementation of the projection would let the preview claim a change the
 * field never showed — the preview's whole purpose is to be believed.
 */

/**
 * Multiplier converting the raw wire value into the unit `parameter.unit`
 * names (LEVEL raw 0.42, unit "%", multiplier 100 → 42). Absent or zero
 * means no projection.
 */
export function multiplierOf(param: Pick<UISchemaParameter, "multiplier">): number {
  return typeof param.multiplier === "number" && param.multiplier !== 0
    ? param.multiplier
    : 1;
}

/**
 * IEEE-754 multiplication leaves noise a human never asked for
 * (0.55 * 100 === 55.00000000000001), which defeats `Number.isInteger`
 * checks and leaks garbage digits into the display. Round-tripping through
 * 12 significant digits discards that noise while keeping every decimal a
 * real projection needs.
 */
export function cleanFloat(n: number): number {
  return Number(n.toPrecision(12));
}

/** Project a raw wire value into the displayed unit. */
export function toDisplay(
  param: Pick<UISchemaParameter, "multiplier">,
  v: unknown,
): unknown {
  const multiplier = multiplierOf(param);
  if (multiplier === 1 || typeof v !== "number" || !Number.isFinite(v)) return v;
  return cleanFloat(v * multiplier);
}

/** Inverse of [toDisplay]: the working-values map stays in raw wire units. */
export function fromDisplay(
  param: Pick<UISchemaParameter, "multiplier">,
  n: number,
): number {
  const multiplier = multiplierOf(param);
  return multiplier === 1 ? n : cleanFloat(n / multiplier);
}

/**
 * The value as a human reads it: an enum's label, a boolean's on/off word,
 * a number in its displayed unit. Raw values that carry no projection pass
 * through as their own string form.
 */
export function formatDisplayValue(
  param: Pick<
    UISchemaParameter,
    "type" | "unit" | "value_list" | "multiplier"
  >,
  v: unknown,
): string {
  if (v == null || v === "") return "—";
  if (param.type === "BOOL") return v ? t("quick.on") : t("quick.off");
  if (param.type === "ENUM") {
    const list = param.value_list ?? [];
    const num = Number(v);
    const hit = Number.isFinite(num) ? list.find((e) => e.value === num) : undefined;
    return hit?.label || hit?.key || String(v);
  }
  const n = Number(toDisplay(param, v));
  if (!Number.isFinite(n)) return String(v);
  // INTEGER shows as an integer — unless a projection made it fractional
  // (TIME_OF_OPERATION: seconds → days), where rounding would erase the
  // very thing the projection exists to show.
  const multiplier = multiplierOf(param);
  const text =
    param.type === "INTEGER" && multiplier === 1
      ? String(Math.round(n))
      : Number.isInteger(n)
        ? String(n)
        : n.toFixed(1);
  return param.unit ? `${text} ${param.unit}` : text;
}
