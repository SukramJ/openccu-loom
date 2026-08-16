// @vitest-environment happy-dom
//
// The enrolled-sensor roster is fetched per zone but saved against whatever
// zone the selector points at when Save is pressed. Switching zones must
// therefore drop the previous zone's roster immediately and discard a
// response that arrives after the operator has already moved on — otherwise
// zone A's detector set is written into zone B, replacing the membership its
// arm readiness and trigger evaluation are computed from.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen, within } from "@testing-library/svelte";
import type { AlarmZone, AlarmSensor, DeviceSummary } from "$lib/api/types";

let mockZonesConfig: AlarmZone[] = [];
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return mockZonesConfig;
    },
    get loading() {
      return false;
    },
    get error() {
      return null;
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

// Whole-module mock (not just $lib/api/client) so the real areas.svelte.ts
// never pulls in auth.svelte.ts's module-level setUnauthorizedHandler side
// effect, which this file's minimal api-client mock does not export.
vi.mock("$lib/stores/areas.svelte", () => ({
  areasStore: {
    get areas() {
      return [];
    },
    ensureLoaded: vi.fn(),
    areaIdOf: vi.fn(() => undefined),
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

const mockConfirmAsk = vi.fn();
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import AlarmSensors from "./AlarmSensors.svelte";

function sensor(id: string, name: string): AlarmSensor {
  return {
    id,
    central: "ccu1",
    interface_id: "HmIP-RF",
    channel_address: `${id}:1`,
    parameter: "STATE",
    type: "door",
    name,
    config: { modes: ["perimeter"] },
  };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

/** The zone selector is the first Select on the page. */
async function pickZone(label: string) {
  const zoneSelect = screen.getAllByRole("listbox")[0];
  await fireEvent.click(within(zoneSelect).getByText(label));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockZonesConfig = [
    { id: "zone-a", name: "Ground floor" },
    { id: "zone-b", name: "Upper floor" },
  ] as AlarmZone[];
  mockDevices = [];
  mockPutAlarmZoneSensors.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("AlarmSensors — zone switch", () => {
  it("discards a roster that arrives after the operator moved to another zone", async () => {
    const zoneA = deferred<AlarmSensor[]>();
    const zoneB = deferred<AlarmSensor[]>();
    mockListAlarmZoneSensors.mockImplementation((id: string) =>
      id === "zone-a" ? zoneA.promise : zoneB.promise,
    );

    render(AlarmSensors);
    await waitFor(() => expect(mockListAlarmZoneSensors).toHaveBeenCalledWith("zone-a"));

    await pickZone("Upper floor");
    await waitFor(() => expect(mockListAlarmZoneSensors).toHaveBeenCalledWith("zone-b"));

    zoneB.resolve([sensor("b1", "Upper door")]);
    await waitFor(() => expect(screen.getByText("Upper door")).toBeInTheDocument());

    // The zone the operator left answers late — it must not land.
    zoneA.resolve([sensor("a1", "Ground door")]);
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByText("Upper door")).toBeInTheDocument();
    expect(screen.queryByText("Ground door")).not.toBeInTheDocument();
  });

  it("drops the previous zone's edited roster instead of offering it for save", async () => {
    const zoneB = deferred<AlarmSensor[]>();
    mockListAlarmZoneSensors.mockImplementation((id: string) =>
      id === "zone-a" ? Promise.resolve([sensor("a1", "Ground door")]) : zoneB.promise,
    );

    render(AlarmSensors);
    await waitFor(() => expect(screen.getByText("Ground door")).toBeInTheDocument());

    // Edit one sensor so the Save bar appears.
    await fireEvent.click(screen.getAllByText("alarm.mode.night")[0]);
    await waitFor(() => expect(screen.getByText("common.modified")).toBeInTheDocument());

    await pickZone("Upper floor");
    await waitFor(() => expect(mockListAlarmZoneSensors).toHaveBeenCalledWith("zone-b"));

    // While zone B is still loading, neither its predecessor's roster nor the
    // Save bar that would write it to zone B may remain on screen.
    expect(screen.queryByText("Ground door")).not.toBeInTheDocument();
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();

    zoneB.resolve([sensor("b1", "Upper door")]);
    await waitFor(() => expect(screen.getByText("Upper door")).toBeInTheDocument());
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();
    expect(mockPutAlarmZoneSensors).not.toHaveBeenCalled();
  });

  it("keeps the unsaved roster when the operator declines the discard prompt", async () => {
    // Dropping the buffer is what stops a cross-zone save, but it also throws
    // away the operator's work — so the drop is their decision, not a silent
    // side effect of touching the selector.
    mockListAlarmZoneSensors.mockImplementation((id: string) =>
      Promise.resolve(id === "zone-a" ? [sensor("a1", "Ground door")] : [sensor("b1", "Upper door")]),
    );
    mockConfirmAsk.mockResolvedValue(false);

    render(AlarmSensors);
    await waitFor(() => expect(screen.getByText("Ground door")).toBeInTheDocument());

    await fireEvent.click(screen.getAllByText("alarm.mode.night")[0]);
    await waitFor(() => expect(screen.getByText("common.modified")).toBeInTheDocument());

    await pickZone("Upper floor");
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledOnce());

    expect(mockListAlarmZoneSensors).not.toHaveBeenCalledWith("zone-b");
    expect(screen.getByText("Ground door")).toBeInTheDocument();
    expect(screen.getByText("common.modified")).toBeInTheDocument();
  });

  it("does not prompt when there is nothing unsaved to lose", async () => {
    mockListAlarmZoneSensors.mockImplementation((id: string) =>
      Promise.resolve(id === "zone-a" ? [sensor("a1", "Ground door")] : [sensor("b1", "Upper door")]),
    );

    render(AlarmSensors);
    await waitFor(() => expect(screen.getByText("Ground door")).toBeInTheDocument());

    await pickZone("Upper floor");
    await waitFor(() => expect(screen.getByText("Upper door")).toBeInTheDocument());
    expect(mockConfirmAsk).not.toHaveBeenCalled();
  });
});
