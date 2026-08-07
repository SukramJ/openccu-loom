// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// Pins the `loadGeneration` guard in ChannelPanel.svelte's `load()`: every
// call captures a monotonic generation counter at start, and any mutation of
// schema/serverValues/values/loading/loadError after the `await
// api.uiSchema(...)` first checks the call is still the latest generation. A
// slow response for a channel the user has already navigated away from must
// never overwrite what a later (possibly faster-resolving) channel's
// response produced.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";
import type { UISchema } from "$lib/api/types";

const { mockUiSchema, mockOpenEditSession } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockOpenEditSession: vi.fn(),
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

// Echoes the key (plus a JSON-encoded vars payload) so the rendered channel
// number / label can be asserted precisely instead of guessing at the real
// EN/DE catalogue string.
vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, string | number>) =>
    vars ? `${key}::${JSON.stringify(vars)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: () => () => {},
}));

import ChannelPanel from "./ChannelPanel.svelte";

// Deferred promise helper — lets the test control exactly when a given
// api.uiSchema(...) call resolves/rejects, independent of call order.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function channelSchema(n: number, level: number): UISchema {
  return {
    channel: {
      address: `0001ABCD:${n}`,
      number: n,
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
        value: level,
      },
    ],
  };
}

function numberInput(container: HTMLElement): HTMLInputElement {
  const el = container.querySelector('input[type="number"]');
  if (!el) throw new Error("expected a number input");
  return el as HTMLInputElement;
}

function headerText(container: HTMLElement): string {
  const p = container.querySelector("header p");
  if (!p) throw new Error("expected the channel header line");
  return p.textContent ?? "";
}

beforeEach(() => {
  vi.clearAllMocks();
  // Sessions not wired in these fixtures — irrelevant to the load race the
  // panel's own generation counter guards against.
  mockOpenEditSession.mockRejectedValue(new Error("sessions not wired"));
});

afterEach(() => cleanup());

describe("ChannelPanel — channel-switch load race", () => {
  it("discards a late channel-1 response after switching to channel 2 (race)", async () => {
    const chan1 = deferred<UISchema>();
    const chan2 = deferred<UISchema>();
    mockUiSchema.mockImplementation((_addr: string, ch: number) =>
      ch === 1 ? chan1.promise : chan2.promise,
    );
    const onLoaded = vi.fn();

    const { container, rerender } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "VALUES",
        locale: "en",
        onLoaded,
      },
    });

    await waitFor(() =>
      expect(mockUiSchema).toHaveBeenCalledWith(
        "0001ABCD",
        1,
        "VALUES",
        "en",
        undefined,
        false,
      ),
    );

    // Switch to channel 2 before channel 1's response ever arrives. This
    // fires a second, still-pending api.uiSchema(...) call.
    await rerender({ channel: 2 });
    await waitFor(() =>
      expect(mockUiSchema).toHaveBeenCalledWith(
        "0001ABCD",
        2,
        "VALUES",
        "en",
        undefined,
        false,
      ),
    );

    // Channel 2's (later-started) request resolves first.
    chan2.resolve(channelSchema(2, 99));
    await waitFor(() => expect(numberInput(container).value).toBe("99"));
    expect(headerText(container)).toContain('channel.kanal::{"n":2}');
    expect(onLoaded).toHaveBeenCalledTimes(1);
    expect(onLoaded).toHaveBeenLastCalledWith({ count: 1, error: false });

    // The stale channel-1 response now arrives late. It must be discarded
    // entirely — no DOM update, no second onLoaded call.
    chan1.resolve(channelSchema(1, 1));
    // Give the resolved promise's `.then` chain a chance to run before
    // asserting the negative.
    await new Promise((r) => setTimeout(r, 0));

    expect(numberInput(container).value).toBe("99");
    expect(headerText(container)).toContain('channel.kanal::{"n":2}');
    expect(onLoaded).toHaveBeenCalledTimes(1);
  });

  it("discards a late channel-1 error after switching to channel 2 (error variant)", async () => {
    const chan1 = deferred<UISchema>();
    const chan2 = deferred<UISchema>();
    mockUiSchema.mockImplementation((_addr: string, ch: number) =>
      ch === 1 ? chan1.promise : chan2.promise,
    );
    const onLoaded = vi.fn();

    const { container, rerender } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "VALUES",
        locale: "en",
        onLoaded,
      },
    });

    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(1));
    await rerender({ channel: 2 });
    await waitFor(() => expect(mockUiSchema).toHaveBeenCalledTimes(2));

    chan2.resolve(channelSchema(2, 42));
    await waitFor(() => expect(numberInput(container).value).toBe("42"));
    expect(onLoaded).toHaveBeenCalledTimes(1);

    // A late failure for the abandoned channel-1 request must not flip the
    // panel into its error state once channel 2 has already rendered.
    chan1.reject(new Error("ccu unreachable for channel 1"));
    await new Promise((r) => setTimeout(r, 0));

    expect(container.querySelector('input[type="number"]')).not.toBeNull();
    expect(numberInput(container).value).toBe("42");
    expect(onLoaded).toHaveBeenCalledTimes(1);
    expect(container.textContent).not.toContain("channel.schema_failed");
  });

  it("renders the schema normally without a race (control / sanity check)", async () => {
    mockUiSchema.mockResolvedValue(channelSchema(1, 7));
    const onLoaded = vi.fn();

    const { container } = render(ChannelPanel, {
      props: {
        address: "0001ABCD",
        channel: 1,
        paramset: "VALUES",
        locale: "en",
        onLoaded,
      },
    });

    await waitFor(() => expect(numberInput(container).value).toBe("7"));
    expect(headerText(container)).toContain('channel.kanal::{"n":1}');
    expect(onLoaded).toHaveBeenCalledTimes(1);
    expect(onLoaded).toHaveBeenLastCalledWith({ count: 1, error: false });
  });
});
