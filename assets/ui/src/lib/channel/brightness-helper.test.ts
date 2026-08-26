// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import type { DataPointSummary } from "$lib/api/types";
import {
  coerceNumber,
  formatReading,
  isBrightnessDataPoint,
  isConditionValueParam,
  pickBrightnessReading,
} from "./brightness-helper";

function dp(
  parameter: string,
  value: unknown,
  extra: Partial<DataPointSummary> = {},
): DataPointSummary {
  return {
    parameter,
    value,
    observed: true,
    operations: { read: true, write: false, event: true },
    ...extra,
  } as DataPointSummary;
}

describe("isConditionValueParam", () => {
  it("matches the SHORT_/LONG_ COND_VALUE_LO/_HI thresholds", () => {
    for (const name of [
      "SHORT_COND_VALUE_LO",
      "SHORT_COND_VALUE_HI",
      "LONG_COND_VALUE_LO",
      "LONG_COND_VALUE_HI",
    ]) {
      expect(isConditionValueParam(name)).toBe(true);
    }
  });

  it("rejects unrelated LINK parameters", () => {
    for (const name of [
      "SHORT_CT_ON",
      "SHORT_COND_VALUE", // no LO/HI suffix
      "COND_VALUE_LO", // no SHORT_/LONG_ prefix
      "SHORT_ON_TIME",
      "LEVEL",
    ]) {
      expect(isConditionValueParam(name)).toBe(false);
    }
  });
});

describe("isBrightnessDataPoint", () => {
  it("recognises the brightness / illuminance families", () => {
    expect(isBrightnessDataPoint("BRIGHTNESS")).toBe(true);
    expect(isBrightnessDataPoint("ILLUMINATION")).toBe(true);
    expect(isBrightnessDataPoint("CURRENT_ILLUMINATION")).toBe(true);
    expect(isBrightnessDataPoint("STATE")).toBe(false);
    expect(isBrightnessDataPoint("MOTION")).toBe(false);
  });
});

describe("pickBrightnessReading", () => {
  it("returns null when the channel exposes no brightness data point", () => {
    expect(pickBrightnessReading([dp("MOTION", true), dp("STATE", false)])).toBe(
      null,
    );
  });

  it("reads the classic BidCos BRIGHTNESS reading (0..255, no unit)", () => {
    const reading = pickBrightnessReading([dp("MOTION", true), dp("BRIGHTNESS", 128)]);
    expect(reading).toEqual({ parameter: "BRIGHTNESS", value: 128, unit: null });
  });

  it("reads an HmIP ILLUMINATION reading with its lux unit", () => {
    const reading = pickBrightnessReading([
      dp("CURRENT_ILLUMINATION", 42.5, { unit: "lx" }),
    ]);
    expect(reading).toEqual({
      parameter: "CURRENT_ILLUMINATION",
      value: 42.5,
      unit: "lx",
    });
  });

  it("prefers BRIGHTNESS over the illuminance variants when both exist", () => {
    const reading = pickBrightnessReading([
      dp("ILLUMINATION", 300, { unit: "lx" }),
      dp("BRIGHTNESS", 90),
    ]);
    expect(reading?.parameter).toBe("BRIGHTNESS");
  });

  it("coerces a numeric string value", () => {
    const reading = pickBrightnessReading([dp("BRIGHTNESS", "77")]);
    expect(reading?.value).toBe(77);
  });

  it("skips a brightness data point that has never reported a value", () => {
    expect(
      pickBrightnessReading([dp("BRIGHTNESS", undefined, { observed: false })]),
    ).toBe(null);
    expect(pickBrightnessReading([dp("BRIGHTNESS", null)])).toBe(null);
    expect(pickBrightnessReading([dp("BRIGHTNESS", "not-a-number")])).toBe(null);
  });

  it("skips a brightness data point that is not readable", () => {
    expect(
      pickBrightnessReading([
        dp("BRIGHTNESS", 128, {
          operations: { read: false, write: false, event: true },
        }),
      ]),
    ).toBe(null);
  });
});

describe("coerceNumber", () => {
  it("passes a finite number through unchanged (happy path)", () => {
    expect(coerceNumber(128)).toBe(128);
    expect(coerceNumber(-12.5)).toBe(-12.5);
    expect(coerceNumber(0)).toBe(0);
  });

  it("rejects non-finite numbers", () => {
    expect(coerceNumber(Number.NaN)).toBe(null);
    expect(coerceNumber(Number.POSITIVE_INFINITY)).toBe(null);
    expect(coerceNumber(Number.NEGATIVE_INFINITY)).toBe(null);
  });

  it("parses a numeric string, trimming surrounding whitespace", () => {
    expect(coerceNumber("77")).toBe(77);
    expect(coerceNumber("  42.5  ")).toBe(42.5);
    expect(coerceNumber("-3")).toBe(-3);
  });

  it("rejects a blank or non-numeric string", () => {
    expect(coerceNumber("")).toBe(null);
    expect(coerceNumber("   ")).toBe(null);
    expect(coerceNumber("not-a-number")).toBe(null);
  });

  it("rejects values of other types", () => {
    expect(coerceNumber(null)).toBe(null);
    expect(coerceNumber(undefined)).toBe(null);
    expect(coerceNumber(true)).toBe(null);
    expect(coerceNumber({})).toBe(null);
  });
});

describe("formatReading", () => {
  it("keeps integers integral and appends the unit when present", () => {
    expect(formatReading(128, null)).toBe("128");
    expect(formatReading(300, "lx")).toBe("300 lx");
  });

  it("renders one decimal for a fractional reading", () => {
    expect(formatReading(42.53, "lx")).toBe("42.5 lx");
  });
});
