// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// DOOR_STATE is a read-only ENUM, so the daemon publishes the value_list
// index. A tile that compares that value against the "OPEN" token reports
// an unknown state for a door whose position the daemon knows exactly.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
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

import CoverTile from "./CoverTile.svelte";

const ADDRESS = "0001GARAGE";

const DOOR_STATES = ["CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"];

function doorState(index: number): DataPointSummary {
  return {
    parameter: "DOOR_STATE",
    type: "ENUM",
    value: index,
    value_list: DOOR_STATES,
    observed: true,
    operations: { read: true, write: false, event: true },
  } as unknown as DataPointSummary;
}

const garageCdp = {
  name: "garage",
  kind: "cover_garage",
  channel_no: 1,
  capabilities: { vent: true, stop: true },
} as unknown as CustomDPSummary;

beforeEach(() => {
  vi.clearAllMocks();
  mockInvoke.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("CoverTile — garage door state", () => {
  it("resolves the DOOR_STATE index to its state caption", async () => {
    mockListDataPoints.mockResolvedValue([doorState(1)]);
    render(CoverTile, { props: { address: ADDRESS, cdp: garageCdp } });

    await waitFor(() =>
      expect(screen.getByText("cdp.cover.state_open")).toBeTruthy(),
    );
    expect(screen.queryByText("cdp.cover.state_unknown")).toBeNull();
  });

  it("marks the matching command button active for the reported state", async () => {
    mockListDataPoints.mockResolvedValue([doorState(2)]);
    render(CoverTile, { props: { address: ADDRESS, cdp: garageCdp } });

    const vent = await screen.findByLabelText("cdp.cover.ventilate");
    await waitFor(() =>
      expect(vent.getAttribute("aria-pressed")).toBe("true"),
    );
  });
});
