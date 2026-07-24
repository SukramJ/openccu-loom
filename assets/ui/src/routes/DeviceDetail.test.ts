// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent, within } from "@testing-library/svelte";

const {
  mockGetDevice,
  mockGetDeviceSchedule,
  mockGetPreference,
  mockPutPreference,
  mockRenameDevice,
  mockRenameChannel,
  mockSetChannelRooms,
  mockSetChannelFunctions,
  mockListRooms,
  mockListFunctions,
  mockCreateRoom,
  mockCreateFunction,
  mockDeleteDevice,
  mockListLinks,
  mockListPrograms,
  mockRestoreDeviceConfig,
  mockTestDeviceCommunication,
  mockToastSuccess,
  mockToastError,
  mockToastWarn,
} = vi.hoisted(() => ({
  mockGetDevice: vi.fn(),
  mockGetDeviceSchedule: vi.fn(),
  mockGetPreference: vi.fn(),
  mockPutPreference: vi.fn(),
  mockRenameDevice: vi.fn(),
  mockRenameChannel: vi.fn(),
  mockSetChannelRooms: vi.fn(),
  mockSetChannelFunctions: vi.fn(),
  mockListRooms: vi.fn(),
  mockListFunctions: vi.fn(),
  mockCreateRoom: vi.fn(),
  mockCreateFunction: vi.fn(),
  mockDeleteDevice: vi.fn(),
  mockListLinks: vi.fn(),
  mockListPrograms: vi.fn(),
  mockRestoreDeviceConfig: vi.fn(),
  mockTestDeviceCommunication: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockToastWarn: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    getDeviceSchedule: (...args: unknown[]) => mockGetDeviceSchedule(...args),
    getPreference: (...args: unknown[]) => mockGetPreference(...args),
    putPreference: (...args: unknown[]) => mockPutPreference(...args),
    renameDevice: (...args: unknown[]) => mockRenameDevice(...args),
    renameChannel: (...args: unknown[]) => mockRenameChannel(...args),
    setChannelRooms: (...args: unknown[]) => mockSetChannelRooms(...args),
    setChannelFunctions: (...args: unknown[]) => mockSetChannelFunctions(...args),
    listRooms: (...args: unknown[]) => mockListRooms(...args),
    listFunctions: (...args: unknown[]) => mockListFunctions(...args),
    createRoom: (...args: unknown[]) => mockCreateRoom(...args),
    createFunction: (...args: unknown[]) => mockCreateFunction(...args),
    deleteDevice: (...args: unknown[]) => mockDeleteDevice(...args),
    listLinks: (...args: unknown[]) => mockListLinks(...args),
    listPrograms: (...args: unknown[]) => mockListPrograms(...args),
    restoreDeviceConfig: (...args: unknown[]) => mockRestoreDeviceConfig(...args),
    testDeviceCommunication: (...args: unknown[]) => mockTestDeviceCommunication(...args),
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
    warn: (...args: unknown[]) => mockToastWarn(...args),
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
import { confirmStore } from "$lib/stores/confirm.svelte";

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
  mockSetChannelRooms.mockResolvedValue(undefined);
  mockSetChannelFunctions.mockResolvedValue(undefined);
  mockListRooms.mockResolvedValue([
    { name: "Wohnzimmer" },
    { name: "Küche" },
    { name: "Bad" },
  ]);
  mockListFunctions.mockResolvedValue([
    { name: "Licht" },
    { name: "Heizung" },
  ]);
  mockCreateRoom.mockResolvedValue({ id: 9, name: "created" });
  mockCreateFunction.mockResolvedValue({ id: 9, name: "created" });
  mockDeleteDevice.mockResolvedValue(undefined);
  mockListLinks.mockResolvedValue([]);
  mockListPrograms.mockResolvedValue([]);
  mockRestoreDeviceConfig.mockResolvedValue(undefined);
  mockTestDeviceCommunication.mockResolvedValue({
    passed: true,
    started_at: "2026-07-22T10:00:00Z",
    completed_at: "2026-07-22T10:00:03Z",
    duration_ms: 3000,
    timed_out: false,
  });
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

describe("DeviceDetail — restore device config", () => {
  it("hides the restore-config button when config_restore_supported is absent", async () => {
    mockGetDevice.mockResolvedValue(baseDevice());
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    expect(
      screen.queryByRole("button", { name: "device.restore_config" }),
    ).not.toBeInTheDocument();
  });

  it("hides the restore-config button when config_restore_supported is false", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ config_restore_supported: false }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    expect(
      screen.queryByRole("button", { name: "device.restore_config" }),
    ).not.toBeInTheDocument();
  });

  it("shows the restore-config button when config_restore_supported is true", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ config_restore_supported: true }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.restore_config" }),
      ).toBeInTheDocument();
    });
  });

  it("confirms, calls the restore API, and shows a success toast", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ config_restore_supported: true }));
    vi.mocked(confirmStore.ask).mockResolvedValueOnce(true);
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.restore_config" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.restore_config" }));

    await waitFor(() => {
      expect(mockRestoreDeviceConfig).toHaveBeenCalledWith("0001ABCD");
    });
    expect(confirmStore.ask).toHaveBeenCalledOnce();
    expect(mockToastSuccess).toHaveBeenCalledWith("device.restore_config_triggered");
  });

  it("does nothing when the user cancels the confirm dialog", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ config_restore_supported: true }));
    // confirmStore.ask defaults to "cancelled" (see the file-wide mock above).
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.restore_config" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.restore_config" }));

    await waitFor(() => expect(confirmStore.ask).toHaveBeenCalledOnce());
    expect(mockRestoreDeviceConfig).not.toHaveBeenCalled();
  });

  it("shows an error toast when the restore call fails", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ config_restore_supported: true }));
    vi.mocked(confirmStore.ask).mockResolvedValueOnce(true);
    mockRestoreDeviceConfig.mockRejectedValueOnce(new Error("ccu unreachable"));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.restore_config" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.restore_config" }));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("ccu unreachable");
    });
  });
});

