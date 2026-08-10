// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock the api module — getSystemCCUs is the only entry point the
// store calls; controllable per test via the returned spy.
vi.mock("$lib/api/client", () => ({
  api: {
    getSystemCCUs: vi.fn(),
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

// Capture the handler passed to subscribe() so tests can simulate
// inbound WS envelopes without a real WebSocket.
let capturedHandler: ((ev: unknown) => void) | null = null;
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: vi.fn((handler: (ev: unknown) => void) => {
    capturedHandler = handler;
    return () => {
      capturedHandler = null;
    };
  }),
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import { api } from "$lib/api/client";
import { centralStore } from "./centrals.svelte";

const getSystemCCUsMock = api.getSystemCCUs as ReturnType<typeof vi.fn>;

type Entry = {
  name: string;
  host: string;
  available: boolean;
  is_ha_app: boolean;
  configured_interfaces: string[];
  readiness: {
    phase: "unknown" | "waiting_for_ccu" | "loading_hub" | "loading_devices" | "ready";
    ready: boolean;
    interfaces_loaded: number;
    interfaces_total: number;
  };
};

function makeEntry(overrides: Partial<Entry> & { name: string }): Entry {
  return {
    host: "10.0.0.1",
    available: true,
    is_ha_app: false,
    configured_interfaces: [],
    readiness: {
      phase: "ready",
      ready: true,
      interfaces_loaded: 1,
      interfaces_total: 1,
    },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
});

afterEach(() => {
  centralStore.close();
});

describe("centralStore.refresh", () => {
  it("populates items from api.getSystemCCUs()", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({ name: "ccu1" }),
      makeEntry({
        name: "ccu2",
        readiness: {
          phase: "waiting_for_ccu",
          ready: false,
          interfaces_loaded: 0,
          interfaces_total: 0,
        },
      }),
    ]);
    await centralStore.refresh();
    expect(centralStore.items).toHaveLength(2);
    expect(centralStore.byName("ccu1")?.readiness.phase).toBe("ready");
    expect(centralStore.byName("ccu2")?.readiness.phase).toBe(
      "waiting_for_ccu",
    );
    expect(centralStore.error).toBeNull();
  });

  it("surfaces a fetch error and leaves prior items untouched", async () => {
    // refresh() only replaces `items` on success; a failed fetch must not
    // wipe out whatever the fleet last successfully reported.
    getSystemCCUsMock.mockResolvedValueOnce([makeEntry({ name: "ccu1" })]);
    await centralStore.refresh();
    expect(centralStore.items).toHaveLength(1);

    getSystemCCUsMock.mockRejectedValueOnce(new Error("network down"));
    await centralStore.refresh();
    expect(centralStore.error).toBe("network down");
    expect(centralStore.items).toHaveLength(1);
  });
});

describe("centralStore WS readiness updates", () => {
  it("patches the matching central in place on central.readiness_changed", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({
        name: "ccu1",
        readiness: {
          phase: "waiting_for_ccu",
          ready: false,
          interfaces_loaded: 0,
          interfaces_total: 0,
        },
      }),
    ]);
    await centralStore.refresh();
    centralStore.ensureStream();
    expect(capturedHandler).not.toBeNull();

    capturedHandler!({
      type: "central.readiness_changed",
      payload: {
        central: "ccu1",
        phase: "loading_devices",
        ready: false,
        interfaces_loaded: 2,
        interfaces_total: 4,
      },
    });

    const updated = centralStore.byName("ccu1");
    expect(updated?.readiness.phase).toBe("loading_devices");
    expect(updated?.readiness.interfaces_loaded).toBe(2);
    expect(updated?.readiness.interfaces_total).toBe(4);
  });

  it("marks available=true once a readiness update reports ready", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({
        name: "ccu1",
        available: false,
        readiness: {
          phase: "loading_devices",
          ready: false,
          interfaces_loaded: 1,
          interfaces_total: 2,
        },
      }),
    ]);
    await centralStore.refresh();
    centralStore.ensureStream();

    capturedHandler!({
      type: "central.readiness_changed",
      payload: {
        central: "ccu1",
        phase: "ready",
        ready: true,
        interfaces_loaded: 2,
        interfaces_total: 2,
      },
    });

    expect(centralStore.byName("ccu1")?.available).toBe(true);
    expect(centralStore.byName("ccu1")?.readiness.ready).toBe(true);
  });

  it("ignores envelopes for an unknown central name", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([makeEntry({ name: "ccu1" })]);
    await centralStore.refresh();
    centralStore.ensureStream();

    capturedHandler!({
      type: "central.readiness_changed",
      payload: {
        central: "unknown-ccu",
        phase: "ready",
        ready: true,
        interfaces_loaded: 1,
        interfaces_total: 1,
      },
    });

    expect(centralStore.items).toHaveLength(1);
    expect(centralStore.byName("unknown-ccu")).toBeUndefined();
  });

  it("ignores envelopes of a different event type", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({
        name: "ccu1",
        readiness: {
          phase: "waiting_for_ccu",
          ready: false,
          interfaces_loaded: 0,
          interfaces_total: 0,
        },
      }),
    ]);
    await centralStore.refresh();
    centralStore.ensureStream();

    capturedHandler!({
      type: "central.state_changed",
      payload: {
        central: "ccu1",
        phase: "ready",
        ready: true,
        interfaces_loaded: 1,
        interfaces_total: 1,
      },
    });

    expect(centralStore.byName("ccu1")?.readiness.phase).toBe(
      "waiting_for_ccu",
    );
  });
});

describe("centralStore fleet getters", () => {
  it("computes allReady/anyReady/notReady for a mixed fleet", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({ name: "ccu-ready" }),
      makeEntry({
        name: "ccu-loading",
        readiness: {
          phase: "loading_devices",
          ready: false,
          interfaces_loaded: 1,
          interfaces_total: 3,
        },
      }),
    ]);
    await centralStore.refresh();

    expect(centralStore.allReady).toBe(false);
    expect(centralStore.anyReady).toBe(true);
    expect(centralStore.notReady.map((c) => c.name)).toEqual([
      "ccu-loading",
    ]);
  });

  it("allReady is true only when every central is ready", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([
      makeEntry({ name: "ccu1" }),
      makeEntry({ name: "ccu2" }),
    ]);
    await centralStore.refresh();

    expect(centralStore.allReady).toBe(true);
    expect(centralStore.anyReady).toBe(true);
    expect(centralStore.notReady).toHaveLength(0);
  });

  it("allReady/anyReady are false for an empty fleet", async () => {
    getSystemCCUsMock.mockResolvedValueOnce([]);
    await centralStore.refresh();

    expect(centralStore.allReady).toBe(false);
    expect(centralStore.anyReady).toBe(false);
    expect(centralStore.notReady).toHaveLength(0);
  });
});
