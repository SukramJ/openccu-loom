// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import type { AlarmZoneStatus, AlarmWalkTestStatus } from "$lib/api/types";

let mockZonesConfig: { id: string; name: string }[] = [];
let mockZones: AlarmZoneStatus[] = [];
let mockWalktest: Record<string, { seen: number; total: number }> = {};

vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return mockZonesConfig;
    },
    get zones() {
      return mockZones;
    },
    get walktest() {
      return mockWalktest;
    },
  },
}));

const mockGetStatus = vi.fn();
const mockStart = vi.fn();
const mockStop = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    getAlarmWalkTestStatus: (...args: unknown[]) => mockGetStatus(...args),
    startAlarmWalkTest: (...args: unknown[]) => mockStart(...args),
    stopAlarmWalkTest: (...args: unknown[]) => mockStop(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmWalkTest from "./AlarmWalkTest.svelte";

function status(overrides: Partial<AlarmWalkTestStatus> = {}): AlarmWalkTestStatus {
  return {
    active: true,
    sensors: [
      { id: "s1", name: "Front door", tested: false },
      { id: "s2", name: "Hallway motion", tested: false },
    ],
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockZonesConfig = [{ id: "zone-1", name: "Ground floor" }];
  mockZones = [];
  mockWalktest = {};
});

afterEach(() => {
  cleanup();
});

describe("AlarmWalkTest — checklist", () => {
  it("ticks a row green once the sensor has been seen", async () => {
    mockGetStatus.mockResolvedValueOnce(
      status({
        sensors: [
          // No last_triggered_at: the "tested" cell would otherwise append a
          // "· <time>" suffix, breaking an exact-text match on the plain key.
          { id: "s1", name: "Front door", tested: true },
          { id: "s2", name: "Hallway motion", tested: false },
        ],
      }),
    );
    const { findByText, getByText } = render(AlarmWalkTest);

    await findByText("Front door");

    expect(getByText("alarm.walktest.tested")).toBeTruthy();
    expect(getByText("alarm.walktest.untested")).toBeTruthy();
    // The tested row's name cell renders with the "font-medium" emphasis
    // class; the untested row does not — that's the visible "tick".
    expect(getByText("Front door").className).toContain("font-medium");
    expect(getByText("Hallway motion").className).not.toContain("font-medium");
  });

  it("shows the live seen/total progress from the store's walktest counter", async () => {
    mockGetStatus.mockResolvedValue(status());
    mockWalktest = { "zone-1": { seen: 1, total: 2 } };
    const { container, findByText } = render(AlarmWalkTest);

    await findByText("Front door");

    expect(container.textContent?.includes("alarm.walktest.progress")).toBe(true);
  });
});