describe("DeviceDetail — communication test", () => {
  it("hides the test button when communication_test_supported is absent", async () => {
    mockGetDevice.mockResolvedValue(baseDevice());
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    expect(
      screen.queryByRole("button", { name: "device.communication_test" }),
    ).not.toBeInTheDocument();
  });

  it("hides the test button when communication_test_supported is false", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ communication_test_supported: false }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    expect(
      screen.queryByRole("button", { name: "device.communication_test" }),
    ).not.toBeInTheDocument();
  });

  it("shows the test button when communication_test_supported is true", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ communication_test_supported: true }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.communication_test" }),
      ).toBeInTheDocument();
    });
  });

  it("runs the test, shows a success toast, and renders a passed badge", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ communication_test_supported: true }));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.communication_test" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.communication_test" }));

    await waitFor(() => {
      expect(mockTestDeviceCommunication).toHaveBeenCalledWith("0001ABCD");
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("device.communication_test_passed");
    await waitFor(() => {
      expect(screen.getByText("device.communication_test_passed")).toBeInTheDocument();
    });
  });

  it("shows a warning toast and a failed badge when the device does not answer", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ communication_test_supported: true }));
    mockTestDeviceCommunication.mockResolvedValueOnce({
      passed: false,
      started_at: "2026-07-22T10:00:00Z",
      duration_ms: 30000,
      timed_out: true,
    });
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.communication_test" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.communication_test" }));

    await waitFor(() => {
      expect(mockToastWarn).toHaveBeenCalledWith("device.communication_test_failed");
    });
    expect(screen.getByText("device.communication_test_failed")).toBeInTheDocument();
  });

  it("shows an error toast when the test call fails", async () => {
    mockGetDevice.mockResolvedValue(baseDevice({ communication_test_supported: true }));
    mockTestDeviceCommunication.mockRejectedValueOnce(new Error("ccu unreachable"));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });

    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "device.communication_test" }),
      ).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.communication_test" }));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("ccu unreachable");
    });
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

