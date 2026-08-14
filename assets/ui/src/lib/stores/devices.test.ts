// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the api module before the store imports it. The factory
// provides a controllable spy on `listDevices` for each test.
vi.mock("$lib/api/client", () => ({
  api: {
    listDevices: vi.fn(),
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
}));

// Stub out the events store — we don't want a live WebSocket in unit tests.
// The stub records the handlers the store registers so a test can drive a
// broadcast into it the way the WS pump would.
const wsPump = vi.hoisted(() => ({
  handlers: [] as ((ev: unknown) => void)[],
}));
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: vi.fn((h: (ev: unknown) => void) => {
    wsPump.handlers.push(h);
    return () => {
      wsPump.handlers = wsPump.handlers.filter((x) => x !== h);
    };
  }),
}));

// Mock authStore.probe — called on 401.
vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));

// Mock i18n — the store calls t("api.error.unauthorized") on 401;
// return the key itself so tests can assert on a stable string.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import { api } from "$lib/api/client";
import { deviceStore } from "./devices.svelte";

function makePage(
  items: { address: string; available?: boolean }[],
  total: number,
) {
  return { items, total };
}

const listDevicesMock = api.listDevices as ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  // reset store state between tests by calling close()
  deviceStore.close();
});

describe("deviceStore.refresh — success path", () => {
  it("starts in loading=true during fetch, then populates items", async () => {
    listDevicesMock.mockResolvedValueOnce(
      makePage([{ address: "ABC123" }], 1),
    );
    const promise = deviceStore.refresh();
    // loading must be true while the promise is in flight
    expect(deviceStore.loading).toBe(true);
    await promise;
    expect(deviceStore.loading).toBe(false);
    expect(deviceStore.items).toHaveLength(1);
    expect(deviceStore.items[0].address).toBe("ABC123");
    expect(deviceStore.error).toBeNull();
  });

  it("clears a previous error on successful refresh", async () => {
    // first call fails
    listDevicesMock.mockRejectedValueOnce(new Error("network"));
    await deviceStore.refresh();
    expect(deviceStore.error).not.toBeNull();

    // second call succeeds
    listDevicesMock.mockResolvedValueOnce(makePage([{ address: "X1" }], 1));
    await deviceStore.refresh();
    expect(deviceStore.error).toBeNull();
    expect(deviceStore.items).toHaveLength(1);
  });
});

describe("deviceStore.refresh — error path", () => {
  it("sets error when the API rejects with a plain Error", async () => {
    // The store is a singleton; prior tests may have populated items.
    // Assert on the error/loading contract only, which is reset each call.
    listDevicesMock.mockRejectedValueOnce(new Error("connection refused"));
    await deviceStore.refresh();
    expect(deviceStore.loading).toBe(false);
    expect(deviceStore.error).toBe("connection refused");
  });

  it("calls authStore.probe and sets expiry message on a 401", async () => {
    const { authStore } = await import("$lib/stores/auth.svelte");
    const { ApiError: AE } = await import("$lib/api/client");
    listDevicesMock.mockRejectedValueOnce(new AE(401, {}, "unauthorized"));
    await deviceStore.refresh();
    expect((authStore.probe as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(1);
    // t() is mocked to return the key, so the store sets the i18n key.
    expect(deviceStore.error).toBe("api.error.unauthorized");
  });
});

describe("deviceStore.refresh — pagination", () => {
  it("loads all pages when total > first page size", async () => {
    // Simulate 450 devices: first page returns 200, remainder on page 2 + 3.
    const page1 = Array.from({ length: 200 }, (_, i) => ({ address: `D${i}` }));
    const page2 = Array.from({ length: 200 }, (_, i) => ({ address: `D${200 + i}` }));
    const page3 = Array.from({ length: 50 }, (_, i) => ({ address: `D${400 + i}` }));
    listDevicesMock
      .mockResolvedValueOnce(makePage(page1, 450))
      .mockResolvedValueOnce(makePage(page2, 450))
      .mockResolvedValueOnce(makePage(page3, 450));

    await deviceStore.refresh();
    expect(deviceStore.items).toHaveLength(450);
    expect(listDevicesMock).toHaveBeenCalledTimes(3);
  });

  it("stops fetching once accumulated count reaches total", async () => {
    const page1 = Array.from({ length: 200 }, (_, i) => ({ address: `A${i}` }));
    const page2 = Array.from({ length: 10 }, (_, i) => ({ address: `B${i}` }));
    listDevicesMock
      .mockResolvedValueOnce(makePage(page1, 210))
      .mockResolvedValueOnce(makePage(page2, 210));

    await deviceStore.refresh();
    expect(deviceStore.items).toHaveLength(210);
    expect(listDevicesMock).toHaveBeenCalledTimes(2);
  });
});

describe("deviceStore — live availability", () => {
  it("applies a device_availability broadcast without refetching the list", async () => {
    listDevicesMock.mockResolvedValueOnce(
      makePage([{ address: "ABC123", available: true }], 1),
    );
    await deviceStore.refresh();
    deviceStore.ensureStream();

    expect(wsPump.handlers).toHaveLength(1);
    wsPump.handlers[0]({
      type: "device_availability",
      payload: { central: "ccu1", address: "ABC123", available: false },
    });

    expect(deviceStore.items[0].available).toBe(false);
    // A live frame must not trigger a full reload — that is what the
    // resync signal is for.
    expect(listDevicesMock).toHaveBeenCalledTimes(1);
  });

  it("ignores an availability frame for a device the list does not hold", async () => {
    listDevicesMock.mockResolvedValueOnce(
      makePage([{ address: "ABC123", available: true }], 1),
    );
    await deviceStore.refresh();
    deviceStore.ensureStream();

    wsPump.handlers[0]({
      type: "device_availability",
      payload: { central: "ccu1", address: "OTHER1", available: false },
    });

    expect(deviceStore.items).toHaveLength(1);
    expect(deviceStore.items[0].available).toBe(true);
  });
});
