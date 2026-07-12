// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

import { describe, it, expect, beforeEach, vi } from "vitest";

// $lib/i18n's `t()` reads `prefs.locale` reactively; mock the
// preferences store so this pure-logic suite doesn't need a DOM
// (localStorage / navigator) environment. vi.mock factories are
// hoisted above top-level const declarations, so the mocked object
// must be created via vi.hoisted() to be visible inside it — mirrors
// the pattern in SourceBadge.test.ts.
const { prefs } = vi.hoisted(() => ({ prefs: { locale: "en" as "en" | "de" } }));
vi.mock("$lib/stores/preferences.svelte", () => ({ prefs }));

import {
  HMIP_TIME_PRESETS,
  HM_TIME_PRESETS,
  derivePairLabel,
  detectTimePairs,
  matchPresetIndex,
  matchPresetIndexIn,
  presetLabel,
  presetsFor,
  type TimePreset,
} from "./time-pairs";
import type { UISchemaParameter } from "$lib/api/types";

beforeEach(() => {
  prefs.locale = "en";
});

function param(name: string, extra: Partial<UISchemaParameter> = {}): UISchemaParameter {
  return {
    name,
    type: "INTEGER",
    operations: { read: true, write: true, event: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    ...extra,
  };
}

describe("presetLabel", () => {
  const preset: TimePreset = { a: 0, b: 0, labelKey: "parameter.time_preset.not_active" };

  it("resolves a catalogue key for the active prefs.locale, falling back to English", () => {
    prefs.locale = "de";
    expect(presetLabel(preset)).toBe("Nicht aktiv");

    prefs.locale = "en";
    expect(presetLabel(preset)).toBe("Not active");
  });

  it("passes a literal, locale-identical label straight through", () => {
    const literal: TimePreset = { a: 0, b: 1, labelKey: "100 ms" };
    prefs.locale = "de";
    expect(presetLabel(literal)).toBe("100 ms");
    prefs.locale = "en";
    expect(presetLabel(literal)).toBe("100 ms");
  });
});

describe("presetsFor", () => {
  it("returns the HmIP table for hmip_unit_value and the classic table otherwise", () => {
    expect(presetsFor("hmip_unit_value")).toBe(HMIP_TIME_PRESETS);
    expect(presetsFor("hm_base_factor")).toBe(HM_TIME_PRESETS);
  });
});

describe("detectTimePairs", () => {
  it("pairs a *_UNIT / *_VALUE name-heuristic pair as hmip_unit_value", () => {
    const params = [param("ON_TIME_UNIT"), param("ON_TIME_VALUE"), param("STATE")];
    const { pairs, paired } = detectTimePairs(params);
    expect(pairs).toHaveLength(1);
    expect(pairs[0].shape).toBe("hmip_unit_value");
    expect(pairs[0].prefix).toBe("ON_TIME");
    expect(pairs[0].unitParam.name).toBe("ON_TIME_UNIT");
    expect(pairs[0].valueParam.name).toBe("ON_TIME_VALUE");
    expect(paired.has("ON_TIME_UNIT")).toBe(true);
    expect(paired.has("ON_TIME_VALUE")).toBe(true);
    expect(paired.has("STATE")).toBe(false);
  });

  it("pairs a *_TIME_BASE / *_TIME_FACTOR name-heuristic pair as hm_base_factor", () => {
    const params = [param("ON_TIME_BASE"), param("ON_TIME_FACTOR")];
    const { pairs, paired } = detectTimePairs(params);
    expect(pairs).toHaveLength(1);
    expect(pairs[0].shape).toBe("hm_base_factor");
    expect(pairs[0].prefix).toBe("ON_TIME");
    expect(paired.has("ON_TIME_BASE")).toBe(true);
    expect(paired.has("ON_TIME_FACTOR")).toBe(true);
  });

  it("leaves an unpaired _UNIT parameter alone when its companion is missing", () => {
    const params = [param("ON_TIME_UNIT")];
    const { pairs, paired } = detectTimePairs(params);
    expect(pairs).toHaveLength(0);
    expect(paired.size).toBe(0);
  });

  it("prefers metadata-driven pairing (time_pair_id) over the name heuristic", () => {
    const params = [
      param("ONDELAY_TIME_UNIT", {
        time_pair_id: "ondelay",
        time_presets: [{ base: 0, factor: 5, label: "500 ms" }],
      }),
      param("ONDELAY_TIME_VALUE", { time_pair_id: "ondelay" }),
    ];
    const { pairs, paired } = detectTimePairs(params);
    expect(pairs).toHaveLength(1);
    expect(pairs[0].shape).toBe("hmip_unit_value");
    expect(pairs[0].presets).toEqual([{ a: 0, b: 5, labelKey: "500 ms" }]);
    expect(paired.has("ONDELAY_TIME_UNIT")).toBe(true);
    expect(paired.has("ONDELAY_TIME_VALUE")).toBe(true);
  });

  it("falls back to the name heuristic when only one side carries time_pair_id", () => {
    // The classifier tagged only the unit side; the byPairID slot is
    // incomplete (no factor), so the metadata pass yields no pair. The
    // name-heuristic pass must still complete the *_UNIT/*_VALUE match.
    const params = [
      param("ONDELAY_TIME_UNIT", { time_pair_id: "ondelay" }),
      param("ONDELAY_TIME_VALUE"),
    ];
    const { pairs } = detectTimePairs(params);
    expect(pairs).toHaveLength(1);
    expect(pairs[0].unitParam.name).toBe("ONDELAY_TIME_UNIT");
    expect(pairs[0].valueParam.name).toBe("ONDELAY_TIME_VALUE");
  });

  it("ignores a metadata pair_id group missing one of its two sides", () => {
    const params = [
      param("ONDELAY_TIME_UNIT", { time_pair_id: "ondelay" }),
      param("UNRELATED"),
    ];
    const { pairs } = detectTimePairs(params);
    expect(pairs).toHaveLength(0);
  });
});

describe("derivePairLabel", () => {
  it("strips the German 'Wert ' prefix", () => {
    const pair = {
      prefix: "ON_TIME",
      shape: "hmip_unit_value" as const,
      unitParam: param("ON_TIME_UNIT"),
      valueParam: param("ON_TIME_VALUE", { label: "Wert Einschaltdauer" }),
    };
    expect(derivePairLabel(pair, "de")).toBe("Einschaltdauer");
  });

  it("strips the English ' Value' suffix", () => {
    const pair = {
      prefix: "ON_TIME",
      shape: "hmip_unit_value" as const,
      unitParam: param("ON_TIME_UNIT"),
      valueParam: param("ON_TIME_VALUE", { label: "On time Value" }),
    };
    expect(derivePairLabel(pair, "en")).toBe("On time");
  });

  it("falls back to the raw label when the heuristic does not match", () => {
    const pair = {
      prefix: "ON_TIME",
      shape: "hmip_unit_value" as const,
      unitParam: param("ON_TIME_UNIT"),
      valueParam: param("ON_TIME_VALUE", { label: "Duration" }),
    };
    expect(derivePairLabel(pair, "en")).toBe("Duration");
  });

  it("falls back to the parameter name when there is no label at all", () => {
    const pair = {
      prefix: "ON_TIME",
      shape: "hmip_unit_value" as const,
      unitParam: param("ON_TIME_UNIT"),
      valueParam: param("ON_TIME_VALUE"),
    };
    expect(derivePairLabel(pair, "en")).toBe("ON_TIME_VALUE");
  });
});

describe("matchPresetIndex / matchPresetIndexIn", () => {
  it("finds the exact matching preset for a shape", () => {
    // HMIP_TIME_PRESETS[5] === { a: 1, b: 1, ... } ("1 second").
    expect(matchPresetIndex("hmip_unit_value", 1, 1)).toBe(5);
  });

  it("returns -1 for a custom (non-preset) combination", () => {
    expect(matchPresetIndex("hmip_unit_value", 9, 9)).toBe(-1);
  });

  it("returns -1 when either operand is non-numeric", () => {
    expect(matchPresetIndex("hmip_unit_value", "x", 1)).toBe(-1);
  });

  it("matches against a caller-supplied preset list via matchPresetIndexIn", () => {
    const presets: TimePreset[] = [
      { a: 0, b: 5, labelKey: "500 ms" },
    ];
    expect(matchPresetIndexIn(presets, 0, 5)).toBe(0);
    expect(matchPresetIndexIn(presets, 0, 6)).toBe(-1);
  });

  it("coerces string numeric operands the same way as native numbers", () => {
    expect(matchPresetIndexIn(HM_TIME_PRESETS, "0", "0")).toBe(0);
  });
});
