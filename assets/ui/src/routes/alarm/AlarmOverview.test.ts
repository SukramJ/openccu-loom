// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, within } from "@testing-library/svelte";
import type { AlarmAreaStatus, AlarmModeReadiness } from "$lib/api/types";

// alarmPanelStore is a module-level singleton (mirrors DeviceList.test.ts'
// deviceStore mock): stub the whole module so the view's control verbs are
// observable spies instead of live network calls.
let mockAreas: AlarmAreaStatus[] = [];
let mockReadiness: Record<string, Record<string, AlarmModeReadiness>> = {};
let mockCountdowns: Record<
  string,
  { kind: "exit_delay" | "entry_delay"; remaining_s: number; total_s: number }
> = {};
let mockJournal: { area_id: string; class: string; when: string; actor?: string }[] = [];
// areasConfig backs codeRequired()'s config.code_policy lookup — distinct
// from mockAreas above, which is the live status array.
let mockAreasConfig: { id: string; name: string; config?: unknown }[] = [];

const mockArm = vi.fn();
const mockDisarm = vi.fn();
const mockSilence = vi.fn();
const mockSilenceAll = vi.fn();
const mockAcknowledge = vi.fn();

vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get areas() {
      return mockAreas;
    },
    get areasConfig() {
      return mockAreasConfig;
    },
    get readiness() {
      return mockReadiness;
    },
    get countdowns() {
      return mockCountdowns;
    },
    get journal() {
      return mockJournal;
    },
    get health() {
      return { healthy: true, note: "" };
    },
    get loading() {
      return false;
    },
    get error() {
      return null;
    },
    arm: (...args: unknown[]) => mockArm(...args),
    disarm: (...args: unknown[]) => mockDisarm(...args),
    silence: (...args: unknown[]) => mockSilence(...args),
    silenceAll: (...args: unknown[]) => mockSilenceAll(...args),
    acknowledge: (...args: unknown[]) => mockAcknowledge(...args),
  },
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

import AlarmOverview from "./AlarmOverview.svelte";

function area(overrides: Partial<AlarmAreaStatus> = {}): AlarmAreaStatus {
  return {
    id: "area-1",
    name: "Ground floor",
    state: "disarmed",
    walktest_active: false,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockAreas = [];
  mockAreasConfig = [];
  mockReadiness = {};
  mockCountdowns = {};
  mockJournal = [];
  mockArm.mockResolvedValue({ state: "armed" });
  mockDisarm.mockResolvedValue(true);
  mockSilence.mockResolvedValue(true);
  mockSilenceAll.mockResolvedValue(true);
  mockAcknowledge.mockResolvedValue(true);
});

afterEach(() => {
  cleanup();
});

describe("AlarmOverview — area card", () => {
  it("renders one card per area with a state badge and mode buttons", () => {
    mockAreas = [area()];
    const { getByText, getByRole } = render(AlarmOverview);

    expect(getByText("Ground floor")).toBeTruthy();
    expect(getByText("alarm.state.disarmed")).toBeTruthy();
    // Fallback mode set (no readiness snapshot yet): disarmed + perimeter + full.
    expect(getByRole("button", { name: /alarm.mode.disarmed/ })).toBeTruthy();
    expect(getByRole("button", { name: /alarm.mode.perimeter/ })).toBeTruthy();
    expect(getByRole("button", { name: /alarm.mode.full/ })).toBeTruthy();
  });
});

describe("AlarmOverview — triggered variant", () => {
  it("shows SILENCE + DISARM and silence fires the store verb on the first tap, no confirm", async () => {
    mockAreas = [
      area({
        state: "triggered",
        mode: "full",
        incident: { id: "42", silenced: false },
      }),
    ];
    const { getByText, getByRole } = render(AlarmOverview);

    expect(getByText("alarm.triggered.intrusion")).toBeTruthy();
    const silenceBtn = getByRole("button", { name: /alarm.action.silence/ });
    const disarmBtn = getByRole("button", { name: /alarm.action.disarm/ });
    expect(silenceBtn).toBeTruthy();
    expect(disarmBtn).toBeTruthy();

    await fireEvent.click(silenceBtn);

    // The store verb fires directly from the click handler — no awaited
    // confirmStore.ask() gate sits between the tap and the call (S3/S6).
    expect(mockSilence).toHaveBeenCalledTimes(1);
    expect(mockSilence).toHaveBeenCalledWith("area-1");
    expect(mockDisarm).not.toHaveBeenCalled();
  });
});

