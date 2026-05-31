// SPDX-License-Identifier: MIT
//
// State-color rules for the AutoTile composer.
// The backend `ui_hint.state_color_rule` carries a named threshold
// rule (e.g. "temp_heat", "humidity_band", "alarm_active"); this
// module turns the rule + the DP's current value into a CSS colour
// token, or undefined when the rule doesn't fire for this value.
//
// Adding a rule is a single switch-case below + a row in the Go
// catalogue. Keep them in sync.

import type { DataPointSummary } from "$lib/api/types";

/**
 * Resolves the state colour for a DP. Returns undefined when no
 * rule applies; AutoTile then renders the readout in the default
 * text colour.
 */
export function stateColorFor(dp: DataPointSummary): string | undefined {
  const rule = dp.ui_hint?.state_color_rule;
  if (!rule) return undefined;
  const v = dp.value;
  switch (rule) {
    case "temp_heat":
      if (typeof v !== "number") return undefined;
      if (v >= 24) return "var(--ha-warning-color)"; // heat orange
      if (v <= 18) return "var(--ha-info-color)"; //   cool blue
      return undefined;
    case "humidity_band":
      if (typeof v !== "number") return undefined;
      if (v < 30) return "var(--ha-warning-color)"; // dry yellow
      if (v > 70) return "var(--ha-info-color)"; //   damp blue
      return undefined;
    case "alarm_active":
      if (typeof v === "boolean") return v ? "var(--ha-error-color)" : undefined;
      if (typeof v === "number") {
        // ENUMs: index > 0 = non-idle → alarm
        return Math.round(v) > 0 ? "var(--ha-error-color)" : undefined;
      }
      return undefined;
    case "signal_weak":
      if (typeof v !== "number") return undefined;
      return v < -85 ? "var(--ha-error-color)" : undefined;
    case "particulate_band":
      if (typeof v !== "number") return undefined;
      if (v > 50) return "var(--ha-error-color)";
      if (v > 25) return "var(--ha-warning-color)";
      return undefined;
  }
  return undefined;
}

/**
 * Tile-level tint colour for the worst-case lifecycle across a
 * channel's DPs. Used as the AutoTile background hint per wizard
 * Q3 (hybrid: tile tint + per-readout age stamp).
 */
export function lifecycleTint(
  worst: "live" | "cache" | "stale" | "unobserved",
): string | undefined {
  switch (worst) {
    case "cache":
      return "color-mix(in oklab, var(--ha-warning-color) 10%, transparent)";
    case "stale":
      return "color-mix(in oklab, var(--ha-error-color) 10%, transparent)";
    case "unobserved":
      return "color-mix(in oklab, var(--ha-secondary-text-color) 8%, transparent)";
    case "live":
      return undefined;
  }
  return undefined;
}
