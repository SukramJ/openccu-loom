// @vitest-environment happy-dom
import { describe, it, expect, vi } from "vitest";

// A catalogue that knows two keys and renders its variables, so a test
// can tell a translated name from the fallback and see what was filled in.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string>) => {
    const known: Record<string, string> = {
      "feature.name.system.reboot": "Reboot",
      "feature.system.openccu-lite": "openccu-lite",
      "feature.system.unknown": "this system",
    };
    const base = known[key] ?? key;
    return vars ? `${base} ${JSON.stringify(vars)}` : base;
  },
}));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: { byName: (n: string) => (n === "box" ? { system_type: "openccu-lite" } : undefined) },
}));

import { ApiError } from "$lib/api/client";
import { apiErrorMessage, featureName, featureProblem, featureReason } from "./features";

describe("featureName", () => {
  it("names a known feature and falls back to the key", () => {
    expect(featureName("system.reboot")).toBe("Reboot");
    expect(featureName("future.thing")).toBe("future.thing");
  });
});

describe("featureReason", () => {
  it("names the missing scope", () => {
    expect(featureReason({ reason: "missing_scope", scope: "power" })).toBe(
      'feature.reason.missing_scope {"scope":"power"}',
    );
  });

  it("names the system that does not offer it", () => {
    expect(featureReason({ reason: "not_supported_by_system" }, "openccu-lite")).toBe(
      'feature.reason.not_supported_by_system {"system":"openccu-lite"}',
    );
    expect(featureReason({ reason: "not_supported_by_system" }, "something-new")).toBe(
      'feature.reason.not_supported_by_system {"system":"this system"}',
    );
  });

  it("says a system is not ready yet", () => {
    expect(featureReason({ reason: "not_ready" })).toBe("feature.reason.not_ready");
  });
});

describe("feature_unavailable problems", () => {
  const refused = new ApiError(
    422,
    {
      code: "feature_unavailable",
      detail: "central box: system.reboot is not available",
      feature: { central: "box", key: "system.reboot", reason: "missing_scope", scope: "power" },
    },
    "API 422 /system/ccu/box/reboot",
  );

  it("reads the feature a refusal names", () => {
    expect(featureProblem(refused)).toEqual({
      central: "box",
      key: "system.reboot",
      reason: "missing_scope",
      scope: "power",
    });
    expect(featureProblem(new ApiError(422, { code: "validation" }, "x"))).toBeNull();
    expect(featureProblem(new Error("x"))).toBeNull();
  });

  it("words a refusal in the operator's language and passes other errors through", () => {
    const msg = apiErrorMessage(refused);
    expect(msg).toContain("feature.unavailable");
    expect(msg).toContain('"feature":"Reboot"');
    expect(msg).toContain('"central":"box"');
    expect(msg).toContain("feature.reason.missing_scope");
    expect(apiErrorMessage(new Error("boom"))).toBe("boom");
  });
});
