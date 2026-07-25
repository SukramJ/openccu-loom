// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { RxMode } from "$lib/api/types";

// ---------------------------------------------------------------------------
// Module mocks — hoisted before the module under test is imported.
// ---------------------------------------------------------------------------

const mockGetDevice = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
  },
}));

const mockToastPush = vi.fn();
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    push: (...args: unknown[]) => mockToastPush(...args),
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import {
  hasWakeupRxMode,
  anyDeviceNeedsWakeup,
  notifyWakeupPending,
} from "./wakeup-hint";

function device(rx_mode: RxMode | undefined): { rx_mode?: RxMode } {
  return rx_mode === undefined ? {} : { rx_mode };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("hasWakeupRxMode", () => {
  it("is true when the WAKEUP bit is set", () => {
    expect(hasWakeupRxMode({ wakeup: true })).toBe(true);
  });

  it("is true when the LAZY_CONFIG bit is set", () => {
    expect(hasWakeupRxMode({ lazy_config: true })).toBe(true);
  });

  it("is true when both battery bits are set", () => {
    expect(hasWakeupRxMode({ wakeup: true, lazy_config: true })).toBe(true);
  });

  it("is false for mains-only rx modes (always / burst / config)", () => {
    expect(hasWakeupRxMode({ always: true })).toBe(false);
    expect(hasWakeupRxMode({ burst: true })).toBe(false);
    expect(hasWakeupRxMode({ config: true })).toBe(false);
  });

  it("is false for an empty, undefined, or null rx mode", () => {
    expect(hasWakeupRxMode({})).toBe(false);
    expect(hasWakeupRxMode(undefined)).toBe(false);
    expect(hasWakeupRxMode(null)).toBe(false);
  });
});

describe("anyDeviceNeedsWakeup", () => {
  it("returns true when at least one device is a wakeup device", async () => {
    mockGetDevice.mockImplementation((addr: string) =>
      Promise.resolve(
        addr === "DEV_B"
          ? device({ wakeup: true })
          : device({ always: true }),
      ),
    );
    expect(await anyDeviceNeedsWakeup(["DEV_A:1", "DEV_B:2"])).toBe(true);
  });

  it("returns false when no device advertises a wakeup rx mode", async () => {
    mockGetDevice.mockResolvedValue(device({ always: true }));
    expect(await anyDeviceNeedsWakeup(["DEV_A:1", "DEV_B:2"])).toBe(false);
  });

  it("strips channel suffixes and fetches each distinct device once", async () => {
    mockGetDevice.mockResolvedValue(device({ always: true }));
    await anyDeviceNeedsWakeup(["DEV_A:1", "DEV_A:2", "DEV_A:0"]);
    expect(mockGetDevice).toHaveBeenCalledTimes(1);
    expect(mockGetDevice).toHaveBeenCalledWith("DEV_A");
  });

  it("treats a device fetch failure as 'no wakeup' without throwing", async () => {
    mockGetDevice.mockRejectedValue(new Error("boom"));
    await expect(anyDeviceNeedsWakeup(["DEV_A:1"])).resolves.toBe(false);
  });

  it("ignores empty address tokens", async () => {
    mockGetDevice.mockResolvedValue(device({ wakeup: true }));
    // A bare channel-suffix token with no device part must not fetch.
    expect(await anyDeviceNeedsWakeup([":1", ""])).toBe(false);
    expect(mockGetDevice).not.toHaveBeenCalled();
  });
});

describe("notifyWakeupPending", () => {
  it("pushes an info toast and returns true when a device needs wakeup", async () => {
    mockGetDevice.mockResolvedValue(device({ lazy_config: true }));
    const shown = await notifyWakeupPending(["DEV_A:3"]);
    expect(shown).toBe(true);
    expect(mockToastPush).toHaveBeenCalledTimes(1);
    const [severity, title, body] = mockToastPush.mock.calls[0];
    expect(severity).toBe("info");
    expect(title).toBe("links.wakeup_pending.title");
    expect(body).toBe("links.wakeup_pending.body");
  });

  it("shows no toast and returns false when no device needs wakeup", async () => {
    mockGetDevice.mockResolvedValue(device({ always: true }));
    const shown = await notifyWakeupPending(["DEV_A:3"]);
    expect(shown).toBe(false);
    expect(mockToastPush).not.toHaveBeenCalled();
  });
});
