// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import { validateCrossRules, visibleParameters } from "./validate";
import type {
  UISchemaCrossValidation,
  UISchemaParameter,
  UISchemaVisibility,
} from "$lib/api/types";

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

function rule(
  overrides: Partial<UISchemaCrossValidation> &
    Pick<UISchemaCrossValidation, "param_a" | "param_b" | "rule">,
): UISchemaCrossValidation {
  return {
    id: "rule-1",
    applies_to_params: [overrides.param_a, overrides.param_b],
    ...overrides,
  };
}

describe("validateCrossRules", () => {
  it("returns no errors when there are no rules", () => {
    expect(validateCrossRules([], { A: 1, B: 2 })).toEqual({});
  });

  it.each([
    ["eq", 5, 5, true],
    ["eq", 5, 6, false],
    ["ne", 5, 6, true],
    ["ne", 5, 5, false],
    ["gt", 6, 5, true],
    ["gt", 5, 5, false],
    ["gte", 5, 5, true],
    ["gte", 4, 5, false],
    ["lt", 4, 5, true],
    ["lt", 5, 5, false],
    ["lte", 5, 5, true],
    ["lte", 6, 5, false],
  ] as const)("evaluates '%s' comparator (a=%s b=%s) as satisfied=%s", (op, a, b, satisfied) => {
    const rules = [rule({ param_a: "A", param_b: "B", rule: op })];
    const errors = validateCrossRules(rules, { A: a, B: b });
    if (satisfied) {
      expect(errors).toEqual({});
    } else {
      expect(errors.A).toBeDefined();
      expect(errors.B).toBeDefined();
    }
  });

  it("attaches the rule's error message to every listed parameter on violation", () => {
    const rules = [
      rule({
        id: "min-max",
        param_a: "MIN",
        param_b: "MAX",
        rule: "lte",
        applies_to_params: ["MIN", "MAX"],
        error: "MIN must not exceed MAX",
      }),
    ];
    const errors = validateCrossRules(rules, { MIN: 10, MAX: 5 });
    expect(errors).toEqual({
      MIN: "MIN must not exceed MAX",
      MAX: "MIN must not exceed MAX",
    });
  });

  it("falls back to the rule id when no error message is supplied", () => {
    const rules = [rule({ id: "no-message-rule", param_a: "A", param_b: "B", rule: "eq" })];
    const errors = validateCrossRules(rules, { A: 1, B: 2 });
    expect(errors.A).toBe("no-message-rule");
  });

  it("does not overwrite an already-recorded error for a parameter from an earlier rule", () => {
    const rules = [
      rule({
        id: "first",
        param_a: "A",
        param_b: "B",
        rule: "eq",
        applies_to_params: ["A"],
        error: "first error",
      }),
      rule({
        id: "second",
        param_a: "A",
        param_b: "C",
        rule: "eq",
        applies_to_params: ["A"],
        error: "second error",
      }),
    ];
    const errors = validateCrossRules(rules, { A: 1, B: 2, C: 3 });
    expect(errors.A).toBe("first error");
  });

  it("skips silently (treats as satisfied) when either side is non-numeric", () => {
    const rules = [rule({ param_a: "A", param_b: "B", rule: "gt" })];
    expect(validateCrossRules(rules, { A: "not-a-number", B: 1 })).toEqual({});
    expect(validateCrossRules(rules, { A: 1, B: undefined })).toEqual({});
  });

  it("treats an unknown comparator as always satisfied", () => {
    const rules = [rule({ param_a: "A", param_b: "B", rule: "matches" })];
    expect(validateCrossRules(rules, { A: 1, B: 2 })).toEqual({});
  });

  it("coerces boolean and numeric-string operands before comparing", () => {
    const rules = [rule({ param_a: "A", param_b: "B", rule: "eq" })];
    expect(validateCrossRules(rules, { A: true, B: "1" })).toEqual({});
    expect(validateCrossRules(rules, { A: false, B: "0" })).toEqual({});
  });
});

describe("visibleParameters", () => {
  const params = [param("MODE"), param("THRESHOLD"), param("ALWAYS_SHOWN")];

  it("returns every parameter unchanged when there is no visibility metadata", () => {
    expect(visibleParameters(params, undefined, {})).toEqual(params);
    expect(visibleParameters(params, [], {})).toEqual(params);
  });

  it("always shows parameters that are never referenced by a `show` clause", () => {
    const visibility: UISchemaVisibility[] = [
      { show: ["THRESHOLD"], trigger: "MODE", trigger_value: "manual" },
    ];
    const visible = visibleParameters(params, visibility, { MODE: "auto" });
    expect(visible.map((p) => p.name)).toEqual(["MODE", "ALWAYS_SHOWN"]);
  });

  it("shows a gated parameter once its trigger matches", () => {
    const visibility: UISchemaVisibility[] = [
      { show: ["THRESHOLD"], trigger: "MODE", trigger_value: "manual" },
    ];
    const visible = visibleParameters(params, visibility, { MODE: "manual" });
    expect(visible.map((p) => p.name)).toEqual(["MODE", "THRESHOLD", "ALWAYS_SHOWN"]);
  });

  it("matches when the trigger value is one of several accepted values", () => {
    const visibility: UISchemaVisibility[] = [
      { show: ["THRESHOLD"], trigger: "MODE", trigger_value: ["manual", "eco"] },
    ];
    expect(
      visibleParameters(params, visibility, { MODE: "eco" }).map((p) => p.name),
    ).toContain("THRESHOLD");
    expect(
      visibleParameters(params, visibility, { MODE: "auto" }).map((p) => p.name),
    ).not.toContain("THRESHOLD");
  });

  it("is visible when any one of several visibility rules for the same target matches", () => {
    const visibility: UISchemaVisibility[] = [
      { show: ["THRESHOLD"], trigger: "MODE", trigger_value: "manual" },
      { show: ["THRESHOLD"], trigger: "OVERRIDE", trigger_value: true },
    ];
    const visible = visibleParameters(params, visibility, {
      MODE: "auto",
      OVERRIDE: true,
    });
    expect(visible.map((p) => p.name)).toContain("THRESHOLD");
  });

  it("compares the trigger value loosely across numeric-string / number / boolean types", () => {
    const visibility: UISchemaVisibility[] = [
      { show: ["THRESHOLD"], trigger: "MODE", trigger_value: 1 },
    ];
    expect(
      visibleParameters(params, visibility, { MODE: "1" }).map((p) => p.name),
    ).toContain("THRESHOLD");
    expect(
      visibleParameters(params, visibility, { MODE: true }).map((p) => p.name),
    ).toContain("THRESHOLD");
  });
});