describe("DeviceDetail — channel room/function assignment", () => {
  function deviceWithChannelAssignment(overrides: Record<string, unknown> = {}) {
    return baseDevice({
      channels: [
        {
          address: "0001ABCD:1",
          number: 1,
          type: "SWITCH",
          name: "Kanal 1",
          data_points_count: 0,
          rooms: ["Wohnzimmer"],
          functions: ["Licht"],
        },
      ],
      channels_count: 1,
      ...overrides,
    });
  }

  // The channel-level rooms/functions row sits in its own labelled
  // sibling `<div>` right after the "channel.rooms:"/"channel.functions:"
  // heading span — scoping through that sibling (rather than a bare
  // getByText("common.edit")) avoids matching the device-level rooms/
  // functions edit buttons rendered in the header above.
  function assignmentRow(labelText: string): HTMLElement {
    const label = screen.getByText(labelText);
    return label.nextElementSibling as HTMLElement;
  }

  async function openConfigureTab(overrides: Record<string, unknown> = {}) {
    mockGetDevice.mockResolvedValue(deviceWithChannelAssignment(overrides));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });
    await waitFor(() => {
      expect(screen.getAllByRole("tab").length).toBeGreaterThan(0);
    });
    await fireEvent.click(screen.getByRole("tab", { name: "device.toptab.configure" }));
    await waitFor(() => {
      expect(screen.getByText("channel.rooms:")).toBeInTheDocument();
    });
  }

  it("renders the channel's current assignment as chips", async () => {
    await openConfigureTab();
    expect(within(assignmentRow("channel.rooms:")).getByText("Wohnzimmer")).toBeInTheDocument();
    expect(within(assignmentRow("channel.functions:")).getByText("Licht")).toBeInTheDocument();
  });

  it("shows an empty combobox (no chips) when a channel has no assignment", async () => {
    await openConfigureTab({
      channels: [
        { address: "0001ABCD:1", number: 1, type: "SWITCH", name: "Kanal 1", data_points_count: 0 },
      ],
    });
    const roomsInput = screen.getByLabelText("channel.rooms") as HTMLInputElement;
    expect(roomsInput.value).toBe("");
    expect(
      within(assignmentRow("channel.rooms:")).queryByText("Wohnzimmer"),
    ).not.toBeInTheDocument();
  });

  it("adds a room from the catalogue and persists the extended set", async () => {
    await openConfigureTab();
    const input = screen.getByLabelText("channel.rooms") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Küche" } });

    const option = await within(assignmentRow("channel.rooms:")).findByRole(
      "option",
      { name: "Küche" },
    );
    await fireEvent.click(option);

    await waitFor(() => {
      expect(mockSetChannelRooms).toHaveBeenCalledWith("0001ABCD", 1, [
        "Wohnzimmer",
        "Küche",
      ]);
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("channel.rooms_updated");
    // Optimistic update — no re-fetch of the whole device on success.
    expect(mockGetDevice).toHaveBeenCalledTimes(1);
  });

  it("removes a function chip and persists the reduced set", async () => {
    await openConfigureTab();
    const row = assignmentRow("channel.functions:");
    await fireEvent.click(
      within(row).getByRole("button", { name: "roomfn.remove_named" }),
    );

    await waitFor(() => {
      expect(mockSetChannelFunctions).toHaveBeenCalledWith("0001ABCD", 1, []);
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("channel.functions_updated");
  });

  it("rolls back the optimistic chip and toasts when the write fails", async () => {
    mockSetChannelRooms.mockRejectedValueOnce(new Error("ccu unreachable"));
    await openConfigureTab();
    const input = screen.getByLabelText("channel.rooms") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Bad" } });
    const option = await within(assignmentRow("channel.rooms:")).findByRole(
      "option",
      { name: "Bad" },
    );
    await fireEvent.click(option);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("ccu unreachable");
    });
    // The optimistic "Bad" chip is rolled back — exactly one chip (the
    // original "Wohnzimmer") remains. ("Bad" reappears as a dropdown *option*,
    // so assert on the chip remove-buttons rather than on the text.)
    await waitFor(() => {
      expect(
        within(assignmentRow("channel.rooms:")).getAllByRole("button", {
          name: "roomfn.remove_named",
        }),
      ).toHaveLength(1);
    });
    expect(
      within(assignmentRow("channel.rooms:")).getByText("Wohnzimmer"),
    ).toBeInTheDocument();
  });

  it("creates a brand-new room on the spot and assigns it", async () => {
    await openConfigureTab();
    const input = screen.getByLabelText("channel.rooms") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "Flur" } });

    // "Flur" is not in the catalogue → the create affordance is offered.
    const createBtn = await screen.findByRole("button", {
      name: "roomfn.create.room",
    });
    await fireEvent.click(createBtn);

    await waitFor(() => {
      expect(mockCreateRoom).toHaveBeenCalledWith("Flur", undefined);
    });
    await waitFor(() => {
      expect(mockSetChannelRooms).toHaveBeenCalledWith("0001ABCD", 1, [
        "Wohnzimmer",
        "Flur",
      ]);
    });
  });
});

