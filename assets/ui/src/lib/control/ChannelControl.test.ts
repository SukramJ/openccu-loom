// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// The daemon rejects writes routinely — value out of range, device
// unreachable, CCU 502. The control the operator just used has to survive
// that: `error` drives the template branch that replaces the widget, so a
// write failure belongs in a toast.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { DataPointSummary } from "$lib/api/types";

const { mockListDataPoints, mockSetValue, mockToastError } = vi.hoisted(() => ({
  mockListDataPoints: vi.fn(),
  mockSetValue: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
    setValue: (...args: unknown[]) => mockSetValue(...args),
  },
  friendlyError: (err: unknown) =>
    err instanceof Error ? err.message : String(err),
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: vi.fn(),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

import ChannelControl from "./ChannelControl.svelte";

const ADDRESS = "0001SWITCH";

const stateDP = {
  parameter: "STATE",
  type: "BOOL",
  value: false,
  observed: true,
  control: "SWITCH.STATE",
  operations: { read: true, write: true, event: true },
  unique_id: "dp-STATE",
} as unknown as DataPointSummary;

beforeEach(() => {
  vi.clearAllMocks();
  mockListDataPoints.mockResolvedValue([stateDP]);
});

afterEach(() => cleanup());

describe("ChannelControl — rejected write", () => {
  it("keeps the widget mounted and reports the failure as a toast", async () => {
    mockSetValue.mockRejectedValue(new Error("value below MIN"));
    render(ChannelControl, {
      props: { address: ADDRESS, channel: 1, title: "Bookshelf" },
    });

    const on = await screen.findByLabelText("Bookshelf — quick.on");
    await fireEvent.click(on);

    await waitFor(() => expect(mockToastError).toHaveBeenCalledTimes(1));
    expect(mockToastError.mock.calls[0][1]).toBe("value below MIN");
    // The control is still there to retry with.
    expect(screen.getByLabelText("Bookshelf — quick.on")).toBeTruthy();
  });

  it("still applies the optimistic value when the write is accepted", async () => {
    mockSetValue.mockResolvedValue(undefined);
    render(ChannelControl, {
      props: { address: ADDRESS, channel: 1, title: "Bookshelf" },
    });

    await fireEvent.click(await screen.findByLabelText("Bookshelf — quick.on"));

    await waitFor(() =>
      expect(mockSetValue).toHaveBeenCalledWith(ADDRESS, 1, "STATE", true),
    );
    expect(mockToastError).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(
        screen
          .getByLabelText("Bookshelf — quick.on")
          .getAttribute("aria-pressed"),
      ).toBe("true"),
    );
  });
});

describe("ChannelControl — listDataPoints failure", () => {
  it("renders the shared ErrorState with a retry that reloads the channel", async () => {
    mockListDataPoints.mockRejectedValueOnce(new Error("upstream 502"));
    render(ChannelControl, {
      props: { address: ADDRESS, channel: 1, title: "Bookshelf" },
    });

    await waitFor(() => expect(screen.getByText(/upstream 502/)).toBeInTheDocument());
    // The shared ErrorState renders an alert icon + a localized retry
    // button — not a bare styled <div> with the raw error string.
    expect(screen.getByText(/common\.error/)).toBeInTheDocument();
    const retry = screen.getByText("common.reload");
    expect(retry).toBeInTheDocument();

    mockListDataPoints.mockResolvedValueOnce([stateDP]);
    await fireEvent.click(retry);

    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalledTimes(2));
    await screen.findByLabelText("Bookshelf — quick.on");
  });
});
