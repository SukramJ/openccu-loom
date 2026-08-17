// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// @vitest-environment happy-dom
//
// requireDisarmOptions was a plain top-level `const` that called t(...)
// once, at component construction, so the "require code to disarm"
// picker stayed in whatever locale was active when the view mounted
// even though a live language switch re-rendered every other string on
// the page.
import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { prefs } from "$lib/stores/preferences.svelte";
import type { AlarmZone } from "$lib/api/types";

let mockZonesConfig: AlarmZone[] = [];
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return mockZonesConfig;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

const mockGetAlarmZone = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getAlarmZone: (...args: unknown[]) => mockGetAlarmZone(...args),
    putAlarmZone: vi.fn(),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

// Deliberately NOT mocking $lib/i18n here — this test needs the real
// catalogue and its live `prefs.locale` read to reproduce the freeze.

import AlarmPolicies from "./AlarmPolicies.svelte";

function zone(config: Record<string, unknown> = {}) {
  return { id: "zone-1", name: "Ground floor", position: 1, config };
}

const originalLocale = prefs.locale;

beforeEach(() => {
  vi.clearAllMocks();
  mockZonesConfig = [{ id: "zone-1", name: "Ground floor" }];
  mockGetAlarmZone.mockResolvedValue(zone());
});

afterEach(() => {
  prefs.locale = originalLocale;
  cleanup();
});

describe("AlarmPolicies — require-disarm picker follows a live locale switch", () => {
  it("re-renders the picker label when the UI language changes", async () => {
    prefs.locale = "de";
    render(AlarmPolicies);

    await waitFor(() =>
      expect(screen.getByText("Automatisch (an, sobald Codes existieren)")).toBeInTheDocument(),
    );

    prefs.locale = "en";
    await tick();

    await waitFor(() =>
      expect(screen.getByText("Automatic (on when codes exist)")).toBeInTheDocument(),
    );
    expect(screen.queryByText("Automatisch (an, sobald Codes existieren)")).toBeNull();
  });
});
