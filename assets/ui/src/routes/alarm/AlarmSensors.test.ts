// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";
import type { AlarmZone, AlarmSensor, DeviceSummary } from "$lib/api/types";

let mockZonesConfig: AlarmZone[] = [];
let mockZonesLoading = false;
let mockZonesError: string | null = null;
const mockZonesRefresh = vi.fn().mockResolvedValue(undefined);
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return mockZonesConfig;
    },
    get loading() {
      return mockZonesLoading;
    },
    get error() {
      return mockZonesError;
    },
    refresh: (...args: unknown[]) => mockZonesRefresh(...args),
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

const mockListAlarmZoneSensors = vi.fn();
const mockPutAlarmZoneSensors = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmZoneSensors: (...args: unknown[]) => mockListAlarmZoneSensors(...args),
    putAlarmZoneSensors: (...args: unknown[]) => mockPutAlarmZoneSensors(...args),
    listRooms: vi.fn().mockResolvedValue([]),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmSensors from "./AlarmSensors.svelte";

function sensor(overrides: Partial<AlarmSensor> = {}): AlarmSensor {
  return {
    id: "s1",
    central: "ccu1",
    interface_id: "HmIP-RF",
    channel_address: "ABC123:1",
    parameter: "STATE",
    type: "door",
    name: "Front door",
    config: { modes: ["perimeter", "full"] },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockZonesConfig = [{ id: "zone-1", name: "Ground floor" }];
  mockZonesLoading = false;
  mockZonesError = null;
  mockDevices = [];
  mockListAlarmZoneSensors.mockResolvedValue([sensor()]);
  mockPutAlarmZoneSensors.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("AlarmSensors — zones-loading gate", () => {
  it("shows the shared loading state while the first zones load is in flight", () => {
    mockZonesLoading = true;
    mockZonesConfig = [];
    const { getByRole, queryByText } = render(AlarmSensors);

    expect(getByRole("status")).toBeTruthy();
    // The gate short-circuits before the toolbar/zone-selector ever mounts.
    expect(queryByText("alarm.sensors.zone")).toBeNull();
  });

  it("shows the shared error state on a failed zones refresh, and retry re-triggers the store refresh", async () => {
    mockZonesLoading = false;
    mockZonesError = "network unreachable";
    mockZonesConfig = [];
    const { getByRole, container } = render(AlarmSensors);

    expect(container.textContent).toContain("network unreachable");

    await fireEvent.click(getByRole("button", { name: "common.reload" }));
    expect(mockZonesRefresh).toHaveBeenCalledOnce();
  });

  it("shows the empty state with a wizard link on a successful load with no zones", () => {
    mockZonesLoading = false;
    mockZonesError = null;
    mockZonesConfig = [];
    const { getByText, getByRole } = render(AlarmSensors);

    expect(getByText("alarm.overview.empty")).toBeTruthy();
    const link = getByRole("link", { name: /alarm.wizard.launch/ });
    expect(link).toHaveAttribute("href", "#/alarm/wizard");
  });

  it("renders the sensor picker UI once zones are present", async () => {
    mockZonesLoading = false;
    mockZonesError = null;
    mockZonesConfig = [{ id: "zone-1", name: "Ground floor" }];
    const { findByText, getByRole } = render(AlarmSensors);

    // The toolbar (zone selector + add button) is part of the picker, not
    // the gate, so its presence marks a successful pass through the gate.
    // The zone <Select> sits inside a <label>, which takes over the
    // trigger's accessible name — so the label key, not the selected zone
    // name, is what a screen reader (and this query) sees.
    expect(await findByText("alarm.sensors.zone")).toBeTruthy();
    expect(getByRole("button", { name: "alarm.sensors.zone" })).toBeTruthy();
    expect(getByRole("button", { name: /alarm.sensors.add/ })).toBeTruthy();
  });
});

describe("AlarmSensors — card grid + matrix toggle", () => {
  it("renders one card per enrolled sensor", async () => {
    mockListAlarmZoneSensors.mockResolvedValueOnce([
      sensor({ id: "s1", name: "Front door" }),
      sensor({ id: "s2", name: "Hallway motion", type: "motion" }),
    ]);
    const { findByText } = render(AlarmSensors);

    expect(await findByText("Front door")).toBeTruthy();
    expect(await findByText("Hallway motion")).toBeTruthy();
  });

  it("switches to the dense matrix table when the matrix toggle is clicked", async () => {
    mockListAlarmZoneSensors.mockResolvedValueOnce([sensor()]);
    const { findByText, getByTitle, getByRole } = render(AlarmSensors);
    await findByText("Front door");

    await fireEvent.click(getByTitle("alarm.sensors.view.matrix"));

    // The matrix view renders a <table> with a sensor-name column header;
    // the card grid does not.
    expect(getByRole("table")).toBeTruthy();
    expect(getByRole("columnheader", { name: "alarm.matrix.sensor" })).toBeTruthy();
  });
});

describe("AlarmSensors — detail drawer", () => {
  it("opens the slide-over when a sensor name is clicked", async () => {
    mockListAlarmZoneSensors.mockResolvedValueOnce([sensor({ name: "Front door" })]);
    const { findByText, getByRole } = render(AlarmSensors);

    const nameButton = await findByText("Front door");
    await fireEvent.click(nameButton);

    const drawer = getByRole("dialog", { name: "alarm.sensors.detail.title" });
    expect(drawer).toBeTruthy();
    // The drawer header repeats the sensor's channel/parameter coordinates.
    expect(drawer.textContent).toContain("ABC123:1");
  });
});
