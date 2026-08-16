// @vitest-environment happy-dom
//
// The RF thermostat's CONTROL_MODE is a read-only ENUM, so the daemon
// publishes the value_list index. A widget that compares that value to
// the "AUTO-MODE" token never names the mode and never highlights the
// active mode button.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, screen } from "@testing-library/svelte";
import type { DataPointSummary } from "$lib/api/types";
import type { ResolvedChannel } from "../resolver";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import Climate from "./Climate.svelte";

const TITLE = "Wohnzimmer";
const RF_MODES = ["AUTO-MODE", "MANU-MODE", "PARTY-MODE", "BOOST-MODE"];

function dp(parameter: string, overrides: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    parameter,
    value: null,
    observed: true,
    operations: { read: true, write: false, event: true },
    unique_id: `dp-${parameter}`,
    usage: "data_point",
    ...overrides,
  } as DataPointSummary;
}

function rfChannel(modeIndex: number): ResolvedChannel {
  return {
    family: "HEATING_CONTROL",
    slots: {
      SETPOINT: dp("SET_TEMPERATURE", {
        value: 21,
        operations: { read: true, write: true, event: true },
      }),
      CONTROL_MODE: dp("CONTROL_MODE", {
        type: "ENUM",
        value: modeIndex,
        value_list: RF_MODES,
      }),
      AUTO: dp("AUTO_MODE", { operations: { read: false, write: true, event: false } }),
      BOOST: dp("BOOST_MODE", { operations: { read: false, write: true, event: false } }),
    },
    siblings: {},
  };
}

afterEach(() => cleanup());

describe("Climate — RF CONTROL_MODE index", () => {
  it("names the current mode in the secondary line", () => {
    render(Climate, {
      props: { resolved: rfChannel(3), title: TITLE, onSetSlot: vi.fn() },
    });

    // Secondary line: "<mode> · <setpoint>" — the mode caption is only
    // present once the index resolves back to its value_list token.
    expect(screen.getByText(/^climate\.mode\.boost · /)).toBeTruthy();
  });

  it("marks the mode button matching the reported index active", () => {
    render(Climate, {
      props: { resolved: rfChannel(0), title: TITLE, onSetSlot: vi.fn() },
    });

    const auto = screen.getByLabelText("climate.mode.auto");
    const boost = screen.getByLabelText("climate.mode.boost");
    expect(auto.getAttribute("aria-pressed")).toBe("true");
    expect(boost.getAttribute("aria-pressed")).toBe("false");
  });
});
