// @vitest-environment happy-dom
//
// The enrolled-output roster is fetched per zone but saved against whatever
// zone the selector points at when Save is pressed, and the PUT replaces the
// whole set. Switching zones must therefore drop the previous zone's roster
// immediately and discard a response that arrives after the operator has
// already moved on — otherwise zone A's sirens and alarm lights are written
// into zone B, unenrolling everything B actually sounds with.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen, within } from "@testing-library/svelte";
import type { AlarmZone, AlarmOutput, DeviceSummary } from "$lib/api/types";

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

const mockListAlarmZoneOutputs = vi.fn();
const mockPutAlarmZoneOutputs = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmZoneOutputs: (...args: unknown[]) => mockListAlarmZoneOutputs(...args),
    putAlarmZoneOutputs: (...args: unknown[]) => mockPutAlarmZoneOutputs(...args),
    listAlarmOutputCandidates: vi.fn().mockResolvedValue([]),
    listSysvars: vi.fn().mockResolvedValue([]),
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

import AlarmOutputs from "./AlarmOutputs.svelte";

function output(id: string, name: string): AlarmOutput {
  return {
    id,
    class: "acoustic_siren",
    central: "ccu1",
    channel_address: `${id}:1`,
    name,
    config: { modes: ["full"], policy: "loud", duration_s: 180 },
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
  mockPutAlarmZoneOutputs.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("AlarmOutputs — zone switch", () => {
  it("discards a roster that arrives after the operator moved to another zone", async () => {
    const zoneA = deferred<AlarmOutput[]>();
    const zoneB = deferred<AlarmOutput[]>();
    mockListAlarmZoneOutputs.mockImplementation((id: string) =>
      id === "zone-a" ? zoneA.promise : zoneB.promise,
    );

    render(AlarmOutputs);
    await waitFor(() => expect(mockListAlarmZoneOutputs).toHaveBeenCalledWith("zone-a"));

    await pickZone("Upper floor");
    await waitFor(() => expect(mockListAlarmZoneOutputs).toHaveBeenCalledWith("zone-b"));

    zoneB.resolve([output("b1", "Upper siren")]);
    await waitFor(() => expect(screen.getByText("Upper siren")).toBeInTheDocument());

    // The zone the operator left answers late — it must not land.
    zoneA.resolve([output("a1", "Ground siren")]);
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByText("Upper siren")).toBeInTheDocument();
    expect(screen.queryByText("Ground siren")).not.toBeInTheDocument();
  });

  it("drops the previous zone's edited roster instead of offering it for save", async () => {
    const zoneB = deferred<AlarmOutput[]>();
    mockListAlarmZoneOutputs.mockImplementation((id: string) =>
      id === "zone-a" ? Promise.resolve([output("a1", "Ground siren")]) : zoneB.promise,
    );

    render(AlarmOutputs);
    await waitFor(() => expect(screen.getByText("Ground siren")).toBeInTheDocument());

    // Edit one output so the Save bar appears.
    await fireEvent.click(screen.getAllByText("alarm.mode.night")[0]);
    await waitFor(() => expect(screen.getByText("common.modified")).toBeInTheDocument());

    await pickZone("Upper floor");
    await waitFor(() => expect(mockListAlarmZoneOutputs).toHaveBeenCalledWith("zone-b"));

    // While zone B is still loading, neither its predecessor's roster nor the
    // Save bar that would write it to zone B may remain on screen.
    expect(screen.queryByText("Ground siren")).not.toBeInTheDocument();
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();

    zoneB.resolve([output("b1", "Upper siren")]);
    await waitFor(() => expect(screen.getByText("Upper siren")).toBeInTheDocument());
    expect(screen.queryByText("common.modified")).not.toBeInTheDocument();
    expect(mockPutAlarmZoneOutputs).not.toHaveBeenCalled();
  });

  it("keeps the unsaved roster when the operator declines the discard prompt", async () => {
    mockListAlarmZoneOutputs.mockImplementation((id: string) =>
      Promise.resolve(id === "zone-a" ? [output("a1", "Ground siren")] : [output("b1", "Upper siren")]),
    );
    mockConfirmAsk.mockResolvedValue(false);

    render(AlarmOutputs);
    await waitFor(() => expect(screen.getByText("Ground siren")).toBeInTheDocument());

    await fireEvent.click(screen.getAllByText("alarm.mode.night")[0]);
    await waitFor(() => expect(screen.getByText("common.modified")).toBeInTheDocument());

    await pickZone("Upper floor");
    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalledOnce());

    expect(mockListAlarmZoneOutputs).not.toHaveBeenCalledWith("zone-b");
    expect(screen.getByText("Ground siren")).toBeInTheDocument();
    expect(screen.getByText("common.modified")).toBeInTheDocument();
  });
});
