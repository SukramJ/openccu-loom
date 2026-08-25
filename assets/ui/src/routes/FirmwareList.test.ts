// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

// The deviceStore is a module-level singleton; mock the whole module so
// we can control its state without touching any real API or WebSocket.
let mockItems: unknown[] = [];

vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    get items() {
      return mockItems;
    },
    loading: false,
    error: null,
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

const mockGetDevice = vi.fn();
const mockRefreshFirmwareData = vi.fn();
const mockListInterfaces = vi.fn().mockResolvedValue([]);
const mockUpdateFirmware = vi.fn().mockResolvedValue(undefined);
vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    updateFirmware: (...args: unknown[]) => mockUpdateFirmware(...args),
    refreshFirmwareData: (...args: unknown[]) => mockRefreshFirmwareData(...args),
    listInterfaces: (...args: unknown[]) => mockListInterfaces(...args),
  },
  ApiError: class ApiError extends Error {
    constructor(
      public readonly status: number,
      public readonly body: unknown,
      message: string,
    ) {
      super(message);
    }
  },
}));

// confirmStore.ask defaults to "cancelled" so the duty-cycle-warning tests
// can inspect the dialog body without also exercising the post-confirm
// update flow.
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

import { toastStore } from "$lib/stores/toast.svelte";
import { deviceStore } from "$lib/stores/devices.svelte";
import { confirmStore } from "$lib/stores/confirm.svelte";
import { ApiError } from "$lib/api/client";
import FirmwareList from "./FirmwareList.svelte";

// A pairing-capable HmIP device where the CCU knows a newer firmware
// (NEW_FIRMWARE_AVAILABLE) that has not yet been delivered to the
// device: the gated `update_available` flag is false (not installable
// yet, mirroring the reference stack's update-entity semantics), but
// the overview must still tell the operator that an update exists —
// showing "Up to date" next to 1.2.2 → 1.4.10 is a contradiction.
const wsm = {
  address: "0052E409A90362",
  name: "Bewässerungsventil TKL",
  model: "HmIP-WSM",
  interface_id: "ccu-HmIP-RF",
  central: "",
  updatable: true,
  update_available: false,
};

beforeEach(() => {
  mockItems = [wsm];
  mockGetDevice.mockResolvedValue({
    address: wsm.address,
    update_available: false,
    firmware: {
      Current: "1.2.2",
      Available: "1.4.10",
      Updatable: true,
      UpdateState: "NEW_FIRMWARE_AVAILABLE",
    },
  });
  mockRefreshFirmwareData.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  // Defensive: this Node exposes no localStorage global outside the
  // happy-dom window; loadLS/saveLS are try/catch-guarded the same way.
  globalThis.localStorage?.clear?.();
});

describe("FirmwareList", () => {
  it("shows the CCU update state, never 'up to date', when a newer firmware exists but is not yet installable", async () => {
    const { findByText, queryAllByText } = render(FirmwareList);

    await findByText("1.4.10");
    // The status column renders the real CCU lifecycle state.
    await findByText("Update available");
    // Neither the status badge nor the action column may claim the
    // device is current while 1.2.2 → 1.4.10 is on screen.
    expect(queryAllByText("Up to date")).toEqual([]);
  });

  // The CCU's UpdateState reaches the badge unvalidated, so a value
  // outside the catalogue has to read as the raw token an operator can
  // look up — not as the dotted catalogue key.
  it("falls back to the raw CCU token for an unknown update state", async () => {
    mockGetDevice.mockResolvedValue({
      address: wsm.address,
      update_available: false,
      firmware: {
        Current: "1.2.2",
        Available: "1.4.10",
        Updatable: true,
        UpdateState: "PERFORMING_UNKNOWN_RITUAL",
      },
    });

    const { findByText, queryByText } = render(FirmwareList);

    await findByText("PERFORMING_UNKNOWN_RITUAL");
    expect(queryByText("firmware.state.PERFORMING_UNKNOWN_RITUAL")).toBeNull();
  });

  it("explains that the image is still awaiting transfer instead of offering an impossible install", async () => {
    const { findByText, queryByRole } = render(FirmwareList);

    await findByText("Awaiting transfer to the device");
    expect(queryByRole("button", { name: "Update" })).toBeNull();
  });
});

describe("FirmwareList reload", () => {
  it("re-fetches firmware details on reload even though they were already cached", async () => {
    const { findByText, getByRole } = render(FirmwareList);

    // Initial mount loads the detail for the one updatable device.
    await findByText("1.4.10");
    expect(mockGetDevice).toHaveBeenCalledTimes(1);

    mockGetDevice.mockClear();
    mockRefreshFirmwareData.mockClear();
    (deviceStore.refresh as ReturnType<typeof vi.fn>).mockClear();

    await fireEvent.click(getByRole("button", { name: "Reload" }));

    // The detail cache must have been dropped: getDevice is called again
    // for the same, already-loaded device.
    await waitFor(() => {
      expect(mockRefreshFirmwareData).toHaveBeenCalledTimes(1);
      expect(deviceStore.refresh).toHaveBeenCalledTimes(1);
      expect(mockGetDevice).toHaveBeenCalledTimes(1);
    });
  });

  it("still refreshes the list and details, and raises a toast, when refreshFirmwareData rejects", async () => {
    const { findByText, getByRole } = render(FirmwareList);

    await findByText("1.4.10");
    mockGetDevice.mockClear();
    mockRefreshFirmwareData.mockClear();
    (deviceStore.refresh as ReturnType<typeof vi.fn>).mockClear();
    mockRefreshFirmwareData.mockRejectedValueOnce(
      new ApiError(404, {}, "not found"),
    );
    const toastErrorSpy = vi.spyOn(toastStore, "error");

    await fireEvent.click(getByRole("button", { name: "Reload" }));

    // An old daemon (missing route, 404) must not block the rest of the
    // reload: the list and per-device details still refresh, and the
    // error surfaces as a toast instead of vanishing silently.
    await waitFor(() => {
      expect(mockRefreshFirmwareData).toHaveBeenCalledTimes(1);
      expect(toastErrorSpy).toHaveBeenCalledWith("404: not found");
      expect(deviceStore.refresh).toHaveBeenCalledTimes(1);
      expect(mockGetDevice).toHaveBeenCalledTimes(1);
    });
  });
});

