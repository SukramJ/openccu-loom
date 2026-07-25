// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent, within } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListInbox = vi.fn();
const mockListRooms = vi.fn();
const mockListFunctions = vi.fn();
const mockAcceptInboxDevice = vi.fn();
const mockListReplaceCandidates = vi.fn();
const mockReplaceDevice = vi.fn();
const mockSearchWiredDevices = vi.fn();
const mockGetGroups = vi.fn();
const mockGroupSuitable = vi.fn();
const mockUpdateGroup = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();
const mockConfirmAsk = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    listRooms: (...args: unknown[]) => mockListRooms(...args),
    listFunctions: (...args: unknown[]) => mockListFunctions(...args),
    acceptInboxDevice: (...args: unknown[]) => mockAcceptInboxDevice(...args),
    listReplaceCandidates: (...args: unknown[]) => mockListReplaceCandidates(...args),
    replaceDevice: (...args: unknown[]) => mockReplaceDevice(...args),
    searchWiredDevices: (...args: unknown[]) => mockSearchWiredDevices(...args),
    getGroups: (...args: unknown[]) => mockGetGroups(...args),
    groupSuitableMembers: (...args: unknown[]) => mockGroupSuitable(...args),
    updateGroup: (...args: unknown[]) => mockUpdateGroup(...args),
    listInstallModeInterfaces: vi.fn().mockResolvedValue([]),
    setInstallModeInterface: vi.fn(),
    pairDeviceInstallMode: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, unknown>) => {
    if (params) {
      return Object.entries(params).reduce(
        (s, [k, v]) => s.replace(`{${k}}`, String(v)),
        key,
      );
    }
    return key;
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en", expertMode: false },
}));

