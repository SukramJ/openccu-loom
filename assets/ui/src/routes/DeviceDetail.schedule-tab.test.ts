// @vitest-environment happy-dom
//
// The Schedule sub-tab is the only entry in the configure strip that is
// decided asynchronously (a probe against GET /devices/<addr>/schedule),
// and the view stays mounted across a device switch. These tests pin the
// two ways that combination goes wrong: a probe result landing on the
// wrong device, and a selection surviving a device that has no such tab.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const { mockGetDevice, mockGetDeviceSchedule } = vi.hoisted(() => ({
  mockGetDevice: vi.fn(),
  mockGetDeviceSchedule: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
    getPreference: vi.fn().mockResolvedValue([]),
    putPreference: vi.fn().mockResolvedValue(undefined),
    listRooms: vi.fn().mockResolvedValue([]),
    listFunctions: vi.fn().mockResolvedValue([]),
    listLinks: vi.fn().mockResolvedValue([]),
    listPrograms: vi.fn().mockResolvedValue([]),
    listDataPoints: vi.fn().mockResolvedValue([]),
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

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), warn: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// Keep the render surface to the tab strip and the panel it selects.
vi.mock("$lib/components/channel/ChannelPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/DeviceLinks.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/CentralLinksPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/cdp/CdpTilesPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/device/MaintenanceStatusGrid.svelte", () => ({ default: () => {} }));
vi.mock("./AuditLog.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/HistoryChart.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/RecordToggle.svelte", () => ({ default: () => {} }));

// The schedule panel is the observable difference between "the tab is
// selected" and "the tab is merely offered", so it renders a marker
// instead of being stubbed out entirely.
vi.mock("$lib/components/schedule/ScheduleTab.svelte", async () => {
  const mod = await import("./__testutils__/ScheduleTabStub.svelte");
  return { default: mod.default };
});

import DeviceDetail from "./DeviceDetail.svelte";

function device(address: string, name: string) {
  return {
    address,
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    central: "ccu1",
    model: "HmIP-eTRV-2",
    name,
    available: true,
    channels_count: 1,
    updatable: false,
    firmware: {},
    availability: { IsReachable: true },
    channels: [
      { address: `${address}:0`, number: 0, type: "MAINTENANCE" },
      { address: `${address}:1`, number: 1, type: "HEATING_CLIMATECONTROL_TRANSCEIVER" },
    ],
  };
}

function notFound() {
  return Object.assign(new Error("no schedule"), { status: 404 });
}

async function openConfigure() {
  await fireEvent.click(screen.getByRole("tab", { name: "device.toptab.configure" }));
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetDevice.mockImplementation((addr: string) =>
    Promise.resolve(device(addr, addr === "0001ABCD" ? "Wohnzimmer" : "Küche")),
  );
});

afterEach(() => cleanup());

describe("DeviceDetail — schedule probe across a device switch", () => {
  it("keeps the Schedule tab of the device on screen when the previous probe 404s late", async () => {
    let rejectFirst!: (e: unknown) => void;
    mockGetDeviceSchedule.mockImplementation((addr: string) =>
      addr === "0001ABCD"
        ? new Promise((_resolve, reject) => (rejectFirst = reject))
        : Promise.resolve({ weekdays: {} }),
    );

    const { rerender } = render(DeviceDetail, {
      props: { address: "0001ABCD", locale: "en" },
    });
    await waitFor(() => expect(mockGetDeviceSchedule).toHaveBeenCalledWith("0001ABCD"));

    // Switch before the first probe answers; the second device has one.
    await rerender({ address: "0002BEEF", locale: "en" });
    await waitFor(() => expect(mockGetDeviceSchedule).toHaveBeenCalledWith("0002BEEF"));
    await openConfigure();
    await screen.findByRole("tab", { name: "device.subtab.schedule" });

    // The stale 404 now arrives. It must not un-offer the tab.
    rejectFirst(notFound());
    await new Promise((r) => setTimeout(r, 0));

    expect(
      screen.getByRole("tab", { name: "device.subtab.schedule" }),
    ).toBeInTheDocument();
  });
});

describe("DeviceDetail — configure sub-tab reset", () => {
  it("drops a deep-linked schedule tab when the next device has none", async () => {
    mockGetDeviceSchedule.mockImplementation((addr: string) =>
      addr === "0001ABCD" ? Promise.resolve({ weekdays: {} }) : Promise.reject(notFound()),
    );

    const { rerender } = render(DeviceDetail, {
      props: { address: "0001ABCD", sub: "schedule", locale: "en" },
    });
    await screen.findByTestId("schedule-tab");

    // Same view, next device, no `?tab=` in the hash.
    await rerender({ address: "0002BEEF", locale: "en" });

    await waitFor(() =>
      expect(
        screen.queryByRole("tab", { name: "device.subtab.schedule" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.queryByTestId("schedule-tab")).not.toBeInTheDocument();
  });

  it("returns to the default sub-tab when the route drops its tab parameter", async () => {
    mockGetDeviceSchedule.mockRejectedValue(notFound());

    const { rerender } = render(DeviceDetail, {
      props: { address: "0001ABCD", sub: "links", locale: "en" },
    });
    await waitFor(() =>
      expect(screen.getByRole("tab", { name: "device.subtab.links" })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );

    // App.svelte always passes `sub`, so a hash without `?tab=` arrives
    // as an explicit undefined.
    await rerender({ address: "0002BEEF", sub: undefined, locale: "en" });

    await waitFor(() =>
      expect(screen.getByRole("tab", { name: "device.subtab.channels" })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );
    expect(
      screen.getByRole("tab", { name: "device.subtab.links" }),
    ).toHaveAttribute("aria-selected", "false");
  });
});
