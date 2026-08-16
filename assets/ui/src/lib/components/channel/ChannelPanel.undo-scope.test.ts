// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// The device-config view mounts two ChannelPanels side by side (device
// MASTER plus channel MASTER), and the link editor mounts the receiver
// next to the sender. The undo / redo shortcut listens on the window, so
// without an owner one Ctrl+Z steps every mounted change stack — the
// second revert lands in a panel the operator is not looking at.
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
    putParamset: vi.fn(),
    putLinkParamset: vi.fn(),
    setValue: vi.fn(),
    takeOverEditSession: vi.fn(),
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

function renderPanel(address: string, channel: number) {
  return render(ChannelPanel, {
    props: { address, channel, paramset: "MASTER" as const, locale: "en" },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUiSchema.mockImplementation((address: string, channel: number) =>
    Promise.resolve(masterSchema(address, channel)),
  );
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
  mockListDataPoints.mockResolvedValue([]);
});

afterEach(() => cleanup());

async function numberInputs(): Promise<HTMLInputElement[]> {
  await waitFor(() =>
    expect(document.querySelectorAll('input[type="number"]').length).toBe(2),
  );
  return [...document.querySelectorAll('input[type="number"]')] as HTMLInputElement[];
}

describe("ChannelPanel — undo shortcut scope", () => {
  it("steps only the change stack of the panel the operator worked in", async () => {
    const first = renderPanel("0001ABCD", 1);
    const second = renderPanel("0002ABCD", 2);
    const [firstInput, secondInput] = await numberInputs();

    await fireEvent.input(firstInput, { target: { value: "55" } });
    await fireEvent.input(secondInput, { target: { value: "77" } });
    await flush();
    expect(firstInput.value).toBe("55");
    expect(secondInput.value).toBe("77");

    // The operator's last interaction was inside the second panel.
    await fireEvent.pointerDown(second.container.firstElementChild as Element);
    await fireEvent.pointerDown(secondInput);
    await fireEvent.keyDown(window, { key: "z", ctrlKey: true });
    await flush();

    expect(secondInput.value).toBe("50");
    expect(firstInput.value).toBe("55");
    expect(first).toBeTruthy();
  });

  it("moves the shortcut with the operator's focus", async () => {
    renderPanel("0001ABCD", 1);
    renderPanel("0002ABCD", 2);
    const [firstInput, secondInput] = await numberInputs();

    await fireEvent.input(firstInput, { target: { value: "55" } });
    await fireEvent.input(secondInput, { target: { value: "77" } });
    await flush();

    await fireEvent.focusIn(firstInput);
    await fireEvent.keyDown(window, { key: "z", ctrlKey: true });
    await flush();

    expect(firstInput.value).toBe("50");
    expect(secondInput.value).toBe("77");
  });
});
