// SPDX-License-Identifier: MIT
//
// Relative-age stamp for data-point readouts ("3 min ago").
// Shared by every readout so all of them pick the same unit
// thresholds and the same localized phrasing.

import { t } from "$lib/i18n";

/**
 * Localized relative age for a data point's `value_age_seconds`.
 * Returns an empty string when the age is unknown, so callers can
 * render the stamp conditionally without a null check.
 */
export function formatValueAge(seconds?: number | null): string {
  if (seconds == null || !Number.isFinite(seconds)) return "";
  if (seconds < 60) return t("sensor_actor.age_sec", { n: Math.floor(seconds) });
  if (seconds < 3600) return t("sensor_actor.age_min", { n: Math.floor(seconds / 60) });
  if (seconds < 86400) return t("sensor_actor.age_hour", { n: Math.floor(seconds / 3600) });
  return t("sensor_actor.age_day", { n: Math.floor(seconds / 86400) });
}