describe("AlarmOverview — bypass sheet", () => {
  it("opens on a blocked mode click and force-arm passes the bypass list", async () => {
    mockAreas = [area()];
    mockReadiness = {
      "area-1": {
        perimeter: { ready: false, blockers: ["sensor-a", "sensor-b"] },
        full: { ready: true },
      },
    };
    const { getByRole, getByText } = render(AlarmOverview);

    await fireEvent.click(getByRole("button", { name: /alarm.mode.perimeter/ }));

    expect(getByText("alarm.bypass.title")).toBeTruthy();
    expect(getByText("sensor-a")).toBeTruthy();
    expect(getByText("sensor-b")).toBeTruthy();

    await fireEvent.click(getByRole("button", { name: "alarm.bypass.force_arm" }));

    expect(mockArm).toHaveBeenCalledWith("area-1", {
      mode: "perimeter",
      force: true,
      bypass: ["sensor-a", "sensor-b"],
    });
  });
});

describe("AlarmOverview — countdown ring", () => {
  it("renders a countdown ring when the area has a running countdown", () => {
    mockAreas = [area({ state: "arming", mode: "full" })];
    mockCountdowns = {
      "area-1": { kind: "exit_delay", remaining_s: 12, total_s: 30 },
    };
    const { getByRole } = render(AlarmOverview);

    const ring = getByRole("timer");
    expect(ring).toBeTruthy();
    expect(within(ring).getByText("12")).toBeTruthy();
  });

  it("renders no countdown ring when the area has none", () => {
    mockAreas = [area()];
    const { queryByRole } = render(AlarmOverview);
    expect(queryByRole("timer")).toBeNull();
  });
});

// docs/alarm-concept.md §11: the PIN pad gates a verb only when the area's
// own code policy explicitly opts it in (codeRequired() reads
// config.code_policy.require_arm / require_disarm from areasConfig, not
// from the live status snapshot). Silence is deliberately wired past this
// check entirely (S3) — a screaming siren must never wait on a PIN.
describe("AlarmOverview — PIN-pad flow", () => {
  function areaConfig(id: string, config: Record<string, unknown> = {}) {
    return { id, name: "Ground floor", config };
  }

  it("disarm opens the PIN pad when the policy requires a disarm code, and only disarms once a code is submitted", async () => {
    mockAreas = [area({ state: "armed", mode: "full" })];
    mockAreasConfig = [areaConfig("area-1", { code_policy: { require_disarm: true } })];
    const { getByRole, getByText, queryByRole } = render(AlarmOverview);

    await fireEvent.click(getByRole("button", { name: /alarm.mode.disarmed/ }));

    // Disarm does not fire on the tap itself — the pad opens first.
    expect(mockDisarm).not.toHaveBeenCalled();
    const dialog = getByRole("dialog", { name: "alarm.pinpad.disarm_title" });
    expect(dialog).toBeTruthy();

    await fireEvent.click(getByText("4", { selector: "button" }));
    await fireEvent.click(getByText("2", { selector: "button" }));
    await fireEvent.click(getByRole("button", { name: "alarm.action.disarm" }));

    expect(mockDisarm).toHaveBeenCalledWith("area-1", "42");
    expect(queryByRole("dialog")).toBeNull();
  });

  it("disarm skips the PIN pad entirely when the area's policy does not require a code", async () => {
    mockAreas = [area({ state: "armed", mode: "full" })];
    mockAreasConfig = [areaConfig("area-1")]; // no code_policy at all
    const { getByRole, queryByRole } = render(AlarmOverview);

    await fireEvent.click(getByRole("button", { name: /alarm.mode.disarmed/ }));

    expect(queryByRole("dialog")).toBeNull();
    expect(mockDisarm).toHaveBeenCalledWith("area-1");
  });

  it("silence NEVER shows the PIN pad, even when the same area requires codes for both arm and disarm", async () => {
    mockAreas = [
      area({
        state: "triggered",
        mode: "full",
        incident: { id: "42", silenced: false },
      }),
    ];
    mockAreasConfig = [
      areaConfig("area-1", { code_policy: { require_arm: true, require_disarm: true } }),
    ];
    const { getByRole, queryByRole } = render(AlarmOverview);

    await fireEvent.click(getByRole("button", { name: /alarm.action.silence/ }));

    expect(mockSilence).toHaveBeenCalledWith("area-1");
    expect(mockDisarm).not.toHaveBeenCalled();
    // No PIN pad ever mounts for silence, regardless of the area's policy.
    expect(queryByRole("dialog")).toBeNull();
  });
});