vi.mock("$lib/stores/installMode.svelte", () => ({
  installModeStore: {
    active: false,
    remainingSeconds: null,
    interfaces: [],
    busy: false,
    banner: null,
    refresh: vi.fn(),
    toggle: vi.fn(),
    ensurePoll: vi.fn(),
    release: vi.fn(),
  },
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import Inbox from "./Inbox.svelte";
// installModeStore is mocked above as a plain object; importing it here
// (module cache dedup gives the same reference the component sees) lets
// individual tests populate .interfaces before render so selectedInterface
// defaults to a chosen interface via the component's own $effect. The real
// store type exposes `interfaces` as a read-only getter, so writes go
// through this cast — the mock backing it is a plain writable object.
import { installModeStore } from "$lib/stores/installMode.svelte";
import type { InstallModeInterfaceEntry } from "$lib/api/types";

function setStoreInterfaces(list: InstallModeInterfaceEntry[]) {
  (installModeStore as unknown as { interfaces: InstallModeInterfaceEntry[] }).interfaces =
    list;
}

const ONE_DEVICE = [{ address: "0009ABCD", model: "HmIP-STH", central: "" }];

// A BidCos-RF device is the only inbox interface that offers the
// "replace existing device" action (isReplaceable in Inbox.svelte) — HmIP
// throws NotImplementedException on the CCU side.
const REPLACEABLE_DEVICE = [
  { address: "0009ABCD", model: "HM-Sec-SC", central: "", interface: "BidCos-RF" },
];

beforeEach(() => {
  vi.clearAllMocks();
  mockListInbox.mockResolvedValue(ONE_DEVICE);
  mockListRooms.mockResolvedValue([{ name: "Kitchen" }, { name: "Living Room" }]);
  mockListFunctions.mockResolvedValue([{ name: "Lights" }, { name: "Heating" }]);
  mockAcceptInboxDevice.mockResolvedValue(undefined);
  mockGetGroups.mockResolvedValue([]);
  mockGroupSuitable.mockResolvedValue({ assignable: [], leftover: [] });
  mockUpdateGroup.mockResolvedValue(undefined);
  mockListReplaceCandidates.mockResolvedValue([]);
  mockReplaceDevice.mockResolvedValue(undefined);
  mockConfirmAsk.mockResolvedValue(true);
  // Reset the plain-object store mock's mutable state: individual tests in
  // the "wired bus search" block below populate .interfaces before render.
  setStoreInterfaces([]);
});

afterEach(() => {
  cleanup();
});

// openDialog renders the route, waits for the inbox row to load, opens the
// accept dialog and waits for the room/function catalogues to render.
async function openDialog() {
  render(Inbox);
  await waitFor(() => {
    expect(screen.getByText("inbox.accept")).toBeInTheDocument();
  });
  await fireEvent.click(screen.getByText("inbox.accept"));
  await waitFor(() => {
    expect(
      screen.getByLabelText("inbox.accept_dialog.rooms_label"),
    ).toBeInTheDocument();
  });
}

function submitButton(): HTMLElement {
  return screen.getByText("inbox.accept_dialog.submit");
}

function checkboxNextTo(label: string): HTMLInputElement {
  const el = screen.getByText(label);
  const input = el.querySelector('input[type="checkbox"]');
  if (!input) throw new Error(`no checkbox found next to label ${label}`);
  return input as HTMLInputElement;
}

// Selects a catalogue entry through the RoomFunctionSelect combobox: type into
// the input (id "inbox-rooms" / "inbox-functions"), then click its option.
async function pickFromCombo(comboId: string, option: string) {
  const input = document.getElementById(comboId) as HTMLInputElement;
  await fireEvent.input(input, { target: { value: option } });
  const list = await waitFor(() => {
    const el = document.getElementById(`${comboId}-list`);
    if (!el) throw new Error(`combobox ${comboId} did not open`);
    return el;
  });
  await fireEvent.click(within(list).getByRole("option", { name: option }));
}

// ---------------------------------------------------------------------------
// Dialog state → accept-config payload
// ---------------------------------------------------------------------------
// Inbox.svelte builds the accept body itself (confirmAccept) — this is the
// one piece of new G10 logic that has no Go-side counterpart, so it is
// worth locking in at the component level.

describe("Inbox — accept dialog config payload", () => {
  it("performs a plain accept (undefined config) when every field is left blank", async () => {
    await openDialog();
    await fireEvent.click(submitButton());

    await waitFor(() => {
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "", undefined);
    });
  });

  it("builds a config object carrying only the fields the operator touched", async () => {
    await openDialog();

    const nameInput = document.querySelector("#accept-name") as HTMLInputElement;
    await fireEvent.input(nameInput, { target: { value: "Kitchen Switch" } });

    const includeChannels = checkboxNextTo("inbox.accept_dialog.include_channels");
    expect(includeChannels.disabled).toBe(false); // enabled now that a name is set
    await fireEvent.click(includeChannels);

    await pickFromCombo("inbox-rooms", "Kitchen");
    await pickFromCombo("inbox-functions", "Lights");

    await fireEvent.click(submitButton());

    await waitFor(() => {
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "", {
        name: "Kitchen Switch",
        include_channels: true,
        rooms: ["Kitchen"],
        functions: ["Lights"],
      });
    });
  });

  it("disables the include-channels checkbox until a name is entered", async () => {
    await openDialog();
    const includeChannels = checkboxNextTo("inbox.accept_dialog.include_channels");
    expect(includeChannels.disabled).toBe(true);

    const nameInput = document.querySelector("#accept-name") as HTMLInputElement;
    await fireEvent.input(nameInput, { target: { value: "Kitchen Switch" } });
    expect(checkboxNextTo("inbox.accept_dialog.include_channels").disabled).toBe(false);

    // Clearing the name back out re-disables it.
    await fireEvent.input(nameInput, { target: { value: "" } });
    expect(checkboxNextTo("inbox.accept_dialog.include_channels").disabled).toBe(true);
  });

  it("removing a previously selected room drops it from the payload", async () => {
    await openDialog();

    await pickFromCombo("inbox-rooms", "Kitchen"); // select → chip
    // Remove the chip via its ✕ button.
    await fireEvent.click(
      screen.getByRole("button", { name: "roomfn.remove_named" }),
    );

    await fireEvent.click(submitButton());

    await waitFor(() => {
      // Nothing was left selected → falls back to a plain accept.
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "", undefined);
    });
  });

  it("cancel closes the dialog without calling the API", async () => {
    await openDialog();
    await fireEvent.click(screen.getByText("common.cancel"));

    expect(mockAcceptInboxDevice).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(screen.queryByText("inbox.accept_dialog.title")).toBeNull();
    });
  });

  it("still allows a plain accept when the room/function catalogue fails to load", async () => {
    mockListRooms.mockRejectedValueOnce(new Error("network down"));
    mockListFunctions.mockRejectedValueOnce(new Error("network down"));

    render(Inbox);
    await waitFor(() => {
      expect(screen.getByText("inbox.accept")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.accept"));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith(
        expect.stringContaining("inbox.accept_dialog.catalog_error"),
      );
    });
    // The dialog itself must still be usable — the combobox renders with an
    // empty catalogue (the operator could still create/type), and a plain
    // accept goes through.
    await waitFor(() => {
      expect(
        screen.getByLabelText("inbox.accept_dialog.rooms_label"),
      ).toBeInTheDocument();
    });
    await fireEvent.click(submitButton());
    await waitFor(() => {
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "", undefined);
    });
  });
});

