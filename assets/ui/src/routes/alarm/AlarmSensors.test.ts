// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";
import type { AlarmArea, AlarmSensor, DeviceSummary } from "$lib/api/types";

let mockAreasConfig: AlarmArea[] = [];
let mockAreasLoading = false;
let mockAreasError: string | null = null;
const mockAreasRefresh = vi.fn().mockResolvedValue(undefined);
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get areasConfig() {
      return mockAreasConfig;
    },
    get loading() {
      return mockAreasLoading;
    },
    get error() {
      return mockAreasError;
    },
    refresh: (...args: unknown[]) => mockAreasRefresh(...args),
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

const mockListAlarmAreaSensors = vi.fn();
const mockPutAlarmAreaSensors = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmAreaSensors: (...args: unknown[]) => mockListAlarmAreaSensors(...args),
    putAlarmAreaSensors: (...args: unknown[]) => mockPutAlarmAreaSensors(...args),
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
  mockAreasConfig = [{ id: "area-1", name: "Ground floor" }];
  mockAreasLoading = false;
  mockAreasError = null;
  mockDevices = [];
  mockListAlarmAreaSensors.mockResolvedValue([sensor()]);
  mockPutAlarmAreaSensors.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
});

describe("AlarmSensors — areas-loading gate", () => {
  it("shows the shared loading state while the first areas load is in flight", () => {
    mockAreasLoading = true;
    mockAreasConfig = [];
    const { getByRole, queryByText } = render(AlarmSensors);

    expect(getByRole("status")).toBeTruthy();
    // The gate short-circuits before the toolbar/area-selector ever mounts.
    expect(queryByText("alarm.sensors.area")).toBeNull();
  });

  it("shows the shared error state on a failed areas refresh, and retry re-triggers the store refresh", async () => {
    mockAreasLoading = false;
    mockAreasError = "network unreachable";
    mockAreasConfig = [];
    const { getByRole, container } = render(AlarmSensors);

    expect(container.textContent).toContain("network unreachable");

    await fireEvent.click(getByRole("button", { name: "common.reload" }));
    expect(mockAreasRefresh).toHaveBeenCalledOnce();
  });

  it("shows the empty state with a wizard link on a successful load with no areas", () => {
    mockAreasLoading = false;
    mockAreasError = null;
    mockAreasConfig = [];
    const { getByText, getByRole } = render(AlarmSensors);

    expect(getByText("alarm.overview.empty")).toBeTruthy();
    const link = getByRole("link", { name: /alarm.wizard.launch/ });
    expect(link).toHaveAttribute("href", "#/alarm/wizard");
  });

  it("renders the sensor picker UI once areas are present", async () => {
    mockAreasLoading = false;
    mockAreasError = null;
    mockAreasConfig = [{ id: "area-1", name: "Ground floor" }];
    const { findByText, getByRole } = render(AlarmSensors);

    // The toolbar (area selector + add button) is part of the picker, not
    // the gate, so its presence marks a successful pass through the gate.
    // The area <Select> sits inside a <label>, which takes over the
    // trigger's accessible name — so the label key, not the selected area
    // name, is what a screen reader (and this query) sees.
    expect(await findByText("alarm.sensors.area")).toBeTruthy();
    expect(getByRole("button", { name: "alarm.sensors.area" })).toBeTruthy();
    expect(getByRole("button", { name: /alarm.sensors.add/ })).toBeTruthy();
  });
});

describe("AlarmSensors — card grid + matrix toggle", () => {
  it("renders one card per enrolled sensor", async () => {
    mockListAlarmAreaSensors.mockResolvedValueOnce([
      sensor({ id: "s1", name: "Front door" }),
      sensor({ id: "s2", name: "Hallway motion", type: "motion" }),
    ]);
    const { findByText } = render(AlarmSensors);

    expect(await findByText("Front door")).toBeTruthy();
    expect(await findByText("Hallway motion")).toBeTruthy();
  });

  it("switches to the dense matrix table when the matrix toggle is clicked", async () => {
    mockListAlarmAreaSensors.mockResolvedValueOnce([sensor()]);
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
    mockListAlarmAreaSensors.mockResolvedValueOnce([sensor({ name: "Front door" })]);
    const { findByText, getByRole } = render(AlarmSensors);

    const nameButton = await findByText("Front door");
    await fireEvent.click(nameButton);

    const drawer = getByRole("dialog", { name: "alarm.sensors.detail.title" });
    expect(drawer).toBeTruthy();
    // The drawer header repeats the sensor's channel/parameter coordinates.
    expect(drawer.textContent).toContain("ABC123:1");
  });
});
