// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The HmIP mode row mirrors the SET_POINT_MODE readback (0 auto, 1 manual,
// 2 away), but away is not a mode the daemon can write — `set_mode` only
// knows auto/heat/cool/off. Pressing "Away" must therefore reach the away
// operation, never a manual-heat write on a real thermostat.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { CustomDPSummary, DataPointSummary } from "$lib/api/types";

const { mockListDataPoints, mockInvoke } = vi.hoisted(() => ({
  mockListDataPoints: vi.fn(),
  mockInvoke: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
    invokeCustomDataPoint: (...args: unknown[]) => mockInvoke(...args),
  },
  friendlyError: (err: unknown) =>
    err instanceof Error ? err.message : String(err),
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ClimateTile from "./ClimateTile.svelte";

const ADDRESS = "0001ABCD";

function dp(
  parameter: string,
  value: unknown,
  type = "FLOAT",
): DataPointSummary {
  return {
    parameter,
    type,
    value,
    observed: true,
    operations: { read: true, write: true, event: true },
  } as unknown as DataPointSummary;
}

function cdp(away: boolean, kind = "climate_hmip"): CustomDPSummary {
  return {
    name: "climate",
    kind,
    channel_no: 1,
    capabilities: { away },
  } as unknown as CustomDPSummary;
}

// CONTROL_MODE is a read-only ENUM, so the wire carries the value_list
// index — the label only exists in the descriptor.
const RF_MODES = ["AUTO-MODE", "MANU-MODE", "PARTY-MODE", "BOOST-MODE"];

function controlModeDP(index: number): DataPointSummary {
  return {
    ...dp("CONTROL_MODE", index, "ENUM"),
    value_list: RF_MODES,
    operations: { read: true, write: false, event: true },
  } as unknown as DataPointSummary;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockInvoke.mockResolvedValue(undefined);
  mockListDataPoints.mockResolvedValue([
    dp("SET_POINT_MODE", 1, "INTEGER"),
    dp("SET_POINT_TEMPERATURE", 21),
    dp("ACTUAL_TEMPERATURE", 20.5),
  ]);
});

afterEach(() => cleanup());

describe("ClimateTile — HmIP mode row", () => {
  it("sends the away operation, never set_mode, when the away segment is pressed", async () => {
    render(ClimateTile, { props: { address: ADDRESS, cdp: cdp(true) } });

    const away = await screen.findByText("cdp.climate.mode_away");
    await fireEvent.click(away);

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(1));
    expect(mockInvoke).toHaveBeenCalledWith(
      ADDRESS,
      "climate",
      "enable_away_by_duration",
      { hours: 24, away_temperature: 12 },
    );
    for (const call of mockInvoke.mock.calls) {
      expect(call[2]).not.toBe("set_mode");
    }
  });

  it("omits the away segment when the data point has no away capability", async () => {
    render(ClimateTile, { props: { address: ADDRESS, cdp: cdp(false) } });

    await screen.findByText("cdp.climate.mode_auto");
    expect(screen.queryByText("cdp.climate.mode_away")).toBeNull();
  });

  it("still maps auto and manual onto set_mode", async () => {
    render(ClimateTile, { props: { address: ADDRESS, cdp: cdp(true) } });

    await fireEvent.click(await screen.findByText("cdp.climate.mode_auto"));
    await waitFor(() =>
      expect(mockInvoke).toHaveBeenCalledWith(ADDRESS, "climate", "set_mode", {
        mode: "auto",
      }),
    );

    await fireEvent.click(screen.getByText("cdp.climate.mode_manual"));
    await waitFor(() =>
      expect(mockInvoke).toHaveBeenCalledWith(ADDRESS, "climate", "set_mode", {
        mode: "heat",
      }),
    );
  });

  it("clears the away window before switching to manual while away is active", async () => {
    mockListDataPoints.mockResolvedValue([
      dp("SET_POINT_MODE", 2, "INTEGER"),
      dp("SET_POINT_TEMPERATURE", 12),
    ]);
    render(ClimateTile, { props: { address: ADDRESS, cdp: cdp(true) } });

    await fireEvent.click(await screen.findByText("cdp.climate.mode_manual"));

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(2));
    expect(mockInvoke.mock.calls[0][2]).toBe("disable_away");
    expect(mockInvoke.mock.calls[1][2]).toBe("set_mode");
    expect(mockInvoke.mock.calls[1][3]).toEqual({ mode: "heat" });
  });
});

describe("ClimateTile — RF control mode", () => {
  it("labels the mode from the CONTROL_MODE value_list index", async () => {
    mockListDataPoints.mockResolvedValue([
      dp("SETPOINT", 21),
      dp("ACTUAL_TEMPERATURE", 20.5),
      controlModeDP(1),
    ]);
    render(ClimateTile, {
      props: { address: ADDRESS, cdp: cdp(true, "climate_rf") },
    });

    // The secondary line is the only place the mode word appears — the RF
    // button row carries the same caption, so match the joined readout.
    await waitFor(() =>
      expect(
        screen.getByText("cdp.climate.mode_manual · 20.5 °C → 21.0 °C"),
      ).toBeTruthy(),
    );
  });

  // While the device is in PARTY-MODE the tile has to offer the way out.
  // Reading CONTROL_MODE as a label left `isAway` permanently false, so the
  // only button on the absence row re-armed the window the operator wanted
  // to end.
  it("detects PARTY-MODE as away and offers the disable_away action", async () => {
    mockListDataPoints.mockResolvedValue([dp("SETPOINT", 12), controlModeDP(2)]);
    render(ClimateTile, {
      props: { address: ADDRESS, cdp: cdp(true, "climate_rf") },
    });

    await fireEvent.click(await screen.findByText("cdp.climate.present"));
    await waitFor(() =>
      expect(mockInvoke).toHaveBeenCalledWith(
        ADDRESS,
        "climate",
        "disable_away",
        {},
      ),
    );
    expect(screen.queryByText("cdp.climate.away_24h")).toBeNull();
  });
});

describe("ClimateTile — setpoint bounds", () => {
  it("clamps the stepper to the descriptor range instead of 4.5 °C", async () => {
    mockListDataPoints.mockResolvedValue([
      { ...dp("SETPOINT", 6), min: 6, max: 30 } as unknown as DataPointSummary,
    ]);
    render(ClimateTile, {
      props: { address: ADDRESS, cdp: cdp(false, "climate_rf") },
    });

    await fireEvent.click(
      await screen.findByLabelText("control.number.decrement"),
    );

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(1));
    expect(mockInvoke).toHaveBeenCalledWith(
      ADDRESS,
      "climate",
      "set_temperature",
      { temperature: 6 },
    );
  });
});
