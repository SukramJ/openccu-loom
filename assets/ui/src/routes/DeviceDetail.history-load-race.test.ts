// @vitest-environment happy-dom
//
// Pins the `historyDPsGeneration` guard in DeviceDetail.svelte's
// `loadHistoryDPs()`: a slower response for a channel the operator has
// already switched away from in the History tab's channel picker must
// never overwrite the newer channel's data points.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent, within } from "@testing-library/svelte";

const { mockGetDevice, mockListDataPoints } = vi.hoisted(() => ({
  mockGetDevice: vi.fn(),
  mockListDataPoints: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    getDeviceSchedule: vi.fn().mockRejectedValue(
      Object.assign(new Error("not found"), { status: 404 }),
    ),
    getPreference: vi.fn().mockResolvedValue([]),
    putPreference: vi.fn().mockResolvedValue(undefined),
    listRooms: vi.fn().mockResolvedValue([]),
    listFunctions: vi.fn().mockResolvedValue([]),
    listLinks: vi.fn().mockResolvedValue([]),
    listPrograms: vi.fn().mockResolvedValue([]),
    listDataPoints: (...args: unknown[]) => mockListDataPoints(...args),
  },
  setUnauthorizedHandler: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte). Swap in a plain always-open stub so
// the channel picker interaction below is deterministic.
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("./__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

// Same reasoning as DeviceDetail.test.ts: keep the render surface to just
// the History tab's channel/parameter pickers under test.
vi.mock("$lib/components/channel/ChannelPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/DeviceLinks.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/CentralLinksPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/cdp/CdpTilesPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/schedule/ScheduleTab.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/device/MaintenanceStatusGrid.svelte", () => ({ default: () => {} }));
vi.mock("./AuditLog.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/HistoryChart.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/RecordToggle.svelte", () => ({ default: () => {} }));

import DeviceDetail from "./DeviceDetail.svelte";

function device() {
  return {
    address: "0001ABCD",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    central: "ccu1",
    model: "HmIP-eTRV-2",
    name: "Wohnzimmer Thermostat",
    available: true,
    channels_count: 2,
    updatable: false,
    firmware: {},
    availability: { IsReachable: true },
    channels: [
      { address: "0001ABCD:0", number: 0, type: "MAINTENANCE" },
      { address: "0001ABCD:1", number: 1, type: "SWITCH", name: "Kanal 1" },
      { address: "0001ABCD:2", number: 2, type: "SWITCH", name: "Kanal 2" },
    ],
  };
}

function dp(param: string) {
  return { parameter: param, type: "FLOAT", parameter_label: param, unit: "" };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetDevice.mockResolvedValue(device());
});

afterEach(() => cleanup());

// Picks `optionLabel` from the channel-picker SelectStub (the first
// listbox on the page — the History tab only renders a parameter picker
// alongside it once data points have arrived).
async function pickChannel(optionLabel: string) {
  const listbox = screen.getAllByRole("listbox")[0];
  await fireEvent.click(within(listbox).getByText(optionLabel));
}

describe("DeviceDetail — history channel-switch load race", () => {
  it("keeps channel 2's data points even if channel 1's response resolves later", async () => {
    const chan1: { resolve?: (v: unknown) => void } = {};
    const chan2: { resolve?: (v: unknown) => void } = {};
    mockListDataPoints.mockImplementation((_addr: string, no: number) =>
      no === 1
        ? new Promise((resolve) => (chan1.resolve = resolve))
        : new Promise((resolve) => (chan2.resolve = resolve)),
    );

    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });
    await waitFor(() =>
      expect(document.querySelector("h1")?.textContent).toBe("Wohnzimmer Thermostat"),
    );

    await fireEvent.click(screen.getByRole("tab", { name: /device.toptab.history/ }));
    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalledWith("0001ABCD", 1));

    // Switch to channel 2 before channel 1's response ever arrives.
    await pickChannel("Kanal 2 (2)");
    await waitFor(() => expect(mockListDataPoints).toHaveBeenCalledWith("0001ABCD", 2));

    // Channel 2's (later-started) request resolves first.
    chan2.resolve?.([dp("ACTUAL_TEMPERATURE")]);
    await screen.findByText(/ACTUAL_TEMPERATURE/);

    // The stale channel-1 response now arrives late. It must be discarded.
    chan1.resolve?.([dp("SET_POINT_TEMPERATURE")]);
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByText(/ACTUAL_TEMPERATURE/)).toBeInTheDocument();
    expect(screen.queryByText(/SET_POINT_TEMPERATURE/)).not.toBeInTheDocument();
  });
});
