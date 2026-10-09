import { describe, it, expect } from "vitest";
import type { UISchemaParameter } from "$lib/api/types";
import { keypressRows } from "./keypress-rows";

function p(name: string): UISchemaParameter {
  return {
    name,
    type: "INTEGER",
    operations: { read: true, write: true, event: false },
    flags: { visible: true, internal: false, service: false },
    observed: true,
  };
}

describe("keypressRows", () => {
  it("puts SHORT_x and LONG_x in one row and keeps the rest as common", () => {
    const out = keypressRows([
      p("UI_HINT"),
      p("SHORT_ON_LEVEL"),
      p("LONG_ON_LEVEL"),
      p("SHORT_MULTIEXECUTE"),
    ]);
    expect(out.common.map((x) => x.name)).toEqual(["UI_HINT"]);
    expect(out.rows.map((r) => [r.stem, r.short.map((x) => x.name), r.long.map((x) => x.name)])).toEqual([
      ["ON_LEVEL", ["SHORT_ON_LEVEL"], ["LONG_ON_LEVEL"]],
      ["MULTIEXECUTE", ["SHORT_MULTIEXECUTE"], []],
    ]);
  });

  it("keeps both halves of a time pair in the same cell", () => {
    const out = keypressRows([
      p("SHORT_ON_TIME_BASE"),
      p("SHORT_ON_TIME_FACTOR"),
      p("LONG_ON_TIME_BASE"),
      p("LONG_ON_TIME_FACTOR"),
      p("SHORT_ONDELAY_TIME_UNIT"),
      p("SHORT_ONDELAY_TIME_VALUE"),
    ]);
    expect(out.rows).toHaveLength(2);
    expect(out.rows[0].stem).toBe("ON_TIME");
    expect(out.rows[0].short.map((x) => x.name)).toEqual(["SHORT_ON_TIME_BASE", "SHORT_ON_TIME_FACTOR"]);
    expect(out.rows[0].long.map((x) => x.name)).toEqual(["LONG_ON_TIME_BASE", "LONG_ON_TIME_FACTOR"]);
    expect(out.rows[1].stem).toBe("ONDELAY_TIME");
  });

  it("orders rows by the first appearance of either side", () => {
    const out = keypressRows([p("LONG_B"), p("SHORT_A"), p("SHORT_B")]);
    expect(out.rows.map((r) => r.stem)).toEqual(["B", "A"]);
  });
});
