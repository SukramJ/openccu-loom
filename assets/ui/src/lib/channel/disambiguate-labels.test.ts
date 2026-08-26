// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import { disambiguateLabels } from "./disambiguate-labels";

function p(name: string, label?: string) {
  return { name, label };
}

describe("disambiguateLabels", () => {
  it("infers upper/lower direction for an ABOVE/BELOW threshold pair", () => {
    const out = disambiguateLabels([
      p("COND_TX_CYCLIC_ABOVE", "Entscheidungswert zyklisch senden"),
      p("COND_TX_CYCLIC_BELOW", "Entscheidungswert zyklisch senden"),
    ]);
    expect(out.get("COND_TX_CYCLIC_ABOVE")).toEqual({
      direction: "upper",
      emphasizeName: false,
    });
    expect(out.get("COND_TX_CYCLIC_BELOW")).toEqual({
      direction: "lower",
      emphasizeName: false,
    });
  });

  it("recognises the _HI/_LO and _TOP/_BOTTOM suffix families too", () => {
    const out = disambiguateLabels([
      p("TEMP_HI", "Temperatur"),
      p("TEMP_LO", "Temperatur"),
      p("RANGE_TOP", "Bereich"),
      p("RANGE_BOTTOM", "Bereich"),
    ]);
    expect(out.get("TEMP_HI")?.direction).toBe("upper");
    expect(out.get("TEMP_LO")?.direction).toBe("lower");
    expect(out.get("RANGE_TOP")?.direction).toBe("upper");
    expect(out.get("RANGE_BOTTOM")?.direction).toBe("lower");
  });

  it("flags emphasizeName for a duplicate with no directional suffix", () => {
    const out = disambiguateLabels([
      p("SPECIAL_MODE_A", "Sondermodus"),
      p("SPECIAL_MODE_B", "Sondermodus"),
    ]);
    expect(out.get("SPECIAL_MODE_A")).toEqual({
      direction: null,
      emphasizeName: true,
    });
    expect(out.get("SPECIAL_MODE_B")).toEqual({
      direction: null,
      emphasizeName: true,
    });
  });

  it("leaves unique labels untouched (no map entry)", () => {
    const out = disambiguateLabels([
      p("LEVEL", "Pegel"),
      p("RAMP_TIME", "Rampenzeit"),
      p("ON_TIME", "Einschaltdauer"),
    ]);
    expect(out.size).toBe(0);
  });

  it("only disambiguates the colliding pair inside a mixed group", () => {
    const out = disambiguateLabels([
      p("LEVEL", "Pegel"),
      p("COND_VALUE_ABOVE", "Bedingungswert"),
      p("COND_VALUE_BELOW", "Bedingungswert"),
    ]);
    expect(out.has("LEVEL")).toBe(false);
    expect(out.get("COND_VALUE_ABOVE")?.direction).toBe("upper");
    expect(out.get("COND_VALUE_BELOW")?.direction).toBe("lower");
  });

  it("treats parameters without a label by their machine name, so distinct names never collide", () => {
    const out = disambiguateLabels([p("FOO"), p("BAR")]);
    expect(out.size).toBe(0);
  });
});
