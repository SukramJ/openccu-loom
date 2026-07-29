// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("$lib/api/client", () => ({
  api: {
    listAreas: vi.fn(),
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
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : String(err)),
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import { api } from "$lib/api/client";
import { areasStore } from "./areas.svelte";
import type { Area } from "$lib/api/types";

const listAreasMock = api.listAreas as ReturnType<typeof vi.fn>;

function area(overrides: Partial<Area> = {}): Area {
  return { id: "a1", name: "Ground floor", ...overrides };
}

beforeEach(() => {
  vi.clearAllMocks();
});

// Runs first, deliberately: the store is a module-level singleton (like
// devices.svelte.ts / centrals.svelte.ts), so `loaded` only starts false
// before any test in this file has called refresh() yet.
describe("areasStore.ensureLoaded", () => {
  it("fetches only once across repeated calls", async () => {
    listAreasMock.mockResolvedValue([area()]);
    areasStore.ensureLoaded();
    areasStore.ensureLoaded();
    areasStore.ensureLoaded();
    await Promise.resolve();
    await Promise.resolve();
    expect(listAreasMock).toHaveBeenCalledTimes(1);
  });
});

describe("areasStore.refresh", () => {
  it("loads and exposes the area list", async () => {
    listAreasMock.mockResolvedValueOnce([area()]);
    await areasStore.refresh();
    expect(areasStore.loading).toBe(false);
    expect(areasStore.error).toBeNull();
    expect(areasStore.loaded).toBe(true);
    expect(areasStore.areas).toHaveLength(1);
    expect(areasStore.areas[0].name).toBe("Ground floor");
  });

  it("sets error on failure", async () => {
    listAreasMock.mockRejectedValueOnce(new Error("network down"));
    await areasStore.refresh();
    expect(areasStore.error).toBe("network down");
  });

  it("calls authStore.probe and sets the expiry message on a 401", async () => {
    const { authStore } = await import("$lib/stores/auth.svelte");
    const { ApiError: AE } = await import("$lib/api/client");
    listAreasMock.mockRejectedValueOnce(new AE(401, {}, "unauthorized"));
    await areasStore.refresh();
    expect((authStore.probe as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(1);
    expect(areasStore.error).toBe("api.error.unauthorized");
  });
});

describe("areasStore.areas — sort order", () => {
  it("sorts by position ascending, ties by name, undefined position last", async () => {
    listAreasMock.mockResolvedValueOnce([
      area({ id: "c", name: "Zeta", position: undefined }),
      area({ id: "b", name: "Beta", position: 2 }),
      area({ id: "a", name: "Alpha", position: 1 }),
      area({ id: "d", name: "Aardvark", position: undefined }),
    ]);
    await areasStore.refresh();
    expect(areasStore.areas.map((a) => a.id)).toEqual(["a", "b", "d", "c"]);
  });
});

describe("areasStore.areaIdOf / roomsOf", () => {
  it("resolves the area owning a (central, room) pair", async () => {
    listAreasMock.mockResolvedValueOnce([
      area({
        id: "a1",
        name: "Upstairs",
        rooms: [
          { central: "ccu1", room: "Bedroom" },
          { central: "ccu1", room: "Attic" },
        ],
      }),
      area({ id: "a2", name: "Garden", rooms: [{ central: "ccu2", room: "Shed" }] }),
    ]);
    await areasStore.refresh();

    expect(areasStore.areaIdOf("ccu1", "Bedroom")).toBe("a1");
    expect(areasStore.areaIdOf("ccu2", "Shed")).toBe("a2");
    // Same room NAME on a different central is a distinct pair.
    expect(areasStore.areaIdOf("ccu2", "Bedroom")).toBeUndefined();
    expect(areasStore.areaIdOf("ccu1", "Nonexistent")).toBeUndefined();

    expect(areasStore.roomsOf("a1")).toEqual([
      { central: "ccu1", room: "Bedroom" },
      { central: "ccu1", room: "Attic" },
    ]);
    expect(areasStore.roomsOf("unknown-id")).toEqual([]);
  });
});
