// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mirrors alarmPanel.test.ts: mock the WS pump so the WS state-flow path
// (ensureStream → the daemon pushes `addon_update.state_changed` → the
// store repaints) can be driven by invoking the captured handler
// directly, exactly like the real events pump would.

let capturedHandler: ((ev: { type: string; payload?: unknown }) => void) | null =
  null;

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: vi.fn((h: (ev: { type: string; payload?: unknown }) => void) => {
    capturedHandler = h;
    return vi.fn();
  }),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getAddonUpdateStatus: vi.fn(),
    checkAddonUpdate: vi.fn(),
    installAddonUpdate: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

import { api, ApiError } from "$lib/api/client";
import { subscribe } from "$lib/stores/events.svelte";
import { addonUpdateStore } from "./addonUpdate.svelte";
import type { AddonUpdateStatus } from "$lib/api/types";

const subscribeMock = subscribe as ReturnType<typeof vi.fn>;

function idleStatus(overrides: Partial<AddonUpdateStatus> = {}): AddonUpdateStatus {
  return {
    supported: true,
    current_version: "0.47.0",
    state: "idle",
    ...overrides,
  };
}

const getAddonUpdateStatusMock = api.getAddonUpdateStatus as ReturnType<typeof vi.fn>;
const checkAddonUpdateMock = api.checkAddonUpdate as ReturnType<typeof vi.fn>;
const installAddonUpdateMock = api.installAddonUpdate as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
  getAddonUpdateStatusMock.mockResolvedValue(idleStatus());
  checkAddonUpdateMock.mockResolvedValue(undefined);
  installAddonUpdateMock.mockResolvedValue(undefined);
});

afterEach(() => {
  addonUpdateStore.close();
});

describe("addonUpdateStore.refresh", () => {
  it("seeds status from GET /system/addon-update", async () => {
    getAddonUpdateStatusMock.mockResolvedValueOnce(
      idleStatus({ latest_version: "0.48.0", update_available: true }),
    );
    await addonUpdateStore.refresh();
    expect(addonUpdateStore.status?.update_available).toBe(true);
    expect(addonUpdateStore.status?.latest_version).toBe("0.48.0");
    expect(addonUpdateStore.error).toBeNull();
  });

  it("captures a failed fetch in `error` without throwing", async () => {
    getAddonUpdateStatusMock.mockRejectedValueOnce(new Error("network down"));
    await addonUpdateStore.refresh();
    expect(addonUpdateStore.error).toBe("network down");
  });
});

describe("addonUpdateStore WS state flow (addon_update.state_changed)", () => {
  it("applies a broadcast pushed while the check runs in the background", async () => {
    await addonUpdateStore.refresh();
    addonUpdateStore.ensureStream();
    expect(capturedHandler).not.toBeNull();

    capturedHandler!({
      type: "addon_update.state_changed",
      payload: idleStatus({ state: "checking" }),
    });
    expect(addonUpdateStore.status?.state).toBe("checking");

    capturedHandler!({
      type: "addon_update.state_changed",
      payload: idleStatus({
        state: "idle",
        latest_version: "0.48.0",
        update_available: true,
      }),
    });
    expect(addonUpdateStore.status?.update_available).toBe(true);
  });

  it("ignores envelopes of an unrelated type", async () => {
    await addonUpdateStore.refresh();
    addonUpdateStore.ensureStream();
    const before = addonUpdateStore.status;

    capturedHandler!({ type: "alarm.state_changed", payload: { foo: "bar" } });

    expect(addonUpdateStore.status).toBe(before);
  });

  it("re-subscribes on the next ensureStream() after close()", async () => {
    await addonUpdateStore.refresh();
    addonUpdateStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(1);

    // A second ensureStream() without closing first must be a no-op —
    // the store already holds a live subscription.
    addonUpdateStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(1);

    addonUpdateStore.close();
    addonUpdateStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(2);
  });
});

describe("addonUpdateStore.check", () => {
  it("triggers the check verb and refreshes the status", async () => {
    // check() calls the verb, then refresh()es exactly once — queue a
    // single once-value for that one GET (an extra unconsumed
    // mockResolvedValueOnce would leak into and contaminate the next
    // test, since `clearAllMocks` does not drain the once-queue).
    getAddonUpdateStatusMock.mockResolvedValueOnce(
      idleStatus({ latest_version: "0.48.0", update_available: true }),
    );

    const ok = await addonUpdateStore.check();

    expect(ok).toBe(true);
    expect(checkAddonUpdateMock).toHaveBeenCalledTimes(1);
    expect(addonUpdateStore.status?.update_available).toBe(true);
    expect(addonUpdateStore.checking).toBe(false);
  });

  it("captures a rejected check in `error` and returns false", async () => {
    checkAddonUpdateMock.mockRejectedValueOnce(
      new ApiError(404, {}, "API 404 /system/addon-update/check: not found"),
    );
    const ok = await addonUpdateStore.check();
    expect(ok).toBe(false);
    expect(addonUpdateStore.error).toBeTruthy();
  });

  it("ignores a second concurrent call while one is already in flight", async () => {
    let resolveCheck: () => void = () => {};
    checkAddonUpdateMock.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        resolveCheck = resolve;
      }),
    );
    const first = addonUpdateStore.check();
    const second = await addonUpdateStore.check();
    expect(second).toBe(false);
    resolveCheck();
    await first;
    expect(checkAddonUpdateMock).toHaveBeenCalledTimes(1);
  });
});

describe("addonUpdateStore.install", () => {
  it("triggers the install verb and refreshes the status", async () => {
    // Same one-GET accounting as the check() test above.
    getAddonUpdateStatusMock.mockResolvedValueOnce(idleStatus({ state: "installing" }));

    const ok = await addonUpdateStore.install();

    expect(ok).toBe(true);
    expect(installAddonUpdateMock).toHaveBeenCalledTimes(1);
    expect(addonUpdateStore.status?.state).toBe("installing");
  });

  it("captures a 409 (no update available / already running) in `error`", async () => {
    installAddonUpdateMock.mockRejectedValueOnce(
      new ApiError(409, { detail: "no update available" }, "API 409: no update available"),
    );
    const ok = await addonUpdateStore.install();
    expect(ok).toBe(false);
    expect(addonUpdateStore.error).toBeTruthy();
  });
});

describe("addonUpdateStore.busy", () => {
  it("is true while a verb request is in flight", async () => {
    let resolveCheck: () => void = () => {};
    checkAddonUpdateMock.mockReturnValueOnce(
      new Promise<void>((resolve) => {
        resolveCheck = resolve;
      }),
    );
    const pending = addonUpdateStore.check();
    expect(addonUpdateStore.busy).toBe(true);
    resolveCheck();
    await pending;
    expect(addonUpdateStore.busy).toBe(false);
  });

  it("is true when the daemon-reported state is mid-operation", async () => {
    getAddonUpdateStatusMock.mockResolvedValueOnce(idleStatus({ state: "downloading" }));
    await addonUpdateStore.refresh();
    expect(addonUpdateStore.busy).toBe(true);
  });
});
