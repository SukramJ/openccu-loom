// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";
import type { AlarmArea, AlarmOutput, DeviceSummary } from "$lib/api/types";

let mockAreasConfig: AlarmArea[] = [];
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get areasConfig() {
      return mockAreasConfig;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

let mockDevices: DeviceSummary[] = [];
vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    get items() {
      return mockDevices;
    },
    refresh: vi.fn(),
    ensureStream: vi.fn(),
  },
}));

const mockListAlarmAreaOutputs = vi.fn();
const mockPutAlarmAreaOutputs = vi.fn();
const mockTestAlarmOutput = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmAreaOutputs: (...args: unknown[]) => mockListAlarmAreaOutputs(...args),
    putAlarmAreaOutputs: (...args: unknown[]) => mockPutAlarmAreaOutputs(...args),
    testAlarmOutput: (...args: unknown[]) => mockTestAlarmOutput(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmOutputs from "./AlarmOutputs.svelte";

function output(overrides: Partial<AlarmOutput> = {}): AlarmOutput {
  return {
    id: "o1",
    class: "acoustic_siren",
    central: "ccu1",
    channel_address: "SIR001:1",
    name: "Hallway siren",
    config: { modes: ["full"], policy: "loud", duration_s: 180 },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockAreasConfig = [{ id: "area-1", name: "Ground floor" }];
  mockDevices = [];
  mockListAlarmAreaOutputs.mockResolvedValue([output()]);
  mockPutAlarmAreaOutputs.mockResolvedValue(undefined);
  mockTestAlarmOutput.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(false);
});

afterEach(() => {
  cleanup();
});

describe("AlarmOutputs — smoke-sounder caveat", () => {
  it("shows the smoke caveat copy only for smoke_sounder outputs", async () => {
    mockListAlarmAreaOutputs.mockResolvedValueOnce([
      output({ id: "o1", class: "smoke_sounder", name: "Kitchen smoke sounder" }),
    ]);
    const { findByText } = render(AlarmOutputs);

    expect(await findByText("Kitchen smoke sounder")).toBeTruthy();
    expect(await findByText("alarm.outputs.smoke_caveat")).toBeTruthy();
  });

  it("does not show the smoke caveat for a plain acoustic siren", async () => {
    mockListAlarmAreaOutputs.mockResolvedValueOnce([output({ class: "acoustic_siren" })]);
    const { findByText, queryByText } = render(AlarmOutputs);

    await findByText("Hallway siren");
    expect(queryByText("alarm.outputs.smoke_caveat")).toBeNull();
  });
});

describe("AlarmOutputs — test fire", () => {
  it("asks for confirmation before firing and skips the call when declined", async () => {
    mockListAlarmAreaOutputs.mockResolvedValueOnce([output()]);
    mockConfirmAsk.mockResolvedValueOnce(false);
    const { findByText, getByRole } = render(AlarmOutputs);
    await findByText("Hallway siren");

    await fireEvent.click(getByRole("button", { name: /alarm.outputs.test/ }));

    expect(mockConfirmAsk).toHaveBeenCalledOnce();
    expect(mockTestAlarmOutput).not.toHaveBeenCalled();
  });

  it("fires the output once confirmation is accepted", async () => {
    mockListAlarmAreaOutputs.mockResolvedValueOnce([output()]);
    mockConfirmAsk.mockResolvedValueOnce(true);
    const { findByText, getByRole } = render(AlarmOutputs);
    await findByText("Hallway siren");

    await fireEvent.click(getByRole("button", { name: /alarm.outputs.test/ }));

    expect(mockConfirmAsk).toHaveBeenCalledOnce();
    expect(mockTestAlarmOutput).toHaveBeenCalledWith("o1", { optical_only: false });
  });
});
