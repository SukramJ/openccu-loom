import { describe, it, expect } from "vitest";
import type { DataPointSummary } from "$lib/api/types";
import { resolveChannel, slot } from "./resolver";
import { parseControl } from "./families";

function dp(parameter: string, control?: string): DataPointSummary {
  return {
    parameter,
    value: 0,
    observed: true,
    operations: { read: true, write: true, event: true },
    control,
  };
}

describe("parseControl", () => {
  it("returns null for undefined / empty input", () => {
    expect(parseControl(undefined)).toBeNull();
    expect(parseControl("")).toBeNull();
  });

  it("returns null when no dot is present", () => {
    expect(parseControl("DIMMER")).toBeNull();
  });

  it("returns null when the slot half is empty", () => {
    expect(parseControl("DIMMER.")).toBeNull();
  });

  it("returns null when the family half is empty", () => {
    expect(parseControl(".LEVEL")).toBeNull();
  });

  it("splits a well-formed CONTROL attribute", () => {
    expect(parseControl("DIMMER.LEVEL")).toEqual({
      family: "DIMMER",
      slot: "LEVEL",
    });
  });

  it("keeps multi-segment slot suffixes intact (first dot wins)", () => {
    expect(parseControl("HEATING_CONTROL_HMIP.SETPOINT")).toEqual({
      family: "HEATING_CONTROL_HMIP",
      slot: "SETPOINT",
    });
  });
});

describe("resolveChannel", () => {
  it("returns null for an empty data-point list", () => {
    expect(resolveChannel([])).toBeNull();
  });

  it("returns null when no data-point carries CONTROL", () => {
    expect(
      resolveChannel([dp("STATE"), dp("LEVEL_REAL")]),
    ).toBeNull();
  });

  it("ignores malformed CONTROL strings", () => {
    expect(
      resolveChannel([dp("STATE", "BROKEN"), dp("LEVEL", "ALSO.")]),
    ).toBeNull();
  });

  it("resolves a single CONTROL-tagged data-point", () => {
    const resolved = resolveChannel([dp("STATE", "SWITCH.STATE")]);
    expect(resolved?.family).toBe("SWITCH");
    expect(Object.keys(resolved?.slots ?? {})).toEqual(["STATE"]);
  });

  it("groups multiple slots under the same family", () => {
    const resolved = resolveChannel([
      dp("LEVEL", "DIMMER.LEVEL"),
      dp("LEVEL_REAL", "DIMMER.LEVEL_REAL"),
      dp("OLD_LEVEL", "DIMMER.OLD_LEVEL"),
    ]);
    expect(resolved?.family).toBe("DIMMER");
    expect(Object.keys(resolved?.slots ?? {}).sort()).toEqual([
      "LEVEL",
      "LEVEL_REAL",
      "OLD_LEVEL",
    ]);
  });

  it("picks the family with the most CONTROL-tagged slots", () => {
    const resolved = resolveChannel([
      dp("LEVEL", "DIMMER.LEVEL"),
      dp("LEVEL_REAL", "DIMMER.LEVEL_REAL"),
      dp("STATE", "SWITCH.STATE"),
    ]);
    expect(resolved?.family).toBe("DIMMER");
  });

  it("breaks ties alphabetically when families have equal slot count", () => {
    const resolved = resolveChannel([
      dp("STATE", "SWITCH.STATE"),
      dp("OPEN", "LOCK.OPEN"),
    ]);
    expect(resolved?.family).toBe("LOCK");
  });

  it("keeps first occurrence on duplicate slot suffix within a family", () => {
    const first = dp("LEVEL", "DIMMER.LEVEL");
    const second = dp("LEVEL_LEGACY", "DIMMER.LEVEL");
    const resolved = resolveChannel([first, second]);
    expect(resolved?.slots.LEVEL).toBe(first);
  });

  it("treats each family's slot set as disjoint", () => {
    const resolved = resolveChannel([
      dp("STATE", "SWITCH.STATE"),
      dp("POWER", "POWERMETER.POWER"),
      dp("ENERGY_COUNTER", "POWERMETER.ENERGY_COUNTER"),
    ]);
    expect(resolved?.family).toBe("POWERMETER");
    expect(resolved?.slots.STATE).toBeUndefined();
    expect(resolved?.slots.POWER).toBeDefined();
  });

  it("records non-dominant families as siblings", () => {
    const resolved = resolveChannel([
      dp("STATE", "SWITCH.STATE"),
      dp("POWER", "POWERMETER.POWER"),
      dp("ENERGY_COUNTER", "POWERMETER.ENERGY_COUNTER"),
    ]);
    expect(resolved?.siblings.SWITCH?.STATE).toBeDefined();
    expect(resolved?.siblings.POWERMETER).toBeUndefined();
  });

  it("captures the multi-family HM-CC-TC pair as primary + sibling", () => {
    // SWITCH and TEMP each contribute one slot — the resolver picks
    // SWITCH alphabetically, the setpoint lands in siblings.
    const resolved = resolveChannel([
      dp("STATE", "SWITCH.STATE"),
      dp("SETPOINT", "TEMP.SETPOINT"),
    ]);
    expect(resolved?.family).toBe("SWITCH");
    expect(resolved?.slots.STATE).toBeDefined();
    expect(resolved?.siblings.TEMP?.SETPOINT).toBeDefined();
  });

  it("siblings is empty when only one family is observed", () => {
    const resolved = resolveChannel([dp("STATE", "SWITCH.STATE")]);
    expect(Object.keys(resolved?.siblings ?? {})).toEqual([]);
  });
});

describe("slot helper", () => {
  it("returns the data-point at a known suffix", () => {
    const probe = dp("SETPOINT", "HEATING_CONTROL_HMIP.SETPOINT");
    const resolved = resolveChannel([probe])!;
    expect(slot(resolved, "SETPOINT")).toBe(probe);
  });

  it("returns undefined for unknown suffix", () => {
    const resolved = resolveChannel([dp("STATE", "SWITCH.STATE")])!;
    expect(slot(resolved, "MISSING")).toBeUndefined();
  });
});
