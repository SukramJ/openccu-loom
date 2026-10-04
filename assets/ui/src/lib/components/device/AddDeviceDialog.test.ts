// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, screen, waitFor, fireEvent, within } from "@testing-library/svelte";
import { flushSync } from "svelte";
import type { DeviceSummary } from "$lib/api/types";

const {
  mockListInbox,
  mockAccept,
  mockRelease,
  mockToastSuccess,
  mockToastError,
  calls,
  offers,
} = vi.hoisted(() => ({
  mockListInbox: vi.fn(),
  mockAccept: vi.fn(),
  mockRelease: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  // Every accept / release in the order the dialog sent them.
  calls: [] as string[],
  // Feature answers per key; a missing key is offered.
  offers: {} as Record<string, boolean>,
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    acceptInboxDevice: (...args: unknown[]) => {
      calls.push("accept:" + String(args[0]));
      return mockAccept(...args);
    },
    releaseDevice: (...args: unknown[]) => {
      calls.push("release:" + String(args[0]));
      return mockRelease(...args);
    },
    listRooms: vi.fn().mockResolvedValue([{ name: "Bathroom" }, { name: "Kitchen" }]),
    listFunctions: vi.fn().mockResolvedValue([{ name: "Climate" }, { name: "Lights" }]),
    createRoom: vi.fn(),
    createFunction: vi.fn(),
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
  centralStore: {
    items: [],
    featureAvailable: () => true,
    centralsLacking: () => [],
    offers: (_central: string | undefined, key: string) => offers[key] !== false,
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
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
  calls.length = 0;
  for (const k of Object.keys(offers)) delete offers[k];
  // Reset, not just clear: a once-queued answer a test did not consume
  // must not leak into the next one.
  mockListInbox.mockReset().mockResolvedValue([]);
  mockAccept.mockReset().mockResolvedValue(undefined);
  mockRelease.mockReset().mockResolvedValue(undefined);
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

// Selects a catalogue entry through a RoomFunctionSelect combobox inside
// the given container: type into it, then click the option.
async function pick(container: HTMLElement, label: string, option: string) {
  const input = within(container).getByLabelText(label) as HTMLInputElement;
  await fireEvent.input(input, { target: { value: option } });
  const list = await waitFor(() => {
    const el = document.getElementById(`${input.id}-list`);
    if (!el) throw new Error(`combobox ${input.id} did not open`);
    return el;
  });
  await fireEvent.click(within(list).getByRole("option", { name: option }));
}

const HELD = { address: "A1", model: "HmIP-STH", central: "lite", pending_creation: true };

describe("AddDeviceDialog — accepting without leaving", () => {
  it("accepts and releases a held device with name, rooms and functions, in that order", async () => {
    mockListInbox.mockResolvedValue([HELD]);
    renderOpen();

    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.input(within(list).getByLabelText("inbox.accept_dialog.name_label"), {
      target: { value: "  Bathroom  " },
    });
    await fireEvent.click(within(list).getByRole("checkbox"));
    await pick(list, "inbox.accept_dialog.rooms_label", "Bathroom");
    await pick(list, "inbox.accept_dialog.functions_label", "Climate");
    await fireEvent.click(within(list).getByText("inbox.accept_release"));

    await waitFor(() => expect(calls).toEqual(["accept:A1", "release:A1"]));
    expect(mockAccept).toHaveBeenCalledWith("A1", "lite", {
      name: "Bathroom",
      include_channels: true,
      rooms: ["Bathroom"],
      functions: ["Climate"],
    });
    expect(mockRelease).toHaveBeenCalledWith("A1", "lite");
    expect(mockToastSuccess).toHaveBeenCalledWith("inbox.accepted_released:Bathroom");
    expect(mockToastError).not.toHaveBeenCalled();
  });

  it("does not release when the accept failed", async () => {
    mockListInbox.mockResolvedValue([HELD]);
    mockAccept.mockRejectedValueOnce(new Error("upstream down"));
    renderOpen();

    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.click(within(list).getByText("inbox.accept_release"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("upstream down"));
    expect(calls).toEqual(["accept:A1"]);
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });

  it("reports a failed release after a good accept and lists the device as awaiting release", async () => {
    mockListInbox.mockResolvedValueOnce([HELD]);
    // The reload after the accept never answers: the move to the
    // awaiting-release list is the dialog's own, not the server's.
    mockListInbox.mockReturnValue(new Promise(() => {}));
    mockRelease.mockRejectedValueOnce(new Error("upstream down"));
    renderOpen();

    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.click(within(list).getByText("inbox.accept_release"));

    await waitFor(() =>
      expect(mockToastError).toHaveBeenCalledWith(
        "inbox.release_failed_after_accept:A1,upstream down",
      ),
    );
    expect(calls).toEqual(["accept:A1", "release:A1"]);
    // Never a full success.
    expect(mockToastSuccess).not.toHaveBeenCalled();
    const waiting = await screen.findByTestId("add-device-awaiting-release");
    expect(within(waiting).getByText("A1")).toBeInTheDocument();
    expect(within(waiting).getByText("inbox.release")).toBeInTheDocument();
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();
  });

  it("accepts plainly without a release", async () => {
    mockListInbox.mockResolvedValueOnce([HELD]);
    // What the daemon lists once the accept built the device.
    mockListInbox.mockResolvedValue([{ ...HELD, pending_creation: false, awaiting_release: true }]);
    renderOpen();
    const list = await screen.findByTestId("add-device-acceptable");
    await fireEvent.click(within(list).getByText("inbox.accept_dialog.submit"));

    await waitFor(() => expect(mockAccept).toHaveBeenCalledWith("A1", "lite", undefined));
    expect(mockRelease).not.toHaveBeenCalled();
    expect(mockToastSuccess).toHaveBeenCalledWith("inbox.accepted:A1");
    // The device was built and stays withheld: its remaining step is shown.
    const waiting = await screen.findByTestId("add-device-awaiting-release");
    expect(within(waiting).getByText("inbox.release")).toBeInTheDocument();
  });

  it("accepts a CCU inbox entry without a release call", async () => {
    // Not held by the daemon (the hold is off): the accept builds and
    // publishes it, and a release of a device nothing withholds would answer
    // not-found. The dialog offers the accept alone.
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalledTimes(1));
    mockListInbox.mockResolvedValue([{ address: "B1", model: "HM-Sec-SC", central: "ccu" }]);
    installModeStoreMock.active = true;
    flushSync();
    installModeStoreMock.active = false;
    flushSync();

    const list = await screen.findByTestId("add-device-acceptable");
    expect(within(list).queryByText("inbox.accept_release")).toBeNull();
    await fireEvent.input(within(list).getByLabelText("inbox.accept_dialog.name_label"), {
      target: { value: "Door" },
    });
    await fireEvent.click(within(list).getByText("inbox.accept_dialog.submit"));

    await waitFor(() =>
      expect(mockAccept).toHaveBeenCalledWith("B1", "ccu", { name: "Door" }),
    );
    expect(mockRelease).not.toHaveBeenCalled();
    expect(calls).toEqual(["accept:B1"]);
  });

  it("hides the fields a central does not offer", async () => {
    offers["device.rename"] = false;
    offers["taxonomy.assign"] = false;
    mockListInbox.mockResolvedValue([HELD]);
    renderOpen();

    const list = await screen.findByTestId("add-device-acceptable");
    expect(within(list).queryByLabelText("inbox.accept_dialog.name_label")).toBeNull();
    expect(within(list).queryByLabelText("inbox.accept_dialog.rooms_label")).toBeNull();
    expect(within(list).queryByLabelText("inbox.accept_dialog.functions_label")).toBeNull();
    // Accepting stays possible: it is the hold, not a taxonomy feature.
    await fireEvent.click(within(list).getByText("inbox.accept_release"));
    await waitFor(() => expect(calls).toEqual(["accept:A1", "release:A1"]));
    expect(mockAccept).toHaveBeenCalledWith("A1", "lite", undefined);
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
  // dialog opened belongs to an earlier session and stays in the view.
  it("leaves an older CCU inbox entry to the new-devices view", async () => {
    mockListInbox.mockResolvedValue([{ address: "B0", model: "HM-Sec-SC", central: "ccu" }]);
    renderOpen();
    await waitFor(() => expect(mockListInbox).toHaveBeenCalled());
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();
  });
});

describe("AddDeviceDialog — releasing without leaving", () => {
  const AWAITING = { address: "A2", model: "HmIP-STH", central: "lite", awaiting_release: true };

  it("lists an entry awaiting release by name with its release and device link", async () => {
    deviceStoreMock.items = [...deviceStoreMock.items, device("A2", "Hallway climate")];
    mockListInbox.mockResolvedValue([AWAITING]);
    renderOpen();

    const waiting = await screen.findByTestId("add-device-awaiting-release");
    expect(within(waiting).getByText("add_device.awaiting_release:1")).toBeInTheDocument();
    expect(within(waiting).getByText("Hallway climate")).toBeInTheDocument();
    const link = within(waiting).getByRole("link", { name: "inbox.configure" });
    expect(link.getAttribute("href")).toBe("#/devices/A2");
    expect(screen.queryByTestId("add-device-acceptable")).toBeNull();
  });

  it("releases it through the release endpoint alone", async () => {
    mockListInbox.mockResolvedValue([AWAITING]);
    renderOpen();

    const waiting = await screen.findByTestId("add-device-awaiting-release");
    await fireEvent.click(within(waiting).getByText("inbox.release"));

    await waitFor(() => expect(mockRelease).toHaveBeenCalledWith("A2", "lite"));
    expect(mockAccept).not.toHaveBeenCalled();
    expect(mockToastSuccess).toHaveBeenCalledWith("inbox.released:A2");
  });

  it("surfaces a failed release and keeps the entry", async () => {
    mockListInbox.mockResolvedValue([AWAITING]);
    mockRelease.mockRejectedValueOnce(new Error("upstream down"));
    renderOpen();

    const waiting = await screen.findByTestId("add-device-awaiting-release");
    await fireEvent.click(within(waiting).getByText("inbox.release"));

    await waitFor(() => expect(mockToastError).toHaveBeenCalledWith("upstream down"));
    expect(mockToastSuccess).not.toHaveBeenCalled();
    expect(screen.getByTestId("add-device-awaiting-release")).toBeInTheDocument();
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
      "inbox.accept_release",
      "inbox.accept_only_title",
      "inbox.accepted_released",
      "inbox.release_failed_after_accept",
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
