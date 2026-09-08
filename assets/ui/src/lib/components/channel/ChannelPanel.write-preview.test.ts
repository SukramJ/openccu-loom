// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { UISchema } from "$lib/api/types";

const { mockUiSchema, mockPutParamset, mockSetValue, mockGetDevice } = vi.hoisted(
  () => ({
    mockUiSchema: vi.fn(),
    mockPutParamset: vi.fn(),
    mockSetValue: vi.fn(),
    mockGetDevice: vi.fn(),
  }),
);

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...args: unknown[]) => mockUiSchema(...args),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: vi.fn().mockRejectedValue(new Error("sessions not wired")),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
    putParamset: (...args: unknown[]) => mockPutParamset(...args),
    putLinkParamset: vi.fn().mockResolvedValue(undefined),
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

const mockToastWarn = vi.fn();
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: vi.fn(),
    error: vi.fn(),
    warn: (...args: unknown[]) => mockToastWarn(...args),
    info: vi.fn(),
    push: vi.fn(),
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
import { prefs } from "$lib/stores/preferences.svelte";

function masterSchema(value: unknown = 5): UISchema {
  return {
    channel: {
      address: "0001ABCD:1",
      number: 1,
      type: "SWITCH",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "TEMPERATURE_OFFSET",
        label: "Temperature offset",
        type: "FLOAT",
        unit: "°C",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value,
      },
    ],
  } as UISchema;
}

function buttons(text: string): HTMLButtonElement[] {
  return Array.from(document.querySelectorAll("button")).filter(
    (b) => b.textContent?.trim() === text,
  ) as HTMLButtonElement[];
}

async function renderAndDirty(value = "42") {
  const view = render(ChannelPanel, {
    props: { address: "0001ABCD", channel: 1, paramset: "MASTER", locale: "en" },
  });
  await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
  const input = await waitFor(() => {
    const el = document.querySelector('input[type="number"]') as HTMLInputElement | null;
    expect(el).toBeTruthy();
    return el as HTMLInputElement;
  });
  await fireEvent.input(input, { target: { value } });
  await waitFor(() => expect(buttons("channel.save_n").length).toBeGreaterThan(0));
  await fireEvent.click(buttons("channel.save_n")[0]);
  return view;
}

const originalPreview = prefs.writePreview;

beforeEach(() => {
  vi.clearAllMocks();
  prefs.writePreview = true;
  mockPutParamset.mockResolvedValue(undefined);
  mockSetValue.mockResolvedValue(undefined);
  mockGetDevice.mockResolvedValue({ rx_mode: { always: true } });
  mockUiSchema.mockResolvedValue(masterSchema());
});

afterEach(() => {
  prefs.writePreview = originalPreview;
  cleanup();
});

describe("ChannelPanel — write preview", () => {
  // A MASTER write is device configuration the operator cannot inspect from
  // the device itself, so nothing must reach the CCU until the preview is
  // confirmed.
  it("writes nothing until the preview is confirmed", async () => {
    await renderAndDirty();
    await waitFor(() => {
      expect(buttons("channel.preview.write").length).toBe(1);
    });
    expect(mockPutParamset).not.toHaveBeenCalled();
  });

  it("performs the write once the preview is confirmed", async () => {
    await renderAndDirty();
    await waitFor(() => expect(buttons("channel.preview.write").length).toBe(1));
    await fireEvent.click(buttons("channel.preview.write")[0]);
    await waitFor(() => {
      expect(mockPutParamset).toHaveBeenCalledWith(
        "0001ABCD:1",
        "MASTER",
        { TEMPERATURE_OFFSET: 42 },
        undefined,
      );
    });
  });

  it("cancelling the preview leaves the edit staged and writes nothing", async () => {
    await renderAndDirty();
    await waitFor(() => expect(buttons("channel.preview.write").length).toBe(1));
    await fireEvent.click(buttons("common.cancel")[0]);
    await waitFor(() => expect(buttons("channel.preview.write").length).toBe(0));
    expect(mockPutParamset).not.toHaveBeenCalled();
    // The change is still staged, so the operator can adjust and retry.
    expect(buttons("channel.save_n").length).toBeGreaterThan(0);
  });

  it("writes straight through when the preference is off", async () => {
    prefs.writePreview = false;
    await renderAndDirty();
    await waitFor(() => {
      expect(mockPutParamset).toHaveBeenCalled();
    });
    expect(buttons("channel.preview.write").length).toBe(0);
  });

  // The request line is the one thing in the dialog nothing else in the app
  // would contradict, so it is pinned against the paths api.putParamset and
  // api.putLinkParamset actually build. The LINK path in particular is
  // `/link-ps/`, not the `/link-paramsets/` its method name suggests.
  it("names the request path the client will actually call", async () => {
    await renderAndDirty();
    const dialog = await waitFor(() => {
      const el = document.querySelector('[role="dialog"]');
      expect(el).toBeTruthy();
      return el as HTMLElement;
    });
    expect(dialog.textContent).toContain(
      "PUT /api/v1/devices/0001ABCD:1/paramsets/MASTER",
    );
  });

  // VALUES is a control write, not configuration: its effect is the point,
  // and a dialog in front of a light switch is friction with nothing behind
  // it.
  it("never previews a VALUES write", async () => {
    render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalled());
    const input = await waitFor(() => {
      const el = document.querySelector('input[type="number"]') as HTMLInputElement | null;
      expect(el).toBeTruthy();
      return el as HTMLInputElement;
    });
    await fireEvent.input(input, { target: { value: "7" } });
    await waitFor(() => expect(buttons("channel.save_n").length).toBeGreaterThan(0));
    await fireEvent.click(buttons("channel.save_n")[0]);

    await waitFor(() => {
      expect(mockSetValue).toHaveBeenCalledWith("0001ABCD", 1, "TEMPERATURE_OFFSET", 7);
    });
    expect(buttons("channel.preview.write").length).toBe(0);
  });
});

describe("ChannelPanel — read-back", () => {
  // rfd clamps an out-of-range value to MAX and answers ok; a plain success
  // toast on top of that is a lie the operator has no way to catch.
  it("warns when the device kept a different value than was written", async () => {
    mockUiSchema
      .mockResolvedValueOnce(masterSchema(5))
      // The reload after the write reports what the device actually kept.
      .mockResolvedValue(masterSchema(30));

    await renderAndDirty("42");
    await waitFor(() => expect(buttons("channel.preview.write").length).toBe(1));
    await fireEvent.click(buttons("channel.preview.write")[0]);

    await waitFor(() => {
      expect(mockToastWarn).toHaveBeenCalledWith(
        "channel.readback.title",
        "channel.readback.body",
      );
    });
  });

  it("stays quiet when the device kept what was written", async () => {
    mockUiSchema
      .mockResolvedValueOnce(masterSchema(5))
      .mockResolvedValue(masterSchema(42));

    await renderAndDirty("42");
    await waitFor(() => expect(buttons("channel.preview.write").length).toBe(1));
    await fireEvent.click(buttons("channel.preview.write")[0]);

    await waitFor(() => expect(mockPutParamset).toHaveBeenCalled());
    expect(mockToastWarn).not.toHaveBeenCalled();
  });
});
