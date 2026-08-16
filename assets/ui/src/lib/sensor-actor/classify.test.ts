// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// ENUM value captions: the SPA catalogue owns the curated generic
// tokens, the daemon's per-value `value_translations` cover the long
// tail, and title-casing is only the last resort.
import { describe, it, expect, vi } from "vitest";
import type { DataPointSummary } from "$lib/api/types";

// Only the curated tokens resolve; every other key echoes back, which is
// how the real catalogue signals "no entry".
const CATALOGUE: Record<string, string> = {
  "enum.OPEN": "Offen",
};

vi.mock("$lib/i18n", () => ({
  t: (key: string) => CATALOGUE[key] ?? key,
}));

import { enumValueLabel, enumValueText } from "./classify";

function enumDP(overrides: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    parameter: "DOOR_STATE",
    type: "ENUM",
    value: 2,
    value_list: ["CLOSED", "OPEN", "VENTILATION_POSITION"],
    observed: true,
    operations: { read: true, write: false, event: true },
    ...overrides,
  } as DataPointSummary;
}

describe("enumValueText", () => {
  it("prefers the SPA catalogue entry", () => {
    expect(enumValueText("OPEN", { OPEN: "geöffnet" })).toBe("Offen");
  });

  it("falls back to the daemon's value translation", () => {
    expect(
      enumValueText("VENTILATION_POSITION", {
        VENTILATION_POSITION: "Lüftungsstellung",
      }),
    ).toBe("Lüftungsstellung");
  });

  it("title-cases the raw token when nothing translates it", () => {
    expect(enumValueText("VENTILATION_POSITION")).toBe("Ventilation Position");
  });
});

describe("enumValueLabel", () => {
  it("renders the daemon's translation for the resolved index", () => {
    const dp = enumDP({
      value_translations: { VENTILATION_POSITION: "Lüftungsstellung" },
    } as Partial<DataPointSummary>);
    expect(enumValueLabel(dp)).toBe("Lüftungsstellung");
  });

  it("title-cases when the data point carries no translations", () => {
    expect(enumValueLabel(enumDP())).toBe("Ventilation Position");
  });
});
