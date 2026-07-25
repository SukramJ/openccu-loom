// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { UISchema } from "$lib/api/types";

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

const {
  mockUiSchema,
  mockOpenEditSession,
  mockPutLinkParamset,
  mockPutParamset,
  mockSetValue,
  mockGetDevice,
} = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockOpenEditSession: vi.fn(),
  mockPutLinkParamset: vi.fn(),
  mockPutParamset: vi.fn(),
  mockSetValue: vi.fn(),
  mockGetDevice: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: (...args: unknown[]) => mockOpenEditSession(...args),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    putParamset: (...args: unknown[]) => mockPutParamset(...args),
    putLinkParamset: (...args: unknown[]) => mockPutLinkParamset(...args),
    setValue: (...args: unknown[]) => mockSetValue(...args),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

const mockToastSuccess = vi.fn();
const mockToastPush = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: vi.fn(),
    warn: vi.fn(),
    push: (...args: unknown[]) => mockToastPush(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: () => () => {},
}));

import ChannelPanel from "./ChannelPanel.svelte";

function linkSchema(): UISchema {
  return {
    channel: {
      address: "0001ABCD:1",
      number: 1,
      type: "KEYMATIC",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "SHORT_COND_VALUE_LO",
        label: "Short cond value lo",
        type: "FLOAT",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 50,
      },
    ],
  };
}

function masterSchema(): UISchema {
  return {
    channel: {
      address: "0001ABCD:1",
      number: 1,
      type: "SWITCH",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "LOGGING",
        label: "Logging",
        type: "BOOL",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 0,
      },
    ],
  };
}

// Flushes both the microtask queue (async effect bodies) and Svelte's
// reactive scheduler — mirrors ChannelPanel.brightness.test.ts's flush.
async function flush() {
  await Promise.resolve();
  await Promise.resolve();
}

async function dirtyAndSave(container: HTMLElement) {
  const input = await waitFor(() => {
    const el = document.querySelector('input[type="number"]') as HTMLInputElement | null;
    expect(el).toBeTruthy();
    return el as HTMLInputElement;
  });
  await fireEvent.input(input, { target: { value: "75" } });

  const saveButtons = Array.from(container.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === "channel.save_n",
  );
  expect(saveButtons.length).toBeGreaterThan(0);
  await fireEvent.click(saveButtons[0]);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
  mockPutLinkParamset.mockResolvedValue(undefined);
  mockPutParamset.mockResolvedValue(undefined);
  mockSetValue.mockResolvedValue(undefined);
  // Default: the device is mains-powered (no wakeup hint) unless a test
  // overrides it.
  mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
});

afterEach(() => cleanup());

describe("ChannelPanel — LINK save wakeup hint", () => {
  it("shows the pending-wakeup info toast instead of the plain saved toast for a WAKEUP battery device (happy path)", async () => {
    mockUiSchema.mockResolvedValue(linkSchema());
    mockGetDevice.mockResolvedValue({ rx_mode: { wakeup: true } });

    const { container } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    await dirtyAndSave(container);

    await waitFor(() => {
      expect(mockPutLinkParamset).toHaveBeenCalledWith(
        "0001ABCD:1",
        "PEERDEV:2",
        { SHORT_COND_VALUE_LO: 75 },
        undefined,
      );
    });
    await waitFor(() => {
      expect(mockToastPush).toHaveBeenCalledTimes(1);
    });
    const [severity, title] = mockToastPush.mock.calls[0];
    expect(severity).toBe("info");
    expect(title).toBe("links.wakeup_pending.title");
    // The plain success toast is suppressed — the hint conveys success.
    expect(mockToastSuccess).not.toHaveBeenCalled();
    // The wakeup check queries the channel's OWN device (the LINK
    // paramset lives on the sending channel, not the peer).
    expect(mockGetDevice).toHaveBeenCalledWith("0001ABCD");
  });

  it("shows the plain saved toast for a mains (ALWAYS) device", async () => {
    mockUiSchema.mockResolvedValue(linkSchema());
    mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });

    const { container } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    await dirtyAndSave(container);

    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("channel.saved_short");
    });
    expect(mockToastPush).not.toHaveBeenCalled();
  });

  it("falls back to the plain saved toast when the wakeup lookup fails (error path)", async () => {
    mockUiSchema.mockResolvedValue(linkSchema());
    mockGetDevice.mockRejectedValue(new Error("device unreachable"));

    const { container } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    await dirtyAndSave(container);

    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("channel.saved_short");
    });
    expect(mockToastPush).not.toHaveBeenCalled();
  });

  it("never checks for a wakeup hint on a MASTER save, even for a battery device (edge case)", async () => {
    mockUiSchema.mockResolvedValue(masterSchema());
    mockGetDevice.mockResolvedValue({ rx_mode: { wakeup: true } });

    const { container } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "MASTER",
        locale: "en",
      },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());

    // BOOL parameters render as a bits-ui Switch (role="switch"), not a
    // plain checkbox input.
    const toggle = await waitFor(() => {
      const el = document.querySelector('[role="switch"]') as HTMLElement | null;
      expect(el).toBeTruthy();
      return el as HTMLElement;
    });
    await fireEvent.click(toggle);

    const saveButtons = Array.from(container.querySelectorAll("button")).filter(
      (b) => b.textContent?.trim() === "channel.save_n",
    );
    await fireEvent.click(saveButtons[0]);

    await waitFor(() => {
      expect(mockPutParamset).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(mockToastSuccess).toHaveBeenCalledWith("channel.saved_short");
    });
    // MASTER writes apply immediately regardless of rx mode — the LINK
    // gate in ChannelPanel's save() must not fire the device lookup.
    expect(mockGetDevice).not.toHaveBeenCalled();
    expect(mockToastPush).not.toHaveBeenCalled();
    await flush();
  });
});
