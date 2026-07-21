// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent } from "@testing-library/svelte";

const {
  mockGetDevice,
  mockGetDeviceSchedule,
  mockGetPreference,
  mockPutPreference,
  mockRenameDevice,
  mockRenameChannel,
  mockToastSuccess,
  mockToastError,
} = vi.hoisted(() => ({
  mockGetDevice: vi.fn(),
  mockGetDeviceSchedule: vi.fn(),
  mockGetPreference: vi.fn(),
  mockPutPreference: vi.fn(),
  mockRenameDevice: vi.fn(),
  mockRenameChannel: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
    getPreference: (...args: unknown[]) => mockGetPreference(...args),
    putPreference: (...args: unknown[]) => mockPutPreference(...args),
    renameDevice: (...args: unknown[]) => mockRenameDevice(...args),
    renameChannel: (...args: unknown[]) => mockRenameChannel(...args),
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
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
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
  mockRenameDevice.mockResolvedValue(undefined);
  mockRenameChannel.mockResolvedValue(undefined);
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

describe("DeviceDetail — device rename dialog", () => {
  async function openRenameDialog() {
    mockGetDevice.mockResolvedValue(baseDevice());
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });
    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.rename" }));
    await waitFor(() => {
      expect(screen.getByLabelText("device.rename")).toBeInTheDocument();
    });
  }

  it("opens with the include-channels switch checked by default and the name prefilled", async () => {
    await openRenameDialog();
    const input = screen.getByLabelText("device.rename") as HTMLInputElement;
    expect(input.value).toBe("Wohnzimmer Thermostat");
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true");
  });

  it("commits the rename with include_channels=true by default and reloads", async () => {
    await openRenameDialog();
    await fireEvent.input(screen.getByLabelText("device.rename"), {
      target: { value: "New Name" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(mockRenameDevice).toHaveBeenCalledWith("0001ABCD", "New Name", true);
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("device.renamed");
    await waitFor(() => expect(mockGetDevice).toHaveBeenCalledTimes(2));
  });

  it("forwards include_channels=false when the switch is toggled off", async () => {
    await openRenameDialog();
    await fireEvent.click(screen.getByRole("switch"));
    expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("false");

    await fireEvent.input(screen.getByLabelText("device.rename"), {
      target: { value: "New Name" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(mockRenameDevice).toHaveBeenCalledWith("0001ABCD", "New Name", false);
    });
  });

  it("shows an error toast and keeps the dialog open when the CCU rename fails", async () => {
    mockRenameDevice.mockRejectedValueOnce(new Error("ccu unreachable"));
    await openRenameDialog();
    await fireEvent.input(screen.getByLabelText("device.rename"), {
      target: { value: "New Name" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("ccu unreachable");
    });
    // Dialog stays open on failure — no silent fallback / no silent close.
    expect(screen.getByLabelText("device.rename")).toBeInTheDocument();
    expect(mockGetDevice).toHaveBeenCalledTimes(1);
  });

  it("closes without calling the API when the name is unchanged", async () => {
    await openRenameDialog();
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(screen.queryByLabelText("device.rename")).not.toBeInTheDocument();
    });
    expect(mockRenameDevice).not.toHaveBeenCalled();
  });
});

describe("DeviceDetail — channel rename", () => {
  function deviceWithChannel(overrides: Record<string, unknown> = {}) {
    return baseDevice({
      channels: [
        { address: "0001ABCD:1", number: 1, type: "SWITCH", name: "Kanal 1", data_points_count: 0 },
      ],
      channels_count: 1,
      ...overrides,
    });
  }

  async function openChannelEditor() {
    mockGetDevice.mockResolvedValue(deviceWithChannel());
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });
    await waitFor(() => {
      expect(screen.getAllByRole("tab").length).toBeGreaterThan(0);
    });
    await fireEvent.click(screen.getByRole("tab", { name: "device.toptab.configure" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "channel.rename" })).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "channel.rename" }));
    await waitFor(() => {
      expect(screen.getByLabelText("channel.rename")).toBeInTheDocument();
    });
  }

  it("opens an inline editor prefilled with the current channel name", async () => {
    await openChannelEditor();
    const input = screen.getByLabelText("channel.rename") as HTMLInputElement;
    expect(input.value).toBe("Kanal 1");
  });

  it("commits the channel rename on save and shows a toast", async () => {
    await openChannelEditor();
    await fireEvent.input(screen.getByLabelText("channel.rename"), {
      target: { value: "Kitchen Light" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(mockRenameChannel).toHaveBeenCalledWith("0001ABCD", 1, "Kitchen Light");
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("channel.renamed");
    // The pencil button and the inline editor share the same "channel.rename"
    // label, so assert on the textbox role specifically — the pencil button
    // (also labelled "channel.rename") is expected to reappear.
    await waitFor(() => {
      expect(screen.queryByRole("textbox", { name: "channel.rename" })).not.toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: "channel.rename" })).toBeInTheDocument();
  });

  it("cancels on Escape without calling the API", async () => {
    await openChannelEditor();
    await fireEvent.keyDown(screen.getByLabelText("channel.rename"), { key: "Escape" });

    await waitFor(() => {
      expect(screen.queryByRole("textbox", { name: "channel.rename" })).not.toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: "channel.rename" })).toBeInTheDocument();
    expect(mockRenameChannel).not.toHaveBeenCalled();
  });

  it("shows an error toast and keeps the editor open when the CCU rename fails", async () => {
    mockRenameChannel.mockRejectedValueOnce(new Error("ccu unreachable"));
    await openChannelEditor();
    await fireEvent.input(screen.getByLabelText("channel.rename"), {
      target: { value: "Kitchen Light" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("ccu unreachable");
    });
    expect(screen.getByLabelText("channel.rename")).toBeInTheDocument();
  });
});
