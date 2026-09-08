import { describe, it, expect, afterEach } from "vitest";
import { buildPreview, previewBody, readBackDiff } from "./write-preview";
import { prefs } from "$lib/stores/preferences.svelte";

function param(over: Record<string, unknown> = {}) {
  return {
    name: "P",
    type: "INTEGER",
    operations: { read: true, write: true, event: false, determine: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
    ...over,
  } as never;
}

function schema(params: unknown[]) {
  return { parameters: params } as never;
}

describe("buildPreview", () => {
  const originalLocale = prefs.locale;
  afterEach(() => {
    prefs.locale = originalLocale;
  });

  it("lists one entry per dirty parameter, in schema order", () => {
    const s = schema([
      param({ name: "A", label: "Alpha" }),
      param({ name: "B", label: "Beta" }),
      param({ name: "C", label: "Gamma" }),
    ]);
    const entries = buildPreview(s, { A: 2, B: 5, C: 9 }, { A: 1, B: 5, C: 8 }, ["C", "A"]);
    expect(entries.map((e) => e.name)).toEqual(["A", "C"]);
    expect(entries[0]).toMatchObject({ label: "Alpha", from: "1", to: "2", raw: 2 });
  });

  it("falls back to the raw parameter name when there is no label", () => {
    const entries = buildPreview(schema([param({ name: "RAW_NAME" })]), { RAW_NAME: 1 }, {}, [
      "RAW_NAME",
    ]);
    expect(entries[0].label).toBe("RAW_NAME");
  });

  // The operator only ever saw the projected value, so the preview must
  // report the change in the same terms — a raw 0.42 → 0.55 would be
  // unrecognisable next to a slider that reads 42 % → 55 %.
  it("reports projected values, not raw wire values", () => {
    const s = schema([
      param({ name: "LEVEL", type: "FLOAT", unit: "%", multiplier: 100 }),
    ]);
    const entries = buildPreview(s, { LEVEL: 0.55 }, { LEVEL: 0.42 }, ["LEVEL"]);
    expect(entries[0].from).toBe("42 %");
    expect(entries[0].to).toBe("55 %");
    // …while the body still carries the raw value the CCU expects.
    expect(previewBody(entries)).toEqual({ LEVEL: 0.55 });
  });

  it("reports an enum by its label and a bool by its word", () => {
    prefs.locale = "en";
    const s = schema([
      param({
        name: "MODE",
        type: "ENUM",
        value_list: [
          { value: 0, key: "OFF", label: "Off" },
          { value: 1, key: "ON", label: "On" },
        ],
      }),
      param({ name: "FLAG", type: "BOOL" }),
    ]);
    const entries = buildPreview(s, { MODE: 1, FLAG: true }, { MODE: 0, FLAG: false }, [
      "MODE",
      "FLAG",
    ]);
    expect(entries[0]).toMatchObject({ from: "Off", to: "On" });
    expect(entries[1].to).toBe("On");
  });

  it("renders an absent server value as an em dash rather than blank", () => {
    const entries = buildPreview(schema([param({ name: "A" })]), { A: 3 }, {}, ["A"]);
    expect(entries[0].from).toBe("—");
  });

  it("returns nothing without a schema", () => {
    expect(buildPreview(null, { A: 1 }, {}, ["A"])).toEqual([]);
  });
});

describe("readBackDiff", () => {
  it("reports a value the device clamped", () => {
    const s = schema([param({ name: "TEMP", type: "FLOAT", unit: "°C" })]);
    const diff = readBackDiff(s, { TEMP: 42 }, { TEMP: 30.5 });
    expect(diff).toEqual([{ name: "TEMP", label: "TEMP", sent: "42 °C", got: "30.5 °C" }]);
  });

  // A CCU echoing 1 where 1.0 was sent is not a divergence; reporting it
  // would train the operator to ignore the warning, which costs exactly the
  // case the warning exists for.
  it("does not report a numeric echo in a different spelling", () => {
    const s = schema([param({ name: "N", type: "FLOAT" })]);
    expect(readBackDiff(s, { N: 1.0 }, { N: "1" })).toEqual([]);
    expect(readBackDiff(s, { N: 1 }, { N: 1.0 })).toEqual([]);
  });

  it("ignores parameters the reload does not carry", () => {
    const s = schema([param({ name: "A" }), param({ name: "B" })]);
    expect(readBackDiff(s, { A: 1, B: 2 }, { A: 1 })).toEqual([]);
  });

  it("ignores parameters that were not sent", () => {
    const s = schema([param({ name: "A" })]);
    expect(readBackDiff(s, {}, { A: 99 })).toEqual([]);
  });

  it("compares non-numeric values structurally", () => {
    const s = schema([param({ name: "S", type: "STRING" })]);
    expect(readBackDiff(s, { S: "abc" }, { S: "abc" })).toEqual([]);
    expect(readBackDiff(s, { S: "abc" }, { S: "xyz" })).toHaveLength(1);
  });

  it("returns nothing without a schema", () => {
    expect(readBackDiff(null, { A: 1 }, { A: 2 })).toEqual([]);
  });
});
