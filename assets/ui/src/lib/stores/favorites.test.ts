// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// @vitest-environment happy-dom
//
// Favorites are per-user server state, but the store is a module-level
// singleton that outlives a session. The tests below drive the real
// authStore through a logout/login in the same tab and assert on what the
// favorites store then exposes — not on any reset call, which would only
// prove the two can work together.
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("$lib/api/client", () => ({
  api: {
    me: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    getPreference: vi.fn(),
    putPreference: vi.fn(),
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
  setUnauthorizedHandler: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

import { api } from "$lib/api/client";
import { authStore } from "./auth.svelte";
import { favoritesStore, type Favorite } from "./favorites.svelte";

const meMock = api.me as ReturnType<typeof vi.fn>;
const loginMock = api.login as ReturnType<typeof vi.fn>;
const logoutMock = api.logout as ReturnType<typeof vi.fn>;
const getPreferenceMock = api.getPreference as ReturnType<typeof vi.fn>;
const putPreferenceMock = api.putPreference as ReturnType<typeof vi.fn>;

const alicePins: Favorite[] = [
  { type: "device", id: "0001ABCD", label: "Alice Thermostat" },
];
const bobPins: Favorite[] = [
  { type: "program", id: "1234", label: "Bob Wecker" },
];

async function loginAs(subject: string) {
  loginMock.mockResolvedValueOnce({ subject, role: "admin" });
  await authStore.login(subject, "pw");
}

beforeEach(() => {
  vi.clearAllMocks();
  logoutMock.mockResolvedValue(undefined);
  putPreferenceMock.mockResolvedValue(undefined);
});

describe("favoritesStore — identity lifecycle", () => {
  it("serves the pins of the operator that is signed in now", async () => {
    await loginAs("alice");
    getPreferenceMock.mockResolvedValueOnce(alicePins);
    await favoritesStore.load();
    expect(favoritesStore.loaded).toBe(true);
    expect(favoritesStore.isPinned("device", "0001ABCD")).toBe(true);

    await authStore.logout();
    await loginAs("bob");

    // Bob's session must not inherit Alice's pins, and the call sites'
    // `if (!loaded)` guard has to let the reload through.
    expect(favoritesStore.loaded).toBe(false);
    expect(favoritesStore.items).toEqual([]);
    expect(favoritesStore.isPinned("device", "0001ABCD")).toBe(false);

    getPreferenceMock.mockResolvedValueOnce(bobPins);
    await favoritesStore.load();
    expect(favoritesStore.items).toEqual(bobPins);
    expect(favoritesStore.isPinned("program", "1234")).toBe(true);
  });

  it("never persists the previous operator's pins onto the new one", async () => {
    await loginAs("alice");
    getPreferenceMock.mockResolvedValueOnce(alicePins);
    await favoritesStore.load();

    await authStore.logout();
    await loginAs("bob");

    // Bob pins something before any view got round to loading his list.
    getPreferenceMock.mockResolvedValueOnce(bobPins);
    const pinned = await favoritesStore.toggle({
      type: "device",
      id: "0009FEED",
      label: "Bob Lampe",
    });

    expect(pinned).toBe(true);
    expect(putPreferenceMock).toHaveBeenCalledTimes(1);
    const written = putPreferenceMock.mock.calls[0][1] as Favorite[];
    expect(written).toEqual([
      ...bobPins,
      { type: "device", id: "0009FEED", label: "Bob Lampe" },
    ]);
    expect(written).not.toContainEqual(alicePins[0]);
  });

  it("keeps a load that started before the boot probe answered", async () => {
    // The app fires the auth probe and the first preference reads next to
    // each other, so the identity is routinely still unknown when the
    // favorites request goes out. Discarding that answer would leave the
    // star icons empty on every fresh page load.
    await authStore.logout(); // fresh tab: no identity yet
    meMock.mockResolvedValueOnce({ subject: "erin", role: "admin" });
    const probing = authStore.probe();
    getPreferenceMock.mockResolvedValueOnce(alicePins);
    const loading = favoritesStore.load();
    await Promise.all([probing, loading]);

    expect(favoritesStore.loaded).toBe(true);
    expect(favoritesStore.items).toEqual(alicePins);
  });

  it("drops a response that arrives after the identity changed", async () => {
    // Distinct subjects from the tests above — the store is the real
    // module singleton and keeps whatever the previous test left in it.
    await loginAs("carol");
    let resolveCarol!: (v: Favorite[]) => void;
    getPreferenceMock.mockReturnValueOnce(
      new Promise<Favorite[]>((resolve) => {
        resolveCarol = resolve;
      }),
    );
    const inflight = favoritesStore.load();

    await authStore.logout();
    await loginAs("dave");
    resolveCarol(alicePins);
    await inflight;

    expect(favoritesStore.loaded).toBe(false);
    expect(favoritesStore.items).toEqual([]);
  });
});
