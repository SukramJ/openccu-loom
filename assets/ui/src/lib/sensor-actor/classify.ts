// SPDX-License-Identifier: MIT
//
// DP role classification for the sensor + actor tile.
// Pure functions; the tile component consumes the outputs and renders
// each role with the matching primitive (icon, pill, button, …).
//
// Role rules — see docs/ui/sensor-actor-tile-concept.md §"DP classification roles":
//
//   primary           — first read+event DP, selected by primary.ts
//   secondary         — read+event only, not the primary
//   toggle            — read+write, type bool
//   numeric           — read+write, type number-ish (INTEGER/FLOAT/ENUM with min/max)
//   action            — write only, no value (TYPE=ACTION / unspecified)
//   action_with_value — write only, value-typed (number / float / string)

import type { DataPointSummary } from "$lib/api/types";
import { t } from "$lib/i18n";

export type DPRole =
  | "primary"
  | "secondary"
  | "toggle"
  | "numeric"
  | "action"
  | "action_with_value";

export function classifyDP(dp: DataPointSummary, primaryParameter: string): DPRole {
  if (dp.parameter === primaryParameter) return "primary";
  const ops = dp.operations;
  const t = (dp.type ?? "").toUpperCase();

  // Read+write → setting.
  if (ops.read && ops.write) {
    if (t === "BOOL") return "toggle";
    return "numeric";
  }

  // Write-only → action.
  if (!ops.read && ops.write) {
    if (t === "ACTION" || t === "" || t === "BOOL") return "action";
    return "action_with_value";
  }

  // Read-only (event-only counts too): secondary readout.
  return "secondary";
}

/** Bucket holding the per-role split for a channel's DPs. */
export type RoleBuckets = {
  primary?: DataPointSummary;
  secondary: DataPointSummary[];
  toggles: DataPointSummary[];
  numerics: DataPointSummary[];
  actions: DataPointSummary[];
  actionsWithValue: DataPointSummary[];
};

export function bucketDPs(
  dps: readonly DataPointSummary[],
  primaryParameter: string | undefined,
): RoleBuckets {
  const buckets: RoleBuckets = {
    primary: undefined,
    secondary: [],
    toggles: [],
    numerics: [],
    actions: [],
    actionsWithValue: [],
  };
  for (const dp of dps) {
    const role = classifyDP(dp, primaryParameter ?? "");
    switch (role) {
      case "primary":
        buckets.primary = dp;
        break;
      case "secondary":
        buckets.secondary.push(dp);
        break;
      case "toggle":
        buckets.toggles.push(dp);
        break;
      case "numeric":
        buckets.numerics.push(dp);
        break;
      case "action":
        buckets.actions.push(dp);
        break;
      case "action_with_value":
        buckets.actionsWithValue.push(dp);
        break;
    }
  }
  return buckets;
}

/**
 * Format the primary state value for the headline row.
 * - ENUM (e.g. SMOKE_DETECTOR_ALARM_STATUS) → prettified value_list entry
 * - bool → localised label ("An" / "Aus" or caller-provided override)
 * - number → "<value> <unit>"
 */
export function formatPrimaryValue(
  dp: DataPointSummary,
  labels?: { trueLabel?: string; falseLabel?: string },
): string {
  const v = dp.value;
  if (v === null || v === undefined) return "—";
  const enumLabel = enumValueLabel(dp);
  if (enumLabel !== undefined) return enumLabel;
  if (typeof v === "boolean") {
    if (v) return labels?.trueLabel ?? t("quick.on");
    return labels?.falseLabel ?? t("quick.off");
  }
  if (typeof v === "number") {
    const num = formatNumber(v, dp);
    return dp.unit ? `${num} ${dp.unit}` : num;
  }
  return String(v);
}

/**
 * Compact secondary-readout label, e.g. "124 lx" or "21.3 °C".
 * Numbers without a unit fall back to the parameter label so the user
 * sees what the value is for.
 */
export function formatSecondaryValue(dp: DataPointSummary): string {
  const v = dp.value;
  if (v === null || v === undefined) return "—";
  const enumLabel = enumValueLabel(dp);
  if (enumLabel !== undefined) return enumLabel;
  if (typeof v === "boolean") return v ? "✓" : "✗";
  if (typeof v === "number") {
    const num = formatNumber(v, dp);
    return dp.unit ? `${num} ${dp.unit}` : num;
  }
  return String(v);
}

/**
 * Resolve an ENUM-typed DP's value to its value_list label. Accepts
 * numeric indices AND booleans — HmIP-SWDO's STATE for example is a
 * two-value ENUM where the wire carries `false` / `true` but the
 * descriptor's value_list is `["CLOSED", "OPEN"]`. Returns `undefined`
 * when the DP is not an ENUM, the value is out of range, or
 * value_list is missing. The label is title-cased so wire tokens
 * like `IDLE_OFF` surface as `Idle Off`.
 */
function enumValueLabel(dp: DataPointSummary): string | undefined {
  if ((dp.type ?? "").toUpperCase() !== "ENUM") return undefined;
  if (!dp.value_list || dp.value_list.length === 0) return undefined;
  const v = dp.value;
  let idx: number;
  if (typeof v === "number") {
    idx = Math.round(v);
  } else if (typeof v === "boolean") {
    idx = v ? 1 : 0;
  } else {
    return undefined;
  }
  if (idx < 0 || idx >= dp.value_list.length) return undefined;
  return titleCase(dp.value_list[idx]);
}

function formatNumber(n: number, dp: DataPointSummary): string {
  const t = (dp.type ?? "").toUpperCase();
  if (t === "INTEGER" || Number.isInteger(n)) return String(Math.round(n));
  // Heuristic precision: small magnitudes get more decimals.
  const abs = Math.abs(n);
  let frac = 2;
  if (abs >= 100) frac = 0;
  else if (abs >= 10) frac = 1;
  return n.toLocaleString(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: frac,
  });
}

/**
 * Human-readable label for a DP row. The server resolves the caption
 * (`parameter_label`: locale-aware translation, else title-cased
 * parameter) through the shared naming primitives — render it
 * verbatim so all north-bound surfaces agree. The raw parameter is
 * only a transport fallback for responses that predate the field.
 */
export function dpLabel(dp: DataPointSummary): string {
  if (dp.parameter_label && dp.parameter_label.trim()) return dp.parameter_label;
  return dp.parameter;
}

/** Title-cases enum *value* tokens (`IDLE_OFF` → `Idle Off`). Values
 * have no server-resolved caption — only parameter labels do. */
function titleCase(s: string): string {
  return s
    .toLowerCase()
    .split("_")
    .filter((p) => p.length > 0)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join(" ");
}
