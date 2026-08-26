// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// The reload effect reads address/channel/paramset/peer AND locale/
// expertMode — all six are legitimate reasons to refetch the ui-schema.
// But locale and expertMode changing on their own (same channel, same
// paramset) must not silently discard an unsaved working copy: unlike
// the CONFIG_PENDING-settle and resync reload paths in this file, which
// both skip while `dirtyNames.length > 0`, this path used to reload
// unconditionally and wipe the edit + undo stack with no confirm.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import type { UISchema } from "$lib/api/types";

const { mockUiSchema, mockListDataPoints, mockOpenEditSession } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockListDataPoints: vi.fn(),
  mockOpenEditSession: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
    openEditSession: (...args: unknown[]) => mockOpenEditSession(...args),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    getParamset: vi.fn().mockResolvedValue({}),
    putParamset: vi.fn(),
    putLinkParamset: vi.fn(),
    setValue: vi.fn(),
    takeOverEditSession: vi.fn(),
    determineParameter: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import ChannelPanel from "./ChannelPanel.svelte";

function masterSchema(address: string, number: number): UISchema {
  return {
    channel: {
      address: `${address}:${number}`,
      number,
      type: "KEYMATIC",
      device_address: address,
    },
    parameters: [
      {
        name: "SHORT_ON_TIME",
        label: "Short on time",
        type: "FLOAT",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 50,
      },
    ],
  };
}

async function flush() {
  await tick();
  await Promise.resolve();
  await tick();
}

function numberInput(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector('input[type="number"]');
  if (!el) throw new Error("expected a number input");
  return el as HTMLInputElement;
}

function expertCheckbox(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector('input[type="checkbox"]');
  if (!el) throw new Error("expected the expert-mode checkbox");
  return el as HTMLInputElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  // expertMode is seeded from localStorage ("openccu-loom.expert_mode"),
  // and the checkbox test below flips it for real via setExpert() — clear
  // it so that write does not leak into a later test's initial state.
  localStorage.clear();
  mockUiSchema.mockImplementation((address: string, channel: number) =>
    Promise.resolve(masterSchema(address, channel)),
  );
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
  mockListDataPoints.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("ChannelPanel — locale/expert-mode reload guard", () => {
  it("does not discard an unsaved edit when the UI language changes", async () => {
    const { container, rerender } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER" as const, locale: "en" },
    });
    const input = await waitFor(() => numberInput(container));
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(1));

    await fireEvent.input(input, { target: { value: "55" } });
    await flush();
    expect(input.value).toBe("55");

    await rerender({ locale: "de" });
    await flush();

    // Before the fix this refetched unconditionally, wiping the edit
    // back to the server value (50) with no confirm and no toast.
    expect(input.value).toBe("55");
    expect(mockUiSchema).toHaveBeenCalledTimes(1);
  });

  it("does not discard an unsaved edit when expert mode is toggled", async () => {
    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER" as const, locale: "en" },
    });
    const input = await waitFor(() => numberInput(container));
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(1));

    await fireEvent.input(input, { target: { value: "55" } });
    await flush();
    expect(input.value).toBe("55");

    await fireEvent.click(expertCheckbox(container));
    await flush();

    expect(input.value).toBe("55");
    expect(mockUiSchema).toHaveBeenCalledTimes(1);
  });

  it("still refetches on a locale switch while the panel has no unsaved edits (control)", async () => {
    const { container, rerender } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER" as const, locale: "en" },
    });
    await waitFor(() => numberInput(container));
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(1));

    await rerender({ locale: "de" });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(2));
    expect(mockUiSchema).toHaveBeenLastCalledWith(
      "0001ABCD",
      1,
      "MASTER",
      "de",
      undefined,
      false,
    );
  });

  it("still reloads on a genuine channel switch even while the previous channel was dirty", async () => {
    const { container, rerender } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER" as const, locale: "en" },
    });
    const input = await waitFor(() => numberInput(container));
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(1));

    await fireEvent.input(input, { target: { value: "55" } });
    await flush();

    await rerender({ channel: 2 });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(2));
    expect(mockUiSchema).toHaveBeenLastCalledWith(
      "0001ABCD",
      2,
      "MASTER",
      "en",
      undefined,
      false,
    );
  });
});
