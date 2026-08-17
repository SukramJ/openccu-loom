// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// @vitest-environment happy-dom
//
// DURATION_OPTIONS was a plain top-level `const` that called t(...) once,
// at component construction, so the commissioning-window duration picker
// stayed in whatever locale was active when the view mounted even though
// a live language switch re-rendered every other string on the page.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { prefs } from "$lib/stores/preferences.svelte";

const { mockStatus, mockSetupPayload } = vi.hoisted(() => ({
  mockStatus: vi.fn(),
  mockSetupPayload: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    matterStatus: () => mockStatus(),
    matterSetupPayload: () => mockSetupPayload(),
    matterFabrics: () => Promise.resolve({ fabrics: [] }),
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

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

// Deliberately NOT mocking $lib/i18n here — this test needs the real
// catalogue and its live `prefs.locale` read to reproduce the freeze.

import { matterStore } from "$lib/stores/matter.svelte";
import MatterPair from "./MatterPair.svelte";

// Mirrors MatterPair.test.ts's renderPair(): the store is a module
// singleton, so each test resets and re-hydrates it explicitly.
async function renderPair() {
  matterStore.resetCommissioning();
  await matterStore.loadStatus();
  return render(MatterPair);
}

const WINDOW_NOT_OPEN = {
  enabled: true,
  listening: true,
  endpoint_count: 4,
  fabric_count: 0,
  enabled_count: 4,
  advertising: true,
  commissioning_window_open: false,
  commissioning_window_duration_seconds: 300,
};

const originalLocale = prefs.locale;

beforeEach(() => {
  vi.clearAllMocks();
  mockSetupPayload.mockResolvedValue({
    qr_code: "MT:Y.K90SO527JA0648G00",
    manual_code: "34970112332",
    discriminator: 3840,
    passcode: 20202021,
  });
});

afterEach(() => {
  prefs.locale = originalLocale;
  cleanup();
});

describe("MatterPair — duration picker follows a live locale switch", () => {
  it("re-renders the option labels when the UI language changes", async () => {
    prefs.locale = "de";
    mockStatus.mockResolvedValue(WINDOW_NOT_OPEN);
    await renderPair();

    await waitFor(() => expect(screen.getByText("5 Min")).toBeInTheDocument());

    prefs.locale = "en";
    await tick();

    await waitFor(() => expect(screen.getByText("5 min")).toBeInTheDocument());
    expect(screen.queryByText("5 Min")).toBeNull();
  });
});
