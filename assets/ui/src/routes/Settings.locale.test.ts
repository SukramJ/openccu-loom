// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.
//
// @vitest-environment happy-dom
//
// ALL_TABS was a plain top-level `const` that called t(...) once, at
// component construction — so a live language switch re-rendered every
// other localized string on the page except the settings tab list, which
// stayed frozen in whatever locale was active when the view mounted.
import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { prefs } from "$lib/stores/preferences.svelte";

const {
  mockGetConfigSchema,
  mockGetEffectiveConfig,
  mockGetConfigChanges,
  mockInfo,
  mockGetStartupCapture,
  mockReloadMQTT,
} = vi.hoisted(() => ({
  mockGetConfigSchema: vi.fn(),
  mockGetEffectiveConfig: vi.fn(),
  mockGetConfigChanges: vi.fn(),
  mockInfo: vi.fn(),
  mockGetStartupCapture: vi.fn(),
  mockReloadMQTT: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  setUnauthorizedHandler: vi.fn(),
  api: {
    getConfigSchema: (...args: unknown[]) => mockGetConfigSchema(...args),
    getEffectiveConfig: (...args: unknown[]) => mockGetEffectiveConfig(...args),
    getConfigChanges: (...args: unknown[]) => mockGetConfigChanges(...args),
    info: (...args: unknown[]) => mockInfo(...args),
    getStartupCapture: (...args: unknown[]) => mockGetStartupCapture(...args),
    reloadMQTT: (...args: unknown[]) => mockReloadMQTT(...args),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

// Deliberately NOT mocking $lib/i18n here — this test needs the real
// catalogue and its live `prefs.locale` read to reproduce the freeze.

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

vi.mock("$lib/components/settings/SectionEditor.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/UsersAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/TokensAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ChangePasswordCard.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/CentralsAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/RoomsFunctionsAdmin.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/TlsCertCard.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/SystemUpdatePanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ChangesOverview.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/settings/ConnectivityLights.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/ui/ExpertGate.svelte", () => ({ default: () => {} }));

import Settings from "./Settings.svelte";

const originalLocale = prefs.locale;

beforeEach(() => {
  vi.clearAllMocks();
  mockGetConfigSchema.mockResolvedValue({ sections: [], fields: [] });
  mockGetEffectiveConfig.mockResolvedValue({ config: {}, sources: {} });
  mockGetConfigChanges.mockResolvedValue({ fields: [] });
  mockInfo.mockResolvedValue({ capabilities: [] });
  mockGetStartupCapture.mockResolvedValue({
    enabled: false,
    duration_seconds: 600,
    anonymise: true,
  });
});

afterEach(() => {
  prefs.locale = originalLocale;
  cleanup();
});

describe("Settings — tab list follows a live locale switch", () => {
  it("re-renders the sidebar tab labels when the UI language changes", async () => {
    prefs.locale = "de";
    render(Settings);

    await waitFor(() => expect(screen.getAllByText("Allgemein").length).toBeGreaterThan(0));

    prefs.locale = "en";
    await tick();

    await waitFor(() => expect(screen.getAllByText("General").length).toBeGreaterThan(0));
    expect(screen.queryByText("Allgemein")).toBeNull();
  });
});
