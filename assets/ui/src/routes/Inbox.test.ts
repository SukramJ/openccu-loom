// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockListInbox = vi.fn();
const mockListRooms = vi.fn();
const mockListFunctions = vi.fn();
const mockAcceptInboxDevice = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listInbox: (...args: unknown[]) => mockListInbox(...args),
    listRooms: (...args: unknown[]) => mockListRooms(...args),
    listFunctions: (...args: unknown[]) => mockListFunctions(...args),
    acceptInboxDevice: (...args: unknown[]) => mockAcceptInboxDevice(...args),
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

const ONE_DEVICE = [{ address: "0009ABCD", model: "HmIP-STH", central: "" }];

beforeEach(() => {
  vi.clearAllMocks();
  mockListInbox.mockResolvedValue(ONE_DEVICE);
  mockListRooms.mockResolvedValue([{ name: "Kitchen" }, { name: "Living Room" }]);
  mockListFunctions.mockResolvedValue([{ name: "Lights" }, { name: "Heating" }]);
  mockAcceptInboxDevice.mockResolvedValue(undefined);
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
    expect(screen.getByText("Kitchen")).toBeInTheDocument();
    expect(screen.getByText("Lights")).toBeInTheDocument();
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

    await fireEvent.click(checkboxNextTo("Kitchen"));
    await fireEvent.click(checkboxNextTo("Lights"));

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

  it("un-checking a previously selected room drops it from the payload", async () => {
    await openDialog();

    const kitchenCheckbox = checkboxNextTo("Kitchen");
    await fireEvent.click(kitchenCheckbox); // select
    await fireEvent.click(kitchenCheckbox); // deselect again

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
    // The dialog itself must still be usable — no rooms/functions to pick,
    // but a plain accept goes through.
    await waitFor(() => {
      expect(screen.getByText("inbox.accept_dialog.no_rooms")).toBeInTheDocument();
    });
    await fireEvent.click(submitButton());
    await waitFor(() => {
      expect(mockAcceptInboxDevice).toHaveBeenCalledWith("0009ABCD", "", undefined);
    });
  });
});
