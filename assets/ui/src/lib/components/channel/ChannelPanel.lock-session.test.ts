// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// Pins the edit-lock banner lifecycle in ChannelPanel.svelte's `lockKey`
// effect (`lockedByOther` / `lockLost`, both `$state`):
//
//  - the effect resets `lockedByOther`/`lockLost` synchronously at the top,
//    before the async `api.openEditSession(key)` call, so a channel switch
//    never leaves the PREVIOUS channel's stale banner showing while the new
//    channel's own request is still in flight;
//  - the `catch` branch checks the effect's local `cancelled` flag before
//    setting `lockedByOther`, so a late 423 rejection for a channel the user
//    has already left can never clobber the currently-displayed channel's
//    state;
//  - the "take over" button's `catch` now routes every non-423 failure
//    (network error, 403 viewer role, 503 sessions-unwired) through
//    `toastStore.error(t("channel.take_over_failed"), ...)` instead of
//    silently swallowing it, and leaves the banner in place.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";
import type { UISchema, EditSessionResponse } from "$lib/api/types";

const { mockUiSchema, mockOpenEditSession, mockTakeOverEditSession } = vi.hoisted(() => ({
  mockUiSchema: vi.fn(),
  mockOpenEditSession: vi.fn(),
  mockTakeOverEditSession: vi.fn(),
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
    takeOverEditSession: (...args: unknown[]) => mockTakeOverEditSession(...args),
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

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
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
import { ApiError } from "$lib/api/client";

// Deferred promise helper — lets the test control exactly when a given
// api.openEditSession(key) call resolves/rejects, independent of order.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function schemaFor(channel: number): UISchema {
  return {
    channel: {
      address: `0001ABCD:${channel}`,
      number: channel,
      type: "SWITCH",
      device_address: "0001ABCD",
    },
    parameters: [
      {
        name: "STATE",
        label: "State",
        type: "BOOL",
        operations: { read: true, write: true, event: true },
        flags: { visible: true, internal: false, service: false },
        observed: true,
        value: false,
      },
    ],
  };
}

function session(token: string, key: string): EditSessionResponse {
  return {
    token,
    key,
    expires: new Date(Date.now() + 300_000).toISOString(),
  };
}

const LOCK_KEY_CH1 = "channel:0001ABCD:1:VALUES";
const LOCK_KEY_CH2 = "channel:0001ABCD:2:VALUES";
const BANNER_TEXT = "channel.session_lock_other";
const TAKE_OVER_TEXT = "channel.take_over";

function bannerShown(container: HTMLElement): boolean {
  return container.textContent?.includes(BANNER_TEXT) ?? false;
}

function takeOverButton(container: HTMLElement): HTMLElement {
  const btn = Array.from(container.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === TAKE_OVER_TEXT,
  );
  if (!btn) throw new Error("expected the take-over button to be rendered");
  return btn;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockUiSchema.mockImplementation((_addr: string, ch: number) =>
    Promise.resolve(schemaFor(ch)),
  );
});

afterEach(() => cleanup());

describe("ChannelPanel — edit-lock banner lifecycle", () => {
  it("clears a stale banner from the previous channel immediately on switch, before the new channel's lock response arrives", async () => {
    mockOpenEditSession.mockImplementation((key: string) => {
      if (key === LOCK_KEY_CH1) {
        return Promise.reject(new ApiError(423, {}, "held by alice"));
      }
      return new Promise<EditSessionResponse>(() => {
        // Channel 2's lock request stays pending for the whole test.
      });
    });

    const { container, rerender } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(bannerShown(container)).toBe(true));

    await rerender({ channel: 2 });
    await waitFor(() => expect(mockOpenEditSession).toHaveBeenCalledWith(LOCK_KEY_CH2));

    // The switch clears the stale banner synchronously — channel 2's own
    // request is still unresolved at this point.
    expect(bannerShown(container)).toBe(false);
  });

  it("does not let a late 423 rejection for an abandoned channel clobber the current channel's successful lock", async () => {
    const chan1Lock = deferred<EditSessionResponse>();
    mockOpenEditSession.mockImplementation((key: string) => {
      if (key === LOCK_KEY_CH1) return chan1Lock.promise;
      if (key === LOCK_KEY_CH2) {
        return Promise.resolve(session("tok-2", LOCK_KEY_CH2));
      }
      throw new Error(`unexpected lock key ${key}`);
    });

    const { container, rerender } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(mockOpenEditSession).toHaveBeenCalledWith(LOCK_KEY_CH1));

    await rerender({ channel: 2 });
    await waitFor(() => expect(mockOpenEditSession).toHaveBeenCalledWith(LOCK_KEY_CH2));
    await waitFor(() => expect(bannerShown(container)).toBe(false));

    // Channel 1's request — issued by an effect instance the switch has
    // since cleaned up — now rejects late with a 423.
    chan1Lock.reject(new ApiError(423, {}, "held by bob"));
    await new Promise((r) => setTimeout(r, 0));

    expect(bannerShown(container)).toBe(false);
  });

  it("shows an error toast and leaves the banner in place when take-over fails with a non-423 error", async () => {
    mockOpenEditSession.mockRejectedValue(new ApiError(423, {}, "held by alice"));
    mockTakeOverEditSession.mockRejectedValue(new Error("network down"));

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(bannerShown(container)).toBe(true));
    const button = takeOverButton(container);

    await fireEvent.click(button);

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "channel.take_over_failed",
        "network down",
      ),
    );
    // The failed take-over must not have silently cleared the conflict.
    expect(bannerShown(container)).toBe(true);
  });

  it("clears the banner and shows no error toast when take-over succeeds (happy-path guard)", async () => {
    mockOpenEditSession.mockRejectedValueOnce(new ApiError(423, {}, "held by alice"));
    mockOpenEditSession.mockResolvedValueOnce(session("tok-recovered", LOCK_KEY_CH1));
    mockTakeOverEditSession.mockResolvedValue(undefined);

    const { container } = render(ChannelPanel, {
      props: { address: "0001ABCD", channel: 1, paramset: "VALUES", locale: "en" },
    });

    await waitFor(() => expect(bannerShown(container)).toBe(true));
    const button = takeOverButton(container);

    await fireEvent.click(button);

    await waitFor(() => expect(bannerShown(container)).toBe(false));
    expect(mockToastError).not.toHaveBeenCalled();
  });
});
