// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// @vitest-environment happy-dom
//
// A sidebar tab click flips `activeTab` directly, never through the hash
// router — so App's `dirty.any()` route guard never saw it. Each
// SectionEditor lives in its own `{:else if activeTab === ...}` branch,
// so switching tabs destroys the current one outright: an edited but
// unsaved config field vanished silently, no confirm, no toast. This
// pins Settings.svelte's own confirm gate on the tab-switch click,
// driven through the real global `dirty` store (the same one
// SectionEditor now registers with).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

const {
  mockGetConfigSchema,
  mockGetEffectiveConfig,
  mockGetConfigChanges,
  mockInfo,
  mockGetStartupCapture,
  mockReloadMQTT,
  mockConfirmAsk,
} = vi.hoisted(() => ({
  mockGetConfigSchema: vi.fn(),
  mockGetEffectiveConfig: vi.fn(),
  mockGetConfigChanges: vi.fn(),
  mockInfo: vi.fn(),
  mockGetStartupCapture: vi.fn(),
  mockReloadMQTT: vi.fn(),
  mockConfirmAsk: vi.fn(),
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

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, string>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
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

// Deliberately NOT mocking $lib/stores/dirty.svelte — this test drives
// the real global registry, the same one SectionEditor registers with.
import { dirty } from "$lib/stores/dirty.svelte";
import Settings from "./Settings.svelte";

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
  dirty.discardAll();
  cleanup();
});

describe("Settings — tab switch respects unsaved config edits", () => {
  it("switches tabs freely when nothing is dirty", async () => {
    render(Settings);
    await waitFor(() => expect(screen.getByText("settings.interface")).toBeInTheDocument());

    await fireEvent.click(screen.getAllByText("settings.tab.mqtt")[0]);

    await waitFor(() =>
      expect(screen.getByText("settings.mqtt.reload_title")).toBeInTheDocument(),
    );
    expect(mockConfirmAsk).not.toHaveBeenCalled();
  });

  it("confirms before switching away from a dirty editor, and cancelling keeps the tab", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    render(Settings);
    await waitFor(() => expect(screen.getByText("settings.interface")).toBeInTheDocument());

    // Simulate a dirty config editor the way SectionEditor's own $effect
    // does — through the same global store, not a component internal.
    const rollback = vi.fn();
    dirty.set("config:north.mqtt", true, rollback);

    await fireEvent.click(screen.getAllByText("settings.tab.mqtt")[0]);
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledTimes(1));

    // Cancelled: still on General, nothing rolled back.
    expect(screen.getByText("settings.interface")).toBeInTheDocument();
    expect(screen.queryByText("settings.mqtt.reload_title")).toBeNull();
    expect(rollback).not.toHaveBeenCalled();
  });

  it("switches and rolls back the dirty editor once the operator confirms", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    render(Settings);
    await waitFor(() => expect(screen.getByText("settings.interface")).toBeInTheDocument());

    const rollback = vi.fn();
    dirty.set("config:north.mqtt", true, rollback);

    await fireEvent.click(screen.getAllByText("settings.tab.mqtt")[0]);

    await waitFor(() =>
      expect(screen.getByText("settings.mqtt.reload_title")).toBeInTheDocument(),
    );
    expect(rollback).toHaveBeenCalledTimes(1);
    expect(dirty.any()).toBe(false);
  });
});
