// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// Orchestration test for ChannelPanel's "Determine" wiring: determineParam
// calls api.determineParameter, stages the result through onParamChange
// (dirty-tracked, so it shows up in the working value immediately), and
// reports the outcome via a toast. ParameterField.determine.test.ts covers
// the button's own render/gating/spinner behaviour in isolation; this file
// covers the parent orchestration ChannelPanel adds around it.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { UISchema } from "$lib/api/types";

const { mockUiSchema, mockOpenEditSession, mockDetermineParameter } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockOpenEditSession: vi.fn(),
  mockDetermineParameter: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: (...args: unknown[]) => mockOpenEditSession(...args),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    getParamset: vi.fn().mockResolvedValue({}),
    putParamset: vi.fn(),
    putLinkParamset: vi.fn(),
    setValue: vi.fn(),
    takeOverEditSession: vi.fn(),
    determineParameter: (...args: unknown[]) => mockDetermineParameter(...args),
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

// Echoes the key (plus a JSON-encoded vars payload) so assertions can match
// on the i18n key rather than depending on the real EN/DE catalogue strings.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
    warn: vi.fn(),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import ChannelPanel from "./ChannelPanel.svelte";

function masterSchemaWithDeterminableTemperature(): UISchema {
  return {
    channel: {
      address: "0001ABCD:1",
      number: 1,
      type: "CLIMATE_TRANSCEIVER",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "TEMPERATURE",
        label: "Temperature",
        type: "FLOAT",
        operations: { read: true, write: true, event: false, determine: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 20,
      },
    ],
  };
}

async function determineButton(container: HTMLElement): Promise<HTMLElement> {
  return waitFor(() => {
    const btn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("parameter.determine") && !b.textContent?.includes("tooltip"),
    );
    expect(btn).toBeTruthy();
    return btn as HTMLElement;
  });
}

function numberInput(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector('input[type="number"]');
  if (!el) throw new Error("expected a number input");
  return el as HTMLInputElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  // Sessions not wired in these fixtures — the panel falls through and
  // keeps working optimistically, exactly as the determine read (which
  // needs no edit-lock token) expects.
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
});

afterEach(() => cleanup());

describe("ChannelPanel — Determine wiring", () => {
  it("stages the device's value into the working copy and shows a success toast (happy path)", async () => {
    mockUiSchema.mockResolvedValue(masterSchemaWithDeterminableTemperature());
    mockDetermineParameter.mockResolvedValue({ value: 23.5 });

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
    });

    const button = await determineButton(container);
    await fireEvent.click(button);

    await waitFor(() => expect(mockDetermineParameter).toHaveBeenCalledTimes(1));
    expect(mockDetermineParameter).toHaveBeenCalledWith(
      "0001ABCD",
      1,
      "MASTER",
      "TEMPERATURE",
    );

    await waitFor(() => expect(numberInput(container).value).toBe("23.5"));
    expect(mockToastSuccess).toHaveBeenCalledWith(
      'parameter.determine.done::{"name":"TEMPERATURE"}',
    );
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("shows an error toast and leaves the working value untouched when the backend rejects (error path)", async () => {
    mockUiSchema.mockResolvedValue(masterSchemaWithDeterminableTemperature());
    mockDetermineParameter.mockRejectedValue(new Error("ccu unreachable"));

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
    });

    const button = await determineButton(container);
    await fireEvent.click(button);

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "parameter.determine.failed",
        "ccu unreachable",
      ),
    );
    // The seeded value (20) survives — no partial/garbage stage on error.
    expect(numberInput(container).value).toBe("20");
  });

  it("shows the 'unsupported' toast and does not stage a value when the backend answers with no value (edge case)", async () => {
    mockUiSchema.mockResolvedValue(masterSchemaWithDeterminableTemperature());
    mockDetermineParameter.mockResolvedValue({ value: null });

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
    });

    const button = await determineButton(container);
    await fireEvent.click(button);

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "parameter.determine.failed",
        "parameter.determine.unsupported",
      ),
    );
    expect(numberInput(container).value).toBe("20");
  });

  it("does not render a Determine button on a VALUES panel (paramset-gating edge case)", async () => {
    mockUiSchema.mockResolvedValue(masterSchemaWithDeterminableTemperature());

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    // Give any (absent) determine button a chance to mount before asserting.
    await new Promise((r) => setTimeout(r, 0));
    const btn = Array.from(container.querySelectorAll("button")).find((b) =>
      b.textContent?.includes("parameter.determine"),
    );
    expect(btn).toBeUndefined();
    expect(mockDetermineParameter).not.toHaveBeenCalled();
  });
});
