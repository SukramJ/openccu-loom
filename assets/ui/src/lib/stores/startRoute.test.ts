// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// A stored start route outlives the view it names: an operator who picked
// a page as their landing page before an update removed it must land on
// the router default (the device list), not on the not-found page.
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("$lib/api/client", () => ({
  api: {
    getPreference: vi.fn(),
    putPreference: vi.fn(),
    deletePreference: vi.fn(),
  },
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

vi.mock("./auth.svelte", () => ({ authStore: { identity: null } }));

vi.mock("./surfaces.svelte", () => ({
  surfacesStore: { visible: () => true },
}));

import { api } from "$lib/api/client";
import { startRouteStore } from "./startRoute.svelte";

const getPreferenceMock = api.getPreference as ReturnType<typeof vi.fn>;

async function loadStored(route: string) {
  getPreferenceMock.mockResolvedValueOnce(route);
  await startRouteStore.load();
  expect(startRouteStore.route).toBe(route);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("startRouteStore.resolve — removed views", () => {
  it("honours a stored route that still names a navigation view", async () => {
    await loadStored("#/programs");
    expect(startRouteStore.resolve()).toBe("#/programs");
  });

  it.each(["#/overview", "#/favorites", "#/energy", "#/diagrams"])(
    "falls back to the router default for the removed view %s",
    async (route) => {
      await loadStored(route);
      expect(startRouteStore.resolve()).toBe("");
    },
  );
});
