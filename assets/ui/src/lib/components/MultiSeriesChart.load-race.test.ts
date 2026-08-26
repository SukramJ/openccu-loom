// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Pins the load-generation guard in MultiSeriesChart.svelte: an earlier
// batch that resolves after a newer one must not overwrite the results,
// and the series definitions the responses are zipped against must be the
// ones the batch was started for — otherwise the legend names one series
// while the line belongs to another.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";
import type { DiagramSeries } from "$lib/api/types";

const { mockGetHistory } = vi.hoisted(() => ({ mockGetHistory: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  getHistory: (...args: unknown[]) => mockGetHistory(...args),
  HistoryDisabledError: class HistoryDisabledError extends Error {},
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import MultiSeriesChart from "./MultiSeriesChart.svelte";

type Bucket = { ts: string; min: number; max: number; avg: number };

function bucket(v: number): Bucket {
  return { ts: "2026-07-22T10:00:00Z", min: v, max: v, avg: v };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

const livingRoom: DiagramSeries = {
  central: "ccu1",
  interface_id: "HmIP-RF",
  channel_address: "0001ABCD:1",
  parameter: "ACTUAL_TEMPERATURE",
  label: "Wohnzimmer",
};
const kitchen: DiagramSeries = {
  central: "ccu1",
  interface_id: "HmIP-RF",
  channel_address: "0002BEEF:1",
  parameter: "ACTUAL_TEMPERATURE",
  label: "Küche",
};

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("MultiSeriesChart — overlapping batches", () => {
  it("discards a batch superseded by a newer series list", async () => {
    const first = deferred<Bucket[]>();
    const second = deferred<Bucket[]>();
    mockGetHistory
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { container, rerender } = render(MultiSeriesChart, {
      props: { series: [livingRoom] },
    });
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(1));

    // The parent swaps the series while the first batch is still in flight.
    await rerender({ series: [kitchen] });
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(2));

    second.resolve([bucket(21)]);
    await waitFor(() =>
      expect(container.querySelector("polyline")).not.toBeNull(),
    );
    expect(container.textContent).toContain("Küche");

    // The abandoned first batch lands late — with no samples, which is what
    // makes it observable: adopting it would blank the chart and mark the
    // kitchen series as having no data, pairing one device's answer with
    // another device's definition.
    first.resolve([]);
    await new Promise((r) => setTimeout(r, 0));

    expect(container.querySelector("polyline")).not.toBeNull();
    expect(container.textContent).not.toContain("diagrams.chart.no_samples");
    expect(container.textContent).not.toContain("diagrams.chart.empty");
    expect(container.textContent).not.toContain("Wohnzimmer");
  });

  it("renders the legend of the series it was asked for", async () => {
    mockGetHistory.mockResolvedValue([bucket(21)]);
    const { container } = render(MultiSeriesChart, {
      props: { series: [livingRoom, kitchen] },
    });

    await waitFor(() => expect(container.textContent).toContain("Wohnzimmer"));
    expect(container.textContent).toContain("Küche");
    expect(mockGetHistory).toHaveBeenCalledTimes(2);
  });
});