// The gateway RF module (RPI-RF-MOD) has no OTA image of its own — it is
// updated through the CCU firmware — so the CCU reports the all-zero
// placeholder "0.0.0" as its available version. A placeholder is not a
// pending update: rendering "Update available" / "Awaiting transfer"
// next to 4.4.22 → 0.0.0 contradicts the version columns.
describe("FirmwareList zero-version placeholder", () => {
  const rfMod = {
    address: "001F0123456789",
    name: "Otto-Funkmodul",
    model: "RPI-RF-MOD",
    interface_id: "ccu-HmIP-RF",
    central: "",
    updatable: true,
    update_available: false,
  };

  beforeEach(() => {
    mockItems = [rfMod];
    mockGetDevice.mockResolvedValue({
      address: rfMod.address,
      update_available: false,
      firmware: {
        Current: "4.4.22",
        Available: "0.0.0",
        Updatable: true,
        UpdateState: "UNKNOWN",
      },
    });
  });

  it("treats the all-zero available version as 'no update known', not as a pending transfer", async () => {
    const { findByText, findAllByText, queryByText } = render(FirmwareList);

    await findByText("4.4.22");
    // Status badge and action column both settle on "up to date".
    expect((await findAllByText("Up to date")).length).toBeGreaterThan(0);
    expect(queryByText("Update available")).toBeNull();
    expect(queryByText("Awaiting transfer to the device")).toBeNull();
    // The placeholder never renders as an installable version, and the
    // summary bar does not count the device as having an update.
    expect(queryByText("0.0.0")).toBeNull();
    expect(
      queryByText("1 device(s) have a firmware update available."),
    ).toBeNull();
  });
});

// The CCU WebUI gates device firmware updates on a high BidCos duty
// cycle; OpenCCU-Loom never blocks the update but warns the operator in
// the confirm dialog so a stalled OTA transfer over a saturated radio is
// expected, not surprising. The warning is sourced from GET /interfaces,
// matched to the device via "<central>|<interface_id>".
describe("FirmwareList duty cycle warning", () => {
  const dutyDevice = {
    address: "LEQ0012345",
    name: "Lamp",
    model: "HM-LC-Sw1-Pl",
    interface_id: "BidCos-RF",
    central: "",
    updatable: true,
    update_available: true,
  };

  beforeEach(() => {
    mockItems = [dutyDevice];
    mockGetDevice.mockResolvedValue({
      address: dutyDevice.address,
      update_available: true,
      firmware: {
        Current: "1.0",
        Available: "1.1",
        Updatable: true,
        UpdateState: "READY_FOR_UPDATE",
      },
    });
  });

  it("warns in the confirm dialog when the device's radio interface duty cycle is at or above 80%", async () => {
    mockListInterfaces.mockResolvedValueOnce([
      {
        id: "BidCos-RF",
        central_id: "",
        name: "BidCos-RF",
        interface: "BidCos-RF",
        connected: true,
        duty_cycle: 85,
      },
    ]);
    const { findByRole } = render(FirmwareList);

    await fireEvent.click(await findByRole("button", { name: "Update" }));

    await waitFor(() => {
      expect(confirmStore.ask).toHaveBeenCalledWith(
        expect.objectContaining({
          body: "The radio interface duty cycle is high (85%). The over-the-air transfer may stall until the radio recovers.",
        }),
      );
    });
  });

  it("does not warn when the duty cycle is below the threshold", async () => {
    mockListInterfaces.mockResolvedValueOnce([
      {
        id: "BidCos-RF",
        central_id: "",
        name: "BidCos-RF",
        interface: "BidCos-RF",
        connected: true,
        duty_cycle: 40,
      },
    ]);
    const { findByRole } = render(FirmwareList);

    await fireEvent.click(await findByRole("button", { name: "Update" }));

    await waitFor(() => {
      expect(confirmStore.ask).toHaveBeenCalledWith(
        expect.objectContaining({ body: "" }),
      );
    });
  });

  it("does not warn when the interface carries no duty-cycle reading (HmIP or un-polled BidCos)", async () => {
    mockListInterfaces.mockResolvedValueOnce([
      {
        id: "BidCos-RF",
        central_id: "",
        name: "BidCos-RF",
        interface: "BidCos-RF",
        connected: true,
      },
    ]);
    const { findByRole } = render(FirmwareList);

    await fireEvent.click(await findByRole("button", { name: "Update" }));

    await waitFor(() => {
      expect(confirmStore.ask).toHaveBeenCalledWith(
        expect.objectContaining({ body: "" }),
      );
    });
  });

  it("does not block the update flow when GET /interfaces fails", async () => {
    mockListInterfaces.mockRejectedValueOnce(new Error("network error"));
    const { findByRole } = render(FirmwareList);

    // The best-effort duty-cycle fetch failing must not prevent the
    // update button (or the rest of the overview) from rendering.
    await fireEvent.click(await findByRole("button", { name: "Update" }));

    await waitFor(() => {
      expect(confirmStore.ask).toHaveBeenCalledWith(
        expect.objectContaining({ body: "" }),
      );
    });
  });
});
