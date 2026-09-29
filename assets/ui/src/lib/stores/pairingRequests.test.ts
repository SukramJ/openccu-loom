// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mirrors addonUpdate.test.ts: mock the WS pump so the WS refresh-on-event
// path (ensureStream → the daemon pushes `pairing.requests_changed` → the
// store re-fetches) can be driven by invoking the captured handler
// directly, exactly like the real events pump would.

let capturedHandler: ((ev: { type: string; payload?: unknown }) => void) | null =
  null;

vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
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
    listPairingRequests: vi.fn(),
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

import { api } from "$lib/api/client";
import { subscribe } from "$lib/stores/events.svelte";
import { pairingRequestsStore } from "./pairingRequests.svelte";
import type { PairingView } from "$lib/api/types";

const subscribeMock = subscribe as ReturnType<typeof vi.fn>;
const listPairingRequestsMock = api.listPairingRequests as ReturnType<typeof vi.fn>;

function pairingView(overrides: Partial<PairingView> = {}): PairingView {
  return {
    id: "req-1",
    app: "Home Assistant",
    name: "Kitchen bridge",
    address: "192.0.2.10",
    role: "operator",
    code: "123456",
    created: "2026-01-01T09:00:00Z",
    expires: "2026-01-01T09:05:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  capturedHandler = null;
  listPairingRequestsMock.mockResolvedValue([]);
});

afterEach(() => {
  pairingRequestsStore.close();
});

describe("pairingRequestsStore.refresh", () => {
  it("seeds the request list from GET /pairing-requests", async () => {
    listPairingRequestsMock.mockResolvedValueOnce([pairingView()]);
    await pairingRequestsStore.refresh();
    expect(pairingRequestsStore.requests).toHaveLength(1);
    expect(pairingRequestsStore.requests[0].id).toBe("req-1");
    expect(pairingRequestsStore.error).toBeNull();
  });

  it("captures a failed fetch in `error` without throwing", async () => {
    listPairingRequestsMock.mockRejectedValueOnce(new Error("network down"));
    await pairingRequestsStore.refresh();
    expect(pairingRequestsStore.error).toBe("network down");
  });
});

describe("pairingRequestsStore WS refresh (pairing.requests_changed)", () => {
  it("re-fetches the list when the broadcast arrives", async () => {
    await pairingRequestsStore.refresh();
    pairingRequestsStore.ensureStream();
    expect(capturedHandler).not.toBeNull();

    listPairingRequestsMock.mockResolvedValueOnce([pairingView({ id: "req-2" })]);
    capturedHandler!({ type: "pairing.requests_changed", payload: { pending: 1 } });

    await vi.waitFor(() => {
      expect(pairingRequestsStore.requests.map((r) => r.id)).toEqual(["req-2"]);
    });
  });

  it("ignores envelopes of an unrelated type", async () => {
    await pairingRequestsStore.refresh();
    pairingRequestsStore.ensureStream();
    const before = pairingRequestsStore.requests;

    capturedHandler!({ type: "addon_update.state_changed", payload: {} });

    expect(pairingRequestsStore.requests).toBe(before);
  });

  it("re-subscribes on the next ensureStream() after close()", async () => {
    await pairingRequestsStore.refresh();
    pairingRequestsStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(1);

    pairingRequestsStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(1);

    pairingRequestsStore.close();
    pairingRequestsStore.ensureStream();
    expect(subscribeMock).toHaveBeenCalledTimes(2);
  });
});
