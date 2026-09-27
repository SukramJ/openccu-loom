import { ApiError } from "$lib/api/client";
import type { CentralFeatureState, SystemCCUEntry } from "$lib/api/types";
import { t } from "$lib/i18n";
import { centralStore } from "$lib/stores/centrals.svelte";

/**
 * Per-central features, in the words an operator reads. The daemon names
 * what a central cannot do (`GET /system/ccu` `features`, the
 * `feature_unavailable` problem); these helpers turn that into the
 * localized sentence the views show next to a hidden action.
 */

/** The display name of a feature key; the key itself when unknown. */
export function featureName(key: string): string {
  const k = `feature.name.${key}`;
  const name = t(k);
  return name === k ? key : name;
}

function systemName(systemType: string | undefined): string {
  const k = `feature.system.${systemType ?? "unknown"}`;
  const name = t(k);
  return name === k ? t("feature.system.unknown") : name;
}

/** Why a feature is unavailable: "openccu-lite does not offer it", … */
export function featureReason(
  state: Pick<CentralFeatureState, "reason" | "scope"> | undefined,
  systemType?: string,
): string {
  switch (state?.reason) {
    case "missing_scope":
      return t("feature.reason.missing_scope", { scope: state.scope ?? "" });
    case "not_ready":
      return t("feature.reason.not_ready");
    default:
      return t("feature.reason.not_supported_by_system", { system: systemName(systemType) });
  }
}

/** The reason one central lacks a feature. */
export function centralFeatureReason(central: SystemCCUEntry, key: string): string {
  return featureReason(central.features?.[key], central.system_type);
}

/** The `feature` member of a `feature_unavailable` problem. */
export type FeatureProblem = {
  central: string;
  key: string;
  reason: CentralFeatureState["reason"];
  scope?: string;
};

/** The feature a refused request names, or null for any other error. */
export function featureProblem(err: unknown): FeatureProblem | null {
  if (!(err instanceof ApiError) || err.problemCode !== "feature_unavailable") return null;
  const b = err.body as { feature?: FeatureProblem } | null;
  return b && typeof b === "object" && b.feature ? b.feature : null;
}

/**
 * The message for a failed request: a refused feature in the operator's
 * language, anything else as the daemon worded it.
 */
export function apiErrorMessage(err: unknown): string {
  const f = featureProblem(err);
  if (f) {
    return t("feature.unavailable", {
      feature: featureName(f.key),
      central: f.central,
      reason: featureReason(f, centralStore.byName(f.central)?.system_type),
    });
  }
  return err instanceof Error ? err.message : String(err);
}