// ---------------------------------------------------------------------------
// Guided device replace — isReplaceable gating + confirm/submit flow
// ---------------------------------------------------------------------------
// Mirrors the accept-dialog coverage above: Inbox.svelte's replace dialog
// (openReplace/confirmReplace) is the one piece of client-only logic
// (interface gating, confirm-then-submit) with no Go-side counterpart.

describe("Inbox — replace workflow", () => {
  it("hides the replace action for a device on a non-BidCos interface", async () => {
    mockListInbox.mockResolvedValue([
      { address: "0009ABCD", model: "HmIP-STH", central: "", interface: "HmIP-RF" },
    ]);
    render(Inbox);
    await waitFor(() => {
      expect(screen.getByText("inbox.accept")).toBeInTheDocument();
    });
    expect(screen.queryByText("inbox.replace.button")).toBeNull();
  });

  it("opens the replace dialog for a BidCos device and lists candidates", async () => {
    mockListInbox.mockResolvedValue(REPLACEABLE_DEVICE);
    mockListReplaceCandidates.mockResolvedValue([
      { address: "OLD001", name: "Fenster", model: "HM-Sec-SC", model_matches: true },
    ]);

    render(Inbox);
    await waitFor(() => {
      expect(screen.getByText("inbox.replace.button")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.replace.button"));

    await waitFor(() => {
      expect(mockListReplaceCandidates).toHaveBeenCalledWith("0009ABCD", undefined);
      expect(screen.getByText("Fenster")).toBeInTheDocument();
    });
  });

  it("confirms a replace and forwards the chosen candidate to replaceDevice", async () => {
    mockListInbox.mockResolvedValue(REPLACEABLE_DEVICE);
    mockListReplaceCandidates.mockResolvedValue([
      { address: "OLD001", name: "Fenster", model: "HM-Sec-SC", model_matches: true },
    ]);

    render(Inbox);
    await waitFor(() => {
      expect(screen.getByText("inbox.replace.button")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.replace.button"));
    await waitFor(() => {
      expect(screen.getByText("Fenster")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("Fenster"));

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalled();
      expect(mockReplaceDevice).toHaveBeenCalledWith("0009ABCD", "OLD001", undefined);
      expect(mockToastSuccess).toHaveBeenCalled();
    });
  });

  it("does not call replaceDevice when the confirm dialog is dismissed", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    mockListInbox.mockResolvedValue(REPLACEABLE_DEVICE);
    mockListReplaceCandidates.mockResolvedValue([
      { address: "OLD001", name: "Fenster", model: "HM-Sec-SC", model_matches: true },
    ]);

    render(Inbox);
    await waitFor(() => {
      expect(screen.getByText("inbox.replace.button")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.replace.button"));
    await waitFor(() => {
      expect(screen.getByText("Fenster")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("Fenster"));

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalled();
    });
    expect(mockReplaceDevice).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// Wired-bus device search (BidCos-Wired scan button)
// ---------------------------------------------------------------------------
// searchWiredBus (Inbox.svelte) is offered only when the operator has the
// BidCos-Wired interface selected — install mode itself doesn't apply to
// the wired bus, a scan does. installModeStore.interfaces seeds
// selectedInterface via the component's own $effect (defaults to the
// first entry in the list), so each test below sets it before render.

function installModeInterfaces(iface: string) {
  return [{ interface: iface, active: false, seconds: 0, observed: true, central: "" }];
}

describe("Inbox — wired bus search", () => {
  it("shows the search-wired-bus button when BidCos-Wired is selected", async () => {
    setStoreInterfaces(installModeInterfaces("BidCos-Wired"));
    render(Inbox);

    await waitFor(() => {
      expect(screen.getByText("inbox.search_wired")).toBeInTheDocument();
    });
  });

  it("hides the search-wired-bus button for a non-wired interface", async () => {
    setStoreInterfaces(installModeInterfaces("HmIP-RF"));
    render(Inbox);

    await waitFor(() => {
      expect(screen.getByText("inbox.accept")).toBeInTheDocument();
    });
    expect(screen.queryByText("inbox.search_wired")).toBeNull();
  });

  it("clicking the button scans the bus and shows the found count", async () => {
    setStoreInterfaces(installModeInterfaces("BidCos-Wired"));
    mockSearchWiredDevices.mockResolvedValue({
      central: "",
      interface: "BidCos-Wired",
      found: 2,
    });
    render(Inbox);

    await waitFor(() => {
      expect(screen.getByText("inbox.search_wired")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.search_wired"));

    await waitFor(() => {
      expect(mockSearchWiredDevices).toHaveBeenCalledWith("BidCos-Wired", undefined);
      expect(mockToastSuccess).toHaveBeenCalled();
    });
  });

  it("surfaces a toast error when the scan fails", async () => {
    setStoreInterfaces(installModeInterfaces("BidCos-Wired"));
    mockSearchWiredDevices.mockRejectedValue(new Error("hs485d unreachable"));
    render(Inbox);

    await waitFor(() => {
      expect(screen.getByText("inbox.search_wired")).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText("inbox.search_wired"));

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalled();
    });
  });
});

describe("Inbox — GR05 group assignment on accept", () => {
  it("adds the accepted device's assignable channel to the chosen group", async () => {
    mockGetGroups.mockResolvedValue([
      {
        central: "ccu",
        groups: [
          {
            id: 5,
            name: "Heating",
            type_id: "hmip.heating.group",
            forbid_single_operation: false,
            members: [{ address: "OLD0000001:1" }],
          },
        ],
      },
    ]);
    mockGroupSuitable.mockResolvedValue({
      assignable: [{ address: "0009ABCD:1" }],
      leftover: [],
    });

    await openDialog();
    const sel = (await waitFor(() => {
      const el = document.getElementById("inbox-group");
      if (!el) throw new Error("group picker not shown");
      return el as HTMLSelectElement;
    }));
    await fireEvent.change(sel, { target: { value: "5" } });
    await fireEvent.click(submitButton());

    await waitFor(() => {
      expect(mockUpdateGroup).toHaveBeenCalledWith(
        5,
        {
          name: "Heating",
          forbid_single_operation: false,
          members: ["OLD0000001:1", "0009ABCD:1"],
        },
        expect.anything(),
      );
    });
  });
});
