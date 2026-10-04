// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent, within } from "@testing-library/svelte";
import { flushSync } from "svelte";
import type { DeviceSummary } from "$lib/api/types";

const { mockListInbox, mockAccept } = vi.hoisted(() => ({
  mockListInbox: vi.fn(),
  mockAccept: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    acceptInboxDevice: (...args: unknown[]) => mockAccept(...args),
    listInstallModeInterfaces: vi.fn().mockResolvedValue([]),
    setInstallModeInterface: vi.fn(),
    pairDeviceInstallMode: vi.fn(),
    searchWiredDevices: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status = 0;
  },
}));

vi.mock("$lib/stores/devices.svelte", async () => ({
  deviceStore: (await import("./__testutils__/reactiveStores.svelte")).deviceStoreMock,
}));

vi.mock("$lib/stores/installMode.svelte", async () => ({
  installModeStore: (await import("./__testutils__/reactiveStores.svelte"))
    .installModeStoreMock,
}));

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: { items: [], featureAvailable: () => true, centralsLacking: () => [] },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${Object.values(params).join(",")}` : key,
}));

vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../../../routes/__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import AddDeviceDialog from "./AddDeviceDialog.svelte";
import { deviceStoreMock, installModeStoreMock } from "./__testutils__/reactiveStores.svelte";

function device(address: string, name: string): DeviceSummary {
  return {
    address,
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-PSM",
    name,
    available: true,
    channels_count: 2,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
  } as DeviceSummary;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockListInbox.mockResolvedValue([]);
  mockAccept.mockResolvedValue(undefined);
  deviceStoreMock.items = [device("OLD0000001", "Lamp")];
  deviceStoreMock.lastLoaded = new Date();
  installModeStoreMock.active = false;
  installModeStoreMock.interfaces = [];
});

afterEach(() => cleanup());

function renderOpen() {
  return render(AddDeviceDialog, { open: true, onClose: vi.fn() });
}

describe("AddDeviceDialog — arrived devices", () => {
  it("lists a device that joined the device list after the dialog opened", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());

    deviceStoreMock.items = [...deviceStoreMock.items, device("NEW0000001", "Sensor")];
    flushSync();

    const link = await screen.findByRole("link", { name: "Sensor" });
    expect(link.getAttribute("href")).toBe("#/devices/NEW0000001");
    // A device known before the dialog opened did not arrive.
    expect(screen.queryByRole("link", { name: "Lamp" })).toBeNull();
  });
});

describe("AddDeviceDialog — accepting without leaving", () => {
  it("accepts a held device with the typed name and channel rename", async () => {
    mockListInbox.mockResolvedValue([
      { address: "A1", model: "HmIP-STH", central: "lite", pending_creation: true },
    ]);
    renderOpen();

    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.input(within(list).getByLabelText("inbox.accept_dialog.name_label"), {
      target: { value: "  Bathroom  " },
    });
    await fireEvent.click(within(list).getByRole("checkbox"));
    await fireEvent.click(within(list).getByText("inbox.accept"));

    await waitFor(() =>
      expect(mockAccept).toHaveBeenCalledWith("A1", "lite", {
        name: "Bathroom",
        include_channels: true,
      }),
    );
  });

  it("accepts plainly when no name was typed", async () => {
    mockListInbox.mockResolvedValue([
      { address: "A1", model: "HmIP-STH", central: "lite", pending_creation: true },
    ]);
    renderOpen();
    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.click(within(list).getByText("inbox.accept"));
    await waitFor(() => expect(mockAccept).toHaveBeenCalledWith("A1", "lite", undefined));
  });

  it("lists a CCU inbox entry that appeared while the dialog was open", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalledTimes(1));
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();

    mockListInbox.mockResolvedValue([{ address: "B1", model: "HM-Sec-SC", central: "ccu" }]);
    // The window ending triggers a final reload of the inbox.
    installModeStoreMock.active = true;
    flushSync();
    installModeStoreMock.active = false;
    flushSync();

    const list = await screen.findByTestId("add-device-acceptable");
    expect(within(list).getByText("B1")).toBeInTheDocument();
  });

  // Negative control: a CCU inbox entry that was already waiting before the
  // dialog opened belongs to an earlier session and stays in the inbox.
  it("leaves an older CCU inbox entry to the inbox", async () => {
    mockListInbox.mockResolvedValue([{ address: "B0", model: "HM-Sec-SC", central: "ccu" }]);
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();
  });

  it("points an entry awaiting release to the inbox", async () => {
    mockListInbox.mockResolvedValue([
      { address: "A2", model: "HmIP-STH", central: "lite", awaiting_release: true },
    ]);
    renderOpen();
    const line = await screen.findByTestId("add-device-awaiting-release");
    expect(line.textContent).toContain("add_device.awaiting_release:1");
    expect(within(line).getByRole("link").getAttribute("href")).toBe("#/inbox");
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();
  });
});

describe("AddDeviceDialog — nothing joined", () => {
  it("explains an empty pairing window once it has ended", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());

    installModeStoreMock.active = true;
    flushSync();
    installModeStoreMock.active = false;
    flushSync();

    await screen.findByText("add_device.nothing_joined");
  });

  // Negative control: before any window ran there is nothing to explain.
  it("stays silent while no pairing window has run", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());
    expect(screen.queryByText("add_device.nothing_joined")).toBeNull();
  });

  it("stays silent while the window is still open", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());
    installModeStoreMock.active = true;
    flushSync();
    expect(screen.queryByText("add_device.nothing_joined")).toBeNull();
  });

  it("stays silent when a device joined during the window", async () => {
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());

    installModeStoreMock.active = true;
    flushSync();
    deviceStoreMock.items = [...deviceStoreMock.items, device("NEW0000001", "Sensor")];
    installModeStoreMock.active = false;
    flushSync();

    await screen.findByRole("link", { name: "Sensor" });
    expect(screen.queryByText("add_device.nothing_joined")).toBeNull();
  });
});

describe("AddDeviceDialog — install-mode poll", () => {
  it("holds the install-mode poll while open and releases it on close", async () => {
    const { rerender } = renderOpen();
    expect(installModeStoreMock.ensurePoll).toHaveBeenCalledTimes(1);
    expect(installModeStoreMock.release).not.toHaveBeenCalled();

    await rerender({ open: false, onClose: vi.fn() });
    expect(installModeStoreMock.release).toHaveBeenCalledTimes(1);
  });

  it("releases the poll when unmounted while open", () => {
    const { unmount } = renderOpen();
    unmount();
    expect(installModeStoreMock.release).toHaveBeenCalledTimes(1);
  });
});

describe("AddDeviceDialog — catalogue entries", () => {
  // The component tests above run against a key-echoing t(); this asks the
  // real catalogues, so a key missing in one locale fails here.
  it("has every new string in both locales", async () => {
    const real = await vi.importActual<typeof import("$lib/i18n")>("$lib/i18n");
    const keys = [
      "devicelist.add_device",
      "devicelist.add_device_title",
      "add_device.title",
      "add_device.intro",
      "add_device.arrived_title",
      "add_device.arrived_empty",
      "add_device.interface_label",
      "add_device.no_interfaces",
      "add_device.start_pairing",
      "add_device.targeted_options",
      "add_device.acceptable_title",
      "add_device.more_options",
      "add_device.awaiting_release",
      "add_device.waiting_link",
      "add_device.nothing_joined",
    ];
    const en = new Set(real.catalogKeys("en"));
    const de = new Set(real.catalogKeys("de"));
    for (const k of keys) {
      expect(en.has(k), `en: ${k}`).toBe(true);
      expect(de.has(k), `de: ${k}`).toBe(true);
    }
  });
});
