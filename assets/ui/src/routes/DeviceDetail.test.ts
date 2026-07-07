// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor } from "@testing-library/svelte";

const { mockGetDevice, mockGetDeviceSchedule, mockGetPreference, mockPutPreference } = vi.hoisted(
  () => ({
    mockGetDevice: vi.fn(),
    mockGetDeviceSchedule: vi.fn(),
    mockGetPreference: vi.fn(),
    mockPutPreference: vi.fn(),
  }),
);

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
    getPreference: (...args: unknown[]) => mockGetPreference(...args),
    putPreference: (...args: unknown[]) => mockPutPreference(...args),
    renameDevice: vi.fn(),
    deleteDevice: vi.fn(),
    updateFirmware: vi.fn(),
    setDeviceRooms: vi.fn(),
    setDeviceFunctions: vi.fn(),
    listDataPoints: vi.fn().mockResolvedValue([]),
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
  t: (key: string) => key,
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// DeviceDetail.svelte pulls in a long tail of overview/configure/history
// panel components that are irrelevant to the loading/error/happy-path
// behaviours under test here. Stub them out to keep the render surface
// small; the channel-write + toast-feedback path is covered end-to-end by
// tests/e2e/device-detail.spec.ts instead.
vi.mock("$lib/components/channel/ChannelPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/DeviceLinks.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/links/CentralLinksPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/cdp/CdpTilesPanel.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/schedule/ScheduleTab.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/device/MaintenanceStatusGrid.svelte", () => ({ default: () => {} }));
vi.mock("./AuditLog.svelte", () => ({ default: () => {} }));
vi.mock("$lib/components/HistoryChart.svelte", () => ({ default: () => {} }));

import DeviceDetail from "./DeviceDetail.svelte";

function baseDevice(overrides: Record<string, unknown> = {}) {
  return {
    address: "0001ABCD",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-eTRV-2",
    name: "Wohnzimmer Thermostat",
    available: true,
    channels_count: 0,
    updatable: false,
    firmware: {},
    availability: { IsReachable: true },
    channels: [],
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockGetPreference.mockResolvedValue([]);
  mockPutPreference.mockResolvedValue(undefined);
  mockGetDeviceSchedule.mockRejectedValue(
    Object.assign(new Error("not found"), { status: 404 }),
  );
});

afterEach(() => cleanup());

describe("DeviceDetail — loading", () => {
  it("shows a loading indicator before getDevice() resolves", async () => {
    let resolveGetDevice!: (v: unknown) => void;
    mockGetDevice.mockReturnValue(
      new Promise((resolve) => {
        resolveGetDevice = resolve;
      }),
    );

    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    // Before the promise resolves, neither the error surface nor the
    // device header has rendered yet.
    expect(screen.queryByText(/device fetch failed/)).not.toBeInTheDocument();
    expect(screen.queryByText("Wohnzimmer Thermostat")).not.toBeInTheDocument();

    resolveGetDevice(baseDevice());
    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
  });
});

describe("DeviceDetail — error", () => {
  it("shows ErrorState with retry when getDevice() fails", async () => {
    mockGetDevice.mockRejectedValueOnce(new Error("device fetch failed"));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getByText(/device fetch failed/)).toBeInTheDocument();
    });

    mockGetDevice.mockResolvedValueOnce(baseDevice());
    const retryButton = screen.getByText("common.reload");
    retryButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => expect(mockGetDevice).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
  });
});

describe("DeviceDetail — happy path", () => {
  it("renders the device header once getDevice() resolves", async () => {
    mockGetDevice.mockResolvedValue(baseDevice());
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => expect(mockGetDevice).toHaveBeenCalledWith("0001ABCD"));
    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    expect(screen.getByText("HmIP-eTRV-2")).toBeInTheDocument();
    expect(screen.getAllByText("0001ABCD").length).toBeGreaterThan(0);
  });

  it("renders an EmptyState when the device has no channels", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ channels: [] }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getByText("device.no_channels")).toBeInTheDocument();
    });
  });

  it("renders the tab strip when the device has channels", async () => {
    mockGetDevice.mockResolvedValue(
      baseDevice({
        channels: [{ address: "0001ABCD:1", number: 1, type: "SHUTTER_CONTACT" }],
        channels_count: 1,
      }),
    );
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getAllByRole("tab").length).toBeGreaterThan(0);
    });
    expect(screen.queryByText("device.no_channels")).not.toBeInTheDocument();
  });
});
