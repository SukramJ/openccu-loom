// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";
import type { UISchema, DataPointSummary, DataPointChangedEvent } from "$lib/api/types";

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

const { mockUiSchema, mockListDataPoints, mockOpenEditSession, eventHandlers } = vi.hoisted(
  () => ({
    mockUiSchema: vi.fn(),
    mockListDataPoints: vi.fn(),
    mockOpenEditSession: vi.fn(),
    eventHandlers: [] as Array<(ev: unknown) => void>,
  }),
);

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

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// Controllable event-bus stub: tests fire a `data_point` envelope through
// `emit()` below to exercise the peer's live-brightness subscription
// without a real WebSocket. `$lib/stores/maintenance.svelte` also calls
// `subscribe` internally but the MASTER-only reload effect never reaches
// it in these LINK-paramset tests.
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: (handler: (ev: unknown) => void) => {
    eventHandlers.push(handler);
    return () => {
      const idx = eventHandlers.indexOf(handler);
      if (idx >= 0) eventHandlers.splice(idx, 1);
    };
  },
}));

import ChannelPanel from "./ChannelPanel.svelte";

function emit(payload: DataPointChangedEvent) {
  for (const handler of [...eventHandlers]) handler({ type: "data_point", payload });
}

function dp(
  parameter: string,
  value: unknown,
  extra: Partial<DataPointSummary> = {},
): DataPointSummary {
  return {
    parameter,
    value,
    observed: true,
    operations: { read: true, write: false, event: true },
    ...extra,
  } as DataPointSummary;
}

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

const BUTTON_TITLE = "channel.brightness.apply_tooltip";

// Flushes both the microtask queue (async effect bodies) and Svelte's
// reactive scheduler — mirrors ConfirmDialog.test.ts's flush sequence.
async function flush() {
  await tick();
  await Promise.resolve();
  await tick();
}

beforeEach(() => {
  vi.clearAllMocks();
  eventHandlers.length = 0;
  mockUiSchema.mockResolvedValue(linkSchema());
  // Rejects with a plain (non-ApiError) error so the edit-lock effect
  // falls through silently and the panel keeps working optimistically —
  // these tests are about the brightness helper, not session locking.
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
  mockListDataPoints.mockResolvedValue([]);
});

afterEach(() => cleanup());

describe("ChannelPanel — motion-detector brightness helper (LINK)", () => {
  it("shows the button once the peer reports brightness and fills the field on click (happy path)", async () => {
    mockListDataPoints.mockResolvedValue([dp("BRIGHTNESS", 200)]);

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalledWith("PEERDEV", 2));
    const button = await waitFor(() => screen.getByTitle(BUTTON_TITLE));

    const input = document.querySelector('input[type="number"]') as HTMLInputElement;
    expect(input).toBeTruthy();
    expect(input.value).toBe("50");

    await fireEvent.click(button);
    expect(input.value).toBe("200");
  });

  it("hides the button when the peer exposes no brightness data point", async () => {
    mockListDataPoints.mockResolvedValue([dp("STATE", true)]);

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalled());
    await flush();
    expect(screen.queryByTitle(BUTTON_TITLE)).not.toBeInTheDocument();
  });

  it("hides the button without crashing when the peer data-points fetch fails (error path)", async () => {
    mockListDataPoints.mockRejectedValue(new Error("peer channel unreachable"));

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalled());
    await flush();
    expect(screen.queryByTitle(BUTTON_TITLE)).not.toBeInTheDocument();
    // The panel itself still rendered (schema loaded fine); only the
    // optional brightness context failed to load.
    expect(screen.getByText("channel.export")).toBeInTheDocument();
  });

  it("does not query the peer at all for a VALUES paramset", async () => {
    render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    await flush();
    expect(mockListDataPoints).not.toHaveBeenCalled();
    expect(screen.queryByTitle(BUTTON_TITLE)).not.toBeInTheDocument();
  });

  it("does not query the peer when paramset is LINK but no peer is set", async () => {
    render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "LINK", locale: "en" },
    });

    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    await flush();
    expect(mockListDataPoints).not.toHaveBeenCalled();
    expect(screen.queryByTitle(BUTTON_TITLE)).not.toBeInTheDocument();
  });

  it("follows a live brightness push from the peer channel", async () => {
    mockListDataPoints.mockResolvedValue([dp("BRIGHTNESS", 100)]);

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    const button = await waitFor(() => screen.getByTitle(BUTTON_TITLE));
    const input = document.querySelector('input[type="number"]') as HTMLInputElement;

    emit({
      central: "c1",
      interface: "HmIP-RF",
      channel_address: "PEERDEV:2",
      parameter: "BRIGHTNESS",
      value: 150,
    });
    await flush();

    await fireEvent.click(button);
    expect(input.value).toBe("150");
  });

  it("ignores a live push for a second, different brightness parameter once locked onto the first", async () => {
    mockListDataPoints.mockResolvedValue([dp("BRIGHTNESS", 100)]);

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    const button = await waitFor(() => screen.getByTitle(BUTTON_TITLE));
    const input = document.querySelector('input[type="number"]') as HTMLInputElement;

    emit({
      central: "c1",
      interface: "HmIP-RF",
      channel_address: "PEERDEV:2",
      parameter: "ILLUMINATION",
      value: 999,
    });
    await flush();

    await fireEvent.click(button);
    expect(input.value).toBe("100");
  });

  it("ignores a live push targeting a different channel address", async () => {
    mockListDataPoints.mockResolvedValue([dp("BRIGHTNESS", 100)]);

    render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "LINK",
        peer: "PEERDEV:2",
        locale: "en",
      },
    });

    const button = await waitFor(() => screen.getByTitle(BUTTON_TITLE));
    const input = document.querySelector('input[type="number"]') as HTMLInputElement;

    emit({
      central: "c1",
      interface: "HmIP-RF",
      channel_address: "OTHERDEV:9",
      parameter: "BRIGHTNESS",
      value: 999,
    });
    await flush();

    await fireEvent.click(button);
    expect(input.value).toBe("100");
  });
});
