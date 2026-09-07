// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { Link, UISchema } from "$lib/api/types";

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

const { mockUiSchema, mockPutLinkParamset, mockGetDevice } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockPutLinkParamset: vi.fn(),
  mockGetDevice: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: vi.fn().mockRejectedValue(new Error("sessions not wired")),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    putParamset: vi.fn().mockResolvedValue(undefined),
    putLinkParamset: (...args: unknown[]) => mockPutLinkParamset(...args),
    setValue: vi.fn().mockResolvedValue(undefined),
    takeOverEditSession: vi.fn(),
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
  friendlyError: (err: unknown) =>
    err instanceof Error ? err.message : String(err),
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

const mockToastError = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastPush = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warn: vi.fn(),
    push: (...args: unknown[]) => mockToastPush(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import Host from "./LinkConfigPanelNullLinkHost.svelte";

const SENDER = "SENDERDEV:2";
const RECEIVER = "RECVDEV:1";

function testLink(): Link {
  return {
    sender_address: SENDER,
    receiver_address: RECEIVER,
    name: "Treppenlicht",
    description: "",
    direction: "outgoing",
    sender_channel_type: "KEY_TRANSCEIVER",
    receiver_channel_type: "SWITCH_VIRTUAL_RECEIVER",
  } as Link;
}

// One writable FLOAT so the panel can go dirty and offer Save.
function linkSchema(address: string, channel: number): UISchema {
  return {
    channel: {
      address: `${address}:${channel}`,
      number: channel,
      type: "SWITCH_VIRTUAL_RECEIVER",
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
  } as UISchema;
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUiSchema.mockImplementation((addr: string, ch: number) =>
    Promise.resolve(linkSchema(addr, ch)),
  );
  mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
});

afterEach(() => cleanup());

describe("LinkConfigPanel — the editor closes while a LINK save is in flight", () => {
  // The operator clicks Save and, before the PUT comes back, leaves the
  // editor. DeviceLinks nulls its `editing` holder, and LinkConfigPanel's
  // $derived props (`peer={link.sender_address}`, `address` from
  // `link.receiver_address`) are read again by ChannelPanel.save() after
  // the await — on a link that is now null.
  //
  // What the operator saw: the save succeeded on the CCU and the UI
  // reported "channel.save_failed — Cannot read properties of null
  // (reading 'sender_address')".
  it("does not raise a save-failed toast when the link is nulled mid-flight", async () => {
    let resolvePut: () => void = () => {};
    mockPutLinkParamset.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolvePut = () => resolve();
        }),
    );

    const { container } = render(Host, {
      props: { link: testLink(), locale: "de" },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());

    // Dirty the receiver-side panel and save.
    const input = await waitFor(() => {
      const el = document.querySelector(
        'input[type="number"]',
      ) as HTMLInputElement | null;
      expect(el).toBeTruthy();
      return el as HTMLInputElement;
    });
    await fireEvent.input(input, { target: { value: "75" } });
    const saveButton = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "channel.save_n",
    );
    expect(saveButton).toBeTruthy();
    await fireEvent.click(saveButton as HTMLButtonElement);
    await waitFor(() => expect(mockPutLinkParamset).toHaveBeenCalled());

    // …and leave the editor while the PUT is still pending. This is the
    // real gesture: the panel's own Close button, wired to onBack.
    const closeButton = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "common.close",
    );
    expect(closeButton).toBeTruthy();
    await fireEvent.click(closeButton as HTMLButtonElement);
    await flush();

    resolvePut();
    await flush();
    await flush();

    const failures = mockToastError.mock.calls.filter(
      ([title]) => title === "channel.save_failed",
    );
    expect(
      failures,
      `the save succeeded on the CCU but the UI reported it as failed: ${JSON.stringify(failures)}`,
    ).toHaveLength(0);
  });
});
