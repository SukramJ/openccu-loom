// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
  detectDstGroups,
  dstHeader,
  hhmmToMinutes,
  isDstTimeParam,
  minutesToHHMM,
} from "./dst-groups";
import type { UISchemaParameter } from "$lib/api/types";

function param(name: string): UISchemaParameter {
  return {
    name,
    type: "INTEGER",
    operations: { read: true, write: true, event: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
  };
}

beforeEach(() => {
  prefs.locale = "en";
});

describe("detectDstGroups", () => {
  it("splits DST_START_* and DST_END_* parameters into their own groups", () => {
    const params = [
      param("DST_START_MONTH"),
      param("DST_START_DAY"),
      param("DST_END_MONTH"),
      param("OTHER_PARAM"),
    ];
    const groups = detectDstGroups(params);
    expect(groups.start.map((p) => p.name)).toEqual(["DST_START_MONTH", "DST_START_DAY"]);
    expect(groups.end.map((p) => p.name)).toEqual(["DST_END_MONTH"]);
    expect(groups.paired.has("OTHER_PARAM")).toBe(false);
    expect(groups.paired.has("DST_START_MONTH")).toBe(true);
    expect(groups.paired.has("DST_END_MONTH")).toBe(true);
  });

  it("returns empty groups when no DST parameters are present", () => {
    const groups = detectDstGroups([param("LEVEL")]);
    expect(groups.start).toEqual([]);
    expect(groups.end).toEqual([]);
    expect(groups.paired.size).toBe(0);
  });
});

describe("dstHeader", () => {
  it("resolves the start/end headers from the active prefs.locale", () => {
    prefs.locale = "de";
    expect(dstHeader("start")).toBe("Beginn der Sommerzeit");
    expect(dstHeader("end")).toBe("Ende der Sommerzeit");

    prefs.locale = "en";
    expect(dstHeader("start")).toBe("Start of daylight saving time");
    expect(dstHeader("end")).toBe("End of daylight saving time");
  });
});

describe("isDstTimeParam", () => {
  it("matches only DST_START_*_TIME / DST_END_*_TIME names", () => {
    expect(isDstTimeParam("DST_START_TIME")).toBe(true);
    expect(isDstTimeParam("DST_END_TIME")).toBe(true);
    expect(isDstTimeParam("DST_START_MONTH")).toBe(false);
    expect(isDstTimeParam("SOME_OTHER_TIME")).toBe(false);
  });
});

describe("minutesToHHMM / hhmmToMinutes", () => {
  it("round-trips a normal time of day", () => {
    expect(minutesToHHMM(90)).toBe("01:30");
    expect(hhmmToMinutes("01:30")).toBe(90);
  });

  it("pads single-digit hours and minutes", () => {
    expect(minutesToHHMM(5)).toBe("00:05");
  });

  it("clamps non-finite minute input to midnight", () => {
    expect(minutesToHHMM(Number.NaN)).toBe("00:00");
  });

  it("rejects an out-of-range or malformed HH:MM string", () => {
    expect(hhmmToMinutes("24:00")).toBeNull();
    expect(hhmmToMinutes("10:60")).toBeNull();
    expect(hhmmToMinutes("not-a-time")).toBeNull();
    expect(hhmmToMinutes("10")).toBeNull();
  });

  it("handles the last valid minute of the day", () => {
    expect(minutesToHHMM(23 * 60 + 59)).toBe("23:59");
    expect(hhmmToMinutes("23:59")).toBe(23 * 60 + 59);
  });
});
