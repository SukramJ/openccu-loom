// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Pins the load-generation guard in HistoryChart.svelte: a response for a
// range or data point the chart has already moved on from must never
// overwrite the one it now shows. Both requests succeed, so nothing else
// in the UI would reveal that the older answer won.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

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

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const props = {
  central: "ccu1",
  interfaceId: "HmIP-RF",
  channel: "0001ABCD:1",
  parameter: "ACTUAL_TEMPERATURE",
};

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("HistoryChart — overlapping range requests", () => {
  it("issues exactly one request on mount", async () => {
    mockGetHistory.mockResolvedValue([bucket(10)]);
    render(HistoryChart, { props });
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(1));
    // Give a second (duplicate) fetch a chance to be issued before asserting.
    await new Promise((r) => setTimeout(r, 0));
    expect(mockGetHistory).toHaveBeenCalledTimes(1);
  });

  it("discards the response of a superseded range", async () => {
    const first = deferred<Bucket[]>();
    const second = deferred<Bucket[]>();
    mockGetHistory
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { container, getByText } = render(HistoryChart, { props });
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(1));

    // Pick another range while the first request is still in flight.
    await fireEvent.click(getByText("1 h"));
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(2));

    // The newer request answers first: value 99 → y-axis ticks around 99.
    second.resolve([bucket(99)]);
    await waitFor(() => expect(container.textContent).toContain("99.5"));

    // The abandoned first request lands late and must be dropped.
    first.resolve([bucket(10)]);
    await new Promise((r) => setTimeout(r, 0));

    expect(container.textContent).toContain("99.5");
    expect(container.textContent).not.toContain("10.5");
  });

  it("discards a late failure of a superseded range", async () => {
    const first = deferred<Bucket[]>();
    const second = deferred<Bucket[]>();
    mockGetHistory
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const { container, getByText } = render(HistoryChart, { props });
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(1));
    await fireEvent.click(getByText("1 h"));
    await waitFor(() => expect(mockGetHistory).toHaveBeenCalledTimes(2));

    second.resolve([bucket(99)]);
    await waitFor(() => expect(container.textContent).toContain("99.5"));

    first.reject(new Error("history backend unreachable"));
    await new Promise((r) => setTimeout(r, 0));

    expect(container.textContent).not.toContain("history backend unreachable");
    expect(container.textContent).toContain("99.5");
  });
});
