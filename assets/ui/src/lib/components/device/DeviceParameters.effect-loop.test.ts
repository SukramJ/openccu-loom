// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import type { ChannelSummary, DeviceDetail, UISchema } from "$lib/api/types";

const { mockUiSchema } = vi.hoisted(() => ({ mockUiSchema: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  api: {
    uiSchema: (...a: unknown[]) => mockUiSchema(...a),
    listDataPoints: vi.fn().mockResolvedValue([]),
    openEditSession: vi.fn().mockRejectedValue(new Error("sessions not wired")),
    heartbeatEditSession: vi.fn().mockResolvedValue(null),
    closeEditSession: vi.fn().mockResolvedValue(undefined),
  },
  ApiError: class ApiError extends Error {
    status = 500;
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: () => () => {},
}));

import DeviceParameters from "./DeviceParameters.svelte";

function schema(ch: number): UISchema {
  return {
    channel: { address: `DEV:${ch}`, number: ch, type: "SWITCH", device_address: "DEV" },
    parameters: [
      {
        name: "ON_TIME",
        label: "On time",
        type: "FLOAT",
        operations: { read: true, write: true, event: false },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: 0.5,
      },
    ],
  } as UISchema;
}

function ch(no: number): ChannelSummary {
  return { address: `DEV:${no}`, number: no, type: "SWITCH", paramset_key: "MASTER", data_points_count: 1 };
}

afterEach(() => cleanup());

// The page counts unsaved edits per channel through each panel's
// onDirtyChange. That callback runs inside the panel's effect; writing the
// page's counts from it must not wake the effect again, or one keystroke
// spins the page until Svelte gives up (effect_update_depth_exceeded) —
// seconds of a frozen tab on a slow machine.
describe("DeviceParameters — editing a field", () => {
  it("settles after one edit instead of looping on the dirty count", async () => {
    mockUiSchema.mockImplementation((_a: string, c: number) => Promise.resolve(schema(c)));
    const errors: unknown[] = [];
    const onError = (e: ErrorEvent) => errors.push(e.error ?? e.message);
    window.addEventListener("error", onError);
    const consoleError = vi.spyOn(console, "error").mockImplementation((...a) => errors.push(a[0]));
    try {
      const { container } = render(DeviceParameters, {
        props: {
          detail: { address: "DEV", name: "Dev", model: "M", channels: [] } as unknown as DeviceDetail,
          locale: "en",
          deviceChannel: null,
          channelZero: null,
          channels: [ch(1)],
          linkCounts: new Map(),
          settings: createRawSnippet(() => ({ render: () => "<span></span>" })),
        },
      });
      const input = await waitFor(() => {
        const el = container.querySelector('input[type="number"]') as HTMLInputElement | null;
        expect(el).toBeTruthy();
        return el as HTMLInputElement;
      });
      await fireEvent.input(input, { target: { value: "1.5" } });
      await waitFor(() => expect(container.textContent).toContain("links.editor.apply"));
      const loops = errors.filter((e) => String(e instanceof Error ? e.message : e).includes("effect_update_depth_exceeded"));
      expect(loops).toEqual([]);
    } finally {
      window.removeEventListener("error", onError);
      consoleError.mockRestore();
    }
  });
});
