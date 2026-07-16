// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup } from "@testing-library/svelte";

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
vi.mock("$lib/api/client", () => ({
  api: {
    getDevice: (...args: unknown[]) => mockGetDevice(...args),
    updateFirmware: vi.fn(),
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

  it("explains that the image is still awaiting transfer instead of offering an impossible install", async () => {
    const { findByText, queryByRole } = render(FirmwareList);

    await findByText("Awaiting transfer to the device");
    expect(queryByRole("button", { name: "Update" })).toBeNull();
  });
});

// The gateway RF module (RPI-RF-MOD) has no OTA image of its own — it is
// updated through the CCU firmware — so the CCU reports the all-zero
// placeholder "0.0.0" as its available version. A placeholder is not a
// pending update: rendering "Update available" / "Awaiting transfer"
// next to 4.4.22 → 0.0.0 contradicts the version columns.
describe("FirmwareList zero-version placeholder", () => {
  const rfMod = {
    address: "001F5A4993D962",
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
