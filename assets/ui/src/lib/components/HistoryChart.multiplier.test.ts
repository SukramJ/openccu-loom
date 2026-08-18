// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// Pins HistoryChart's multiplier projection: /history returns raw CCU wire
// values with no multiplier of its own (unlike the REST/WS data-point
// planes, which carry display_value), so the chart has to scale bucket
// min/avg/max itself using the multiplier the caller already resolved
// from the selected data point's summary.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";

const { mockGetHistory } = vi.hoisted(() => ({ mockGetHistory: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  getHistory: (...args: unknown[]) => mockGetHistory(...args),
  HistoryDisabledError: class HistoryDisabledError extends Error {},
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import HistoryChart from "./HistoryChart.svelte";

type Bucket = { ts: string; min: number; max: number; avg: number };

function bucket(v: number): Bucket {
  return { ts: "2026-07-22T10:00:00Z", min: v, max: v, avg: v };
}

const props = {
  central: "ccu1",
  interfaceId: "HmIP-RF",
  channel: "0001ABCD:4",
  parameter: "DIRT_LEVEL",
};

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("HistoryChart — multiplier projection", () => {
  it("scales a raw bucket value by the multiplier prop before plotting", async () => {
    // Raw wire reading 0.42 (LEVEL-shaped, multiplier 100) must land on the
    // chart as ~42, not ~0.42 — the same "0.42 %" bug the REST/WS
    // display_value projection fixes elsewhere.
    mockGetHistory.mockResolvedValue([bucket(0.42)]);
    const { container } = render(HistoryChart, {
      props: { ...props, multiplier: 100 },
    });
    // Wait for the rendered axis, not merely for the fetch: the chart
    // renders after the promise resolves, so asserting right after the
    // call sees only the title. With a single bucket the value padding
    // puts the top tick at scaled + 0.5.
    await waitFor(() => expect(container.textContent).toContain("42.5"));
    expect(container.textContent).not.toContain("0.9");
  });

  it("leaves bucket values unscaled when multiplier is absent (default 1)", async () => {
    mockGetHistory.mockResolvedValue([bucket(10)]);
    const { container } = render(HistoryChart, { props });
    await waitFor(() => expect(container.textContent).toContain("10.5"));
    expect(container.textContent).not.toContain("1050");
  });
});