describe("DeviceDetail — remove device options dialog", () => {
  async function openDeleteDialog(overrides: Record<string, unknown> = {}) {
    mockGetDevice.mockResolvedValue(baseDevice(overrides));
    render(DeviceDetail, { props: { address: "0001ABCD", locale: "en" } });
    await waitFor(() => {
      expect(screen.getAllByText("Wohnzimmer Thermostat").length).toBeGreaterThan(0);
    });
    await fireEvent.click(screen.getByRole("button", { name: "device.remove" }));
    await waitFor(() => {
      expect(screen.getByText("device.delete.mode_label")).toBeInTheDocument();
    });
  }

  it("opens an options dialog and probes links + programs", async () => {
    await openDeleteDialog();
    expect(mockListLinks).toHaveBeenCalledWith("0001ABCD", "en");
    expect(mockListPrograms).toHaveBeenCalled();
    expect(screen.getByText("device.delete.mode_unpair")).toBeInTheDocument();
    expect(screen.getByText("device.delete.force")).toBeInTheDocument();
  });

  it("removes with reset=false force=false by default", async () => {
    await openDeleteDialog();
    await fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => {
      expect(mockDeleteDevice).toHaveBeenCalledWith("0001ABCD", {
        reset: false,
        force: false,
      });
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("device.removed");
  });

  it("forwards reset + force when the factory-reset radio and force box are set", async () => {
    await openDeleteDialog();
    await fireEvent.click(
      screen.getByRole("radio", { name: /device\.delete\.mode_reset/ }),
    );
    await fireEvent.click(screen.getByRole("checkbox"));
    await fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => {
      expect(mockDeleteDevice).toHaveBeenCalledWith("0001ABCD", {
        reset: true,
        force: true,
      });
    });
  });

  it("warns when direct links or programs reference the device", async () => {
    mockListLinks.mockResolvedValue([
      { sender_address: "0001ABCD:1", receiver_address: "X:1" },
    ]);
    mockListPrograms.mockResolvedValue([
      { name: "Prog", device_address: "0001ABCD" },
      { name: "Other", device_address: "9999" },
    ]);
    await openDeleteDialog();
    await waitFor(() => {
      expect(screen.getByText("device.delete.warning_title")).toBeInTheDocument();
    });
  });

  it("shows an error toast and keeps the dialog open on failure", async () => {
    mockDeleteDevice.mockRejectedValueOnce(new Error("CCU refused"));
    await openDeleteDialog();
    await fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith("CCU refused");
    });
    // Dialog stays open on failure — no silent fallback / no silent close.
    expect(screen.getByText("device.delete.mode_label")).toBeInTheDocument();
  });

  it("closes the dialog on cancel without calling deleteDevice", async () => {
    await openDeleteDialog();
    await fireEvent.click(screen.getByRole("button", { name: "common.cancel" }));
    await waitFor(() => {
      expect(screen.queryByText("device.delete.mode_label")).not.toBeInTheDocument();
    });
    expect(mockDeleteDevice).not.toHaveBeenCalled();
  });

  it("suppresses the dependency warning and stays usable when the link/program probe fails", async () => {
    mockListLinks.mockRejectedValue(new Error("network error"));
    mockListPrograms.mockRejectedValue(new Error("network error"));
    await openDeleteDialog();
    // Best-effort probe: a failed read must not block the dialog or surface
    // an error toast — it just falls back to "no known dependencies".
    expect(screen.queryByText("device.delete.warning_title")).not.toBeInTheDocument();
    expect(mockToastError).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "common.delete" }));
    await waitFor(() => {
      expect(mockDeleteDevice).toHaveBeenCalledWith("0001ABCD", {
        reset: false,
        force: false,
      });
    });
  });
});
