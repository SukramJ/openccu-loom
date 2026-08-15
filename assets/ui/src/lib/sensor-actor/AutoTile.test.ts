// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const listDataPoints = vi.fn();
const setValue = vi.fn().mockResolvedValue(undefined);

vi.mock("$lib/api/client", () => ({
  api: {
    listDataPoints: (...args: unknown[]) => listDataPoints(...args),
    setValue: (...args: unknown[]) => setValue(...args),
  },
  friendlyError: (err: unknown) => String(err),
  // Pulled in transitively by the pin store via auth.svelte.
  setUnauthorizedHandler: () => {},
}));

// No live event stream in a unit test; both subscriptions hand back an
// unsubscribe so the tile's onMount cleanup stays valid.
vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: () => () => {},
  onResync: () => () => {},
}));

import AutoTile from "./AutoTile.svelte";
import type { ChannelSummary, DataPointSummary } from "$lib/api/types";

const channel: ChannelSummary = {
  address: "ABC123:3",
  number: 3,
  type: "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER",
  paramset_key: "VALUES",
  data_points_count: 2,
};

/**
 * A read-only DP so the primary slot is taken and the writable one lands
 * in the control bucket — the same order a real channel arrives in.
 */
const activity: DataPointSummary = {
  unique_id: "ABC123:3.ACTIVITY_STATE",
  parameter: "ACTIVITY_STATE",
  observed: true,
  value: 0,
  type: "ENUM",
  value_list: ["UNKNOWN", "UP", "DOWN", "STABLE"],
  operations: { read: true, write: false, event: true },
};

function level(over: Partial<DataPointSummary> = {}): DataPointSummary {
  return {
    unique_id: "ABC123:3.LEVEL",
    parameter: "LEVEL",
    observed: true,
    value: 0,
    type: "FLOAT",
    min: 0,
    max: 1.01,
    operations: { read: true, write: true, event: false },
    ...over,
  };
}

afterEach(() => {
  vi.clearAllMocks();
  cleanup();
});

describe("AutoTile — slider granularity", () => {
  it("writes a fractional level for a FLOAT data point", async () => {
    listDataPoints.mockResolvedValue([activity, level()]);
    const { findByRole } = render(AutoTile, {
      props: { address: "ABC123", channel },
    });

    const slider = await findByRole("slider", { name: "LEVEL" });
    await fireEvent.keyDown(slider, { key: "ArrowRight" });

    await waitFor(() => expect(setValue).toHaveBeenCalled());
    expect(setValue).toHaveBeenCalledWith("ABC123", 3, "LEVEL", 0.01);
  });

  it("keeps whole steps for an INTEGER data point", async () => {
    listDataPoints.mockResolvedValue([
      activity,
      level({ parameter: "SETPOINT", type: "INTEGER", min: 0, max: 10 }),
    ]);
    const { findByRole } = render(AutoTile, {
      props: { address: "ABC123", channel },
    });

    const slider = await findByRole("slider", { name: "SETPOINT" });
    await fireEvent.keyDown(slider, { key: "ArrowRight" });

    await waitFor(() => expect(setValue).toHaveBeenCalled());
    expect(setValue).toHaveBeenCalledWith("ABC123", 3, "SETPOINT", 1);
  });
});
