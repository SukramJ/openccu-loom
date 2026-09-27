import { describe, it, expect, vi } from "vitest";
import { createPairing, type PairingApi } from "./pairing.svelte";
import type { CentralPairingStatus } from "$lib/api/client";

// A daemon whose status answers the test hands out one by one.
function fakeApi(statuses: Array<CentralPairingStatus | Error>) {
  const queue = [...statuses];
  let release: (() => void) | null = null;
  const api: PairingApi = {
    start: vi.fn(async () => ({ pairing_id: "p1", code: "123456", fingerprint: "ab", expires_in: 300 })),
    status: vi.fn(async () => {
      if (queue.length === 0) await new Promise<void>((r) => (release = r));
      const next = queue.shift() ?? { state: "pending" as const };
      if (next instanceof Error) throw next;
      return next;
    }),
    cancel: vi.fn(async () => {}),
  };
  return { api, push: (s: CentralPairingStatus) => (queue.push(s), release?.()) };
}

const noSleep = { sleep: async () => {}, minIntervalMs: 0 };
const req = { host: "box.local" };

describe("pairing state machine", () => {
  it("shows the code while pending and ends approved with the scopes", async () => {
    const { api } = fakeApi([{ state: "pending" }, { state: "approved", scopes: ["rpc:read"] }]);
    const p = createPairing(api, noSleep);
    await p.start(req);
    expect(p.code).toBe("123456");
    expect(p.phase).toBe("approved");
    expect(p.scopes).toEqual(["rpc:read"]);
    expect(p.approvedId).toBe("p1");
    expect(api.status).toHaveBeenCalledWith("p1", 20);
  });

  it("ends in the box's verdict when it rejects or lets the request expire", async () => {
    for (const state of ["rejected", "expired"] as const) {
      const { api } = fakeApi([{ state }]);
      const p = createPairing(api, noSleep);
      await p.start(req);
      expect(p.phase).toBe(state);
      expect(p.approvedId).toBe("");
    }
  });

  it("reports a failed start or poll as an error", async () => {
    const startFail: PairingApi = {
      start: async () => {
        throw new Error("pairing is switched off");
      },
      status: vi.fn(),
      cancel: vi.fn(),
    };
    const a = createPairing(startFail, noSleep);
    await a.start(req);
    expect(a.phase).toBe("error");
    expect(a.error).toBe("pairing is switched off");

    const { api } = fakeApi([new Error("network down")]);
    const b = createPairing(api, noSleep);
    await b.start(req);
    expect(b.phase).toBe("error");
    expect(b.error).toBe("network down");
  });

  it("withdraws a pending request on cancel and ignores a late approval", async () => {
    const { api, push } = fakeApi([]);
    const p = createPairing(api, noSleep);
    const running = p.start(req);
    await vi.waitFor(() => expect(p.phase).toBe("pending"));
    await p.cancel();
    expect(api.cancel).toHaveBeenCalledWith("p1");
    push({ state: "approved", scopes: ["*"] });
    await running;
    expect(p.phase).toBe("cancelled");
    expect(p.approvedId).toBe("");
  });

  it("paces a daemon that answers pending at once", async () => {
    const sleep = vi.fn(async () => {});
    const { api } = fakeApi([{ state: "pending" }, { state: "pending" }, { state: "approved" }]);
    const p = createPairing(api, { sleep, minIntervalMs: 1000, now: () => 0 });
    await p.start(req);
    expect(sleep).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledWith(1000);
  });
});
