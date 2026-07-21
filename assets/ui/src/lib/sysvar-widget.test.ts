import { describe, it, expect } from "vitest";
import {
  sysvarWidget,
  sysvarNumberStep,
  isBoolSysvar,
  type SysvarWidgetInput,
} from "./sysvar-widget";

describe("sysvarWidget", () => {
  it("renders a switch for every boolean-flavoured wire type", () => {
    // LOGIC and ALARM are the CCU's real boolean sysvar types; BOOL is
    // the create-dialog alias. All three flip with a switch.
    for (const value_type of ["BOOL", "LOGIC", "ALARM"]) {
      expect(sysvarWidget({ value_type })).toBe("switch");
    }
  });

  it("matches the boolean wire types case-insensitively", () => {
    expect(sysvarWidget({ value_type: "logic" })).toBe("switch");
    expect(sysvarWidget({ value_type: "Alarm" })).toBe("switch");
  });

  it("keeps a switch even when an ALARM variable ships a label list", () => {
    // ALARM sysvars often carry a two-entry label list; the switch must
    // still win over the dropdown so the operator can flip the flag.
    const sv: SysvarWidgetInput = {
      value_type: "ALARM",
      value_list: ["nicht ausgelöst", "ausgelöst"],
    };
    expect(sysvarWidget(sv)).toBe("switch");
  });

  it("renders a dropdown for a labelled LIST", () => {
    const sv: SysvarWidgetInput = {
      value_type: "LIST",
      value_list: ["Aus", "Aktivierung", "Vollschutz"],
    };
    expect(sysvarWidget(sv)).toBe("select");
  });

  it("renders a numeric input for INTEGER, FLOAT and NUMBER", () => {
    for (const value_type of ["INTEGER", "FLOAT", "NUMBER"]) {
      expect(sysvarWidget({ value_type })).toBe("number");
    }
  });

  it("renders a numeric input for a label-less LIST (numeric index write)", () => {
    // A LIST with no labels writes its numeric index — a number field,
    // not free text. An empty value_list counts as absent.
    expect(sysvarWidget({ value_type: "LIST" })).toBe("number");
    expect(sysvarWidget({ value_type: "LIST", value_list: [] })).toBe("number");
  });

  it("falls back to free text for STRING and unknown types", () => {
    expect(sysvarWidget({ value_type: "STRING" })).toBe("text");
    expect(sysvarWidget({ value_type: "WHATEVER" })).toBe("text");
    expect(sysvarWidget({})).toBe("text");
  });

  it("prefers the dropdown over a numeric input when a numeric type still ships a label list", () => {
    // The value_list check runs before the numeric-type check, so an
    // INTEGER/FLOAT/NUMBER sysvar that also carries labels renders the
    // same dropdown a plain LIST would — the numeric wire value is just
    // the selected option's index either way.
    for (const value_type of ["INTEGER", "FLOAT", "NUMBER"]) {
      expect(
        sysvarWidget({ value_type, value_list: ["Off", "On"] }),
      ).toBe("select");
    }
  });

  it("treats a null or empty value_type the same as an unknown type", () => {
    expect(sysvarWidget({ value_type: null })).toBe("text");
    expect(sysvarWidget({ value_type: "" })).toBe("text");
  });
});

describe("sysvarNumberStep", () => {
  it("allows fractional steps for FLOAT and NUMBER", () => {
    expect(sysvarNumberStep("FLOAT")).toBe("any");
    expect(sysvarNumberStep("NUMBER")).toBe("any");
    expect(sysvarNumberStep("float")).toBe("any");
  });

  it("restricts INTEGER to whole steps", () => {
    expect(sysvarNumberStep("INTEGER")).toBe("1");
    expect(sysvarNumberStep(undefined)).toBe("1");
  });

  it("defaults to whole steps for null or empty input", () => {
    expect(sysvarNumberStep(null)).toBe("1");
    expect(sysvarNumberStep("")).toBe("1");
  });
});

describe("isBoolSysvar", () => {
  it("is true only for BOOL / LOGIC / ALARM", () => {
    expect(isBoolSysvar("BOOL")).toBe(true);
    expect(isBoolSysvar("LOGIC")).toBe(true);
    expect(isBoolSysvar("ALARM")).toBe(true);
    expect(isBoolSysvar("LIST")).toBe(false);
    expect(isBoolSysvar("INTEGER")).toBe(false);
    expect(isBoolSysvar(null)).toBe(false);
  });

  it("is false for an empty or undefined value_type", () => {
    expect(isBoolSysvar("")).toBe(false);
    expect(isBoolSysvar(undefined)).toBe(false);
  });
});
