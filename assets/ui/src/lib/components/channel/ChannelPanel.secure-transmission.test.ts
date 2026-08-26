// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Wiring test for SecureTransmission's mount inside ChannelPanel: the row
// only appears on a MASTER panel, and it must receive the SAME edit-lock
// token ChannelPanel itself holds (and go disabled the moment ChannelPanel
// loses or never acquires that lock) — see ChannelPanel.svelte's `{#if
// paramset === "MASTER"}` block. SecureTransmission.test.ts covers the
// component's own read/write/confirm behaviour in isolation.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { UISchema, EditSessionResponse } from "$lib/api/types";

const {
  mockUiSchema,
  mockListDataPoints,
  mockOpenEditSession,
  mockGetParamset,
  mockPutParamset,
} = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockListDataPoints: vi.fn().mockResolvedValue([]),
  mockOpenEditSession: vi.fn(),
  mockGetParamset: vi.fn(),
  mockPutParamset: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
    openEditSession: (...args: unknown[]) => mockOpenEditSession(...args),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    getParamset: (...args: unknown[]) => mockGetParamset(...args),
    putParamset: (...args: unknown[]) => mockPutParamset(...args),
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

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import ChannelPanel from "./ChannelPanel.svelte";
import { ApiError } from "$lib/api/client";

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

function valuesSchema(): UISchema {
  return {
    channel: {
      address: "0001ABCD:1",
      number: 1,
      type: "SWITCH",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "LEVEL",
        label: "Level",
        type: "FLOAT",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 0.5,
      },
    ],
  };
}

function session(token: string): EditSessionResponse {
  return {
    token,
    key: "channel:0001ABCD:1:MASTER",
    expires: new Date(Date.now() + 300_000).toISOString(),
  };
}

async function switchElements(container: HTMLElement): Promise<HTMLElement[]> {
  return Array.from(container.querySelectorAll('[role="switch"]')) as HTMLElement[];
}

// SecureTransmission mounts ahead of ParameterGrid in ChannelPanel's
// markup (the `{#if paramset === "MASTER"}` block that holds it precedes
// the grid render further down), so its switch is always first in
// document order — ahead of the fixture's own BOOL (LOGGING) switch.
function secureSwitch(elements: HTMLElement[]): HTMLElement {
  if (elements.length === 0) throw new Error("no switch rendered");
  return elements[0];
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPutParamset.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

describe("ChannelPanel — SecureTransmission wiring", () => {
  it("threads the panel's own edit-lock token into SecureTransmission's write (happy path)", async () => {
    mockUiSchema.mockResolvedValue(masterSchema());
    mockOpenEditSession.mockResolvedValue(session("panel-tok-1"));
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: false });
    mockConfirmAsk.mockResolvedValue(true);

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
    });
    await waitFor(() => expect(mockGetParamset).toHaveBeenCalled());
    expect(mockGetParamset).toHaveBeenCalledWith("0001ABCD:1", "MASTER");

    // Two switches mount for this fixture: the AES_ACTIVE row (mounted
    // first in ChannelPanel's markup, ahead of ParameterGrid) and the
    // BOOL LOGGING parameter the grid itself renders as a switch.
    const elements = await waitFor(() => {
      const els = Array.from(
        container.querySelectorAll('[role="switch"]'),
      ) as HTMLElement[];
      expect(els.length).toBe(2);
      return els;
    });
    const sw = secureSwitch(elements);
    await fireEvent.click(sw);

    await waitFor(() => expect(mockPutParamset).toHaveBeenCalledTimes(1));
    // The token comes from ChannelPanel's own lockSession — not a
    // hard-coded / undefined value — proving the two components share
    // the same edit lock rather than SecureTransmission opening its own.
    expect(mockPutParamset).toHaveBeenCalledWith(
      "0001ABCD:1",
      "MASTER",
      { AES_ACTIVE: true },
      "panel-tok-1",
    );
  });

  it("disables the row once the panel learns another session holds the lock (edge case)", async () => {
    mockUiSchema.mockResolvedValue(masterSchema());
    mockOpenEditSession.mockRejectedValue(
      new ApiError(423, {}, "locked by alice"),
    );
    mockGetParamset.mockResolvedValue({ AES_ACTIVE: true });

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
    });

    const sw = await waitFor(() => {
      const els = Array.from(
        container.querySelectorAll('[role="switch"]'),
      ) as HTMLElement[];
      expect(els.length).toBe(2);
      const el = secureSwitch(els);
      expect(el.hasAttribute("disabled")).toBe(true);
      return el;
    });

    await fireEvent.click(sw);
    // bits-ui's Switch.Root guards its own onclick against `disabled`,
    // so the click never reaches SecureTransmission's write path.
    expect(mockPutParamset).not.toHaveBeenCalled();
  });

  it("never mounts the secure-transmission row for a VALUES panel (structural guard)", async () => {
    mockUiSchema.mockResolvedValue(valuesSchema());
    mockOpenEditSession.mockResolvedValue(session("panel-tok-2"));

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());

    // Give the (absent) SecureTransmission mount a chance to run before
    // asserting the negative.
    await new Promise((r) => setTimeout(r, 0));
    expect(mockGetParamset).not.toHaveBeenCalled();
    expect(await switchElements(container)).toHaveLength(0);
  });
});
