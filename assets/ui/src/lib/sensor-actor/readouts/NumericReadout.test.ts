// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
import { describe, it, expect, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import type { DataPointSummary } from "$lib/api/types";

import NumericReadout from "./NumericReadout.svelte";

afterEach(() => cleanup());

// The component formats fractional numbers via `toLocaleString(undefined, …)`,
// which follows the machine's locale (e.g. "0,42" under a German locale vs.
// "0.42" under en-US) — that locale sensitivity is deliberate production
// behaviour (browsers render numbers in the user's own locale), so the test
// derives its expectation the same way instead of hard-coding a separator.
function localized(v: number, maximumFractionDigits: number): string {
  return v.toLocaleString(undefined, { minimumFractionDigits: 0, maximumFractionDigits });
}

function levelDP(overrides: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    unique_id: "ABC123:4.LEVEL",
    parameter: "LEVEL",
    type: "FLOAT",
    unit: "%",
    value: 0.42,
    observed: true,
    operations: { read: true, write: false, event: true },
    ...overrides,
  };
}

describe("NumericReadout — display_value projection", () => {
  it("renders display_value when the summary carries one", () => {
    render(NumericReadout, {
      props: { dp: levelDP({ display_value: 42 }), showAge: false },
    });
    expect(screen.getByText("42 %")).toBeInTheDocument();
  });

  it("falls back to the raw value when display_value is absent", () => {
    render(NumericReadout, {
      props: { dp: levelDP(), showAge: false },
    });
    expect(screen.getByText(`${localized(0.42, 2)} %`)).toBeInTheDocument();
  });

  it("does not force-round an INTEGER-declared parameter's fractional display_value", () => {
    // TIME_OF_OPERATION: raw seconds (INTEGER), multiplier 1/86400,
    // display_value in fractional days.
    render(NumericReadout, {
      props: {
        dp: levelDP({
          parameter: "TIME_OF_OPERATION",
          type: "INTEGER",
          unit: "d",
          value: 129600,
          display_value: 1.5,
        }),
        showAge: false,
      },
    });
    expect(screen.getByText(`${localized(1.5, 2)} d`)).toBeInTheDocument();
  });
});
