// @vitest-environment happy-dom
//
// The COLOR descriptor a CCU publishes is ordered by the RGB bit pattern
// (BLACK, BLUE, GREEN, TURQUOISE, RED, PURPLE, YELLOW, WHITE), which is not
// the order the daemon's colour enum uses. Sending the chip's position in
// that list therefore lit the wrong colour on four of the eight chips, so the
// tile has to send the label.
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
  t: (key: string) => key,
}));

import LightTile from "./LightTile.svelte";

const ADDRESS = "0001BSL0";

// Verbatim descriptor order of a real HmIP-BSL COLOR parameter.
const CCU_COLOR_ORDER = [
  "BLACK",
  "BLUE",
  "GREEN",
  "TURQUOISE",
  "RED",
  "PURPLE",
  "YELLOW",
  "WHITE",
];

function fixedColorCdp(): CustomDPSummary {
  return {
    name: "light",
    kind: "light_fixed_color",
    channel_no: 8,
    capabilities: { color: true },
  } as unknown as CustomDPSummary;
}

function dataPoints(): DataPointSummary[] {
  return [
    {
      parameter: "LEVEL",
      type: "FLOAT",
      value: 1,
      observed: true,
      operations: { read: true, write: true, event: true },
    },
    {
      parameter: "COLOR",
      type: "ENUM",
      value: "BLACK",
      observed: true,
      value_list: CCU_COLOR_ORDER,
      operations: { read: true, write: true, event: true },
    },
  ] as unknown as DataPointSummary[];
}

beforeEach(() => {
  vi.clearAllMocks();
  mockInvoke.mockResolvedValue(undefined);
  mockListDataPoints.mockResolvedValue(dataPoints());
});

afterEach(cleanup);

describe("LightTile — fixed-colour palette", () => {
  it.each(CCU_COLOR_ORDER)("sends %s by name, not by its descriptor index", async (color) => {
    render(LightTile, { props: { address: ADDRESS, cdp: fixedColorCdp() } });
    const chip = await screen.findByLabelText(color);

    await fireEvent.click(chip);

    await waitFor(() => expect(mockInvoke).toHaveBeenCalledTimes(1));
    expect(mockInvoke).toHaveBeenCalledWith(ADDRESS, "light", "set_color", {
      label: color,
    });
  });

  it("names the active colour when the wire value is the descriptor index", async () => {
    const dps = dataPoints();
    // A writable ENUM is published as its value_list index — RED sits at 4.
    (dps[1] as { value: unknown }).value = 4;
    mockListDataPoints.mockResolvedValue(dps);

    render(LightTile, { props: { address: ADDRESS, cdp: fixedColorCdp() } });

    await waitFor(() => expect(screen.getByText(/· Red$/)).toBeTruthy());
  });

  it("omits the colour name while the light is set to BLACK", async () => {
    const dps = dataPoints();
    (dps[1] as { value: unknown }).value = 0;
    mockListDataPoints.mockResolvedValue(dps);

    render(LightTile, { props: { address: ADDRESS, cdp: fixedColorCdp() } });

    await waitFor(() => expect(screen.getByText("100 %")).toBeTruthy());
  });
});
