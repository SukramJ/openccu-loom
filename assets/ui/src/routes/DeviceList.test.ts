// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";

// The deviceStore is a module-level singleton; mock the whole module so
// we can control its state without touching any real API or WebSocket.
let mockItems: unknown[] = [];
let mockLoading = false;
let mockError: string | null = null;
let mockLastLoaded: Date | null = null;

vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    get items() {
      return mockItems;
    },
    get loading() {
      return mockLoading;
    },
    get error() {
      return mockError;
    },
    get lastLoaded() {
      return mockLastLoaded;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
    ensureStream: vi.fn(),
    close: vi.fn(),
  },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    refreshDevices: vi.fn().mockResolvedValue(undefined),
    updateFirmware: vi.fn(),
    setDeviceRooms: vi.fn(),
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

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// DeviceCard internally uses maintenanceStore and Icon; stub them out.
vi.mock("$lib/stores/maintenance.svelte", () => ({
  maintenanceStore: { bind: vi.fn(), all: () => ({}) },
}));

vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn(),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import type { DeviceSummary } from "$lib/api/types";
import DeviceList from "./DeviceList.svelte";

function makeDevice(overrides: Partial<DeviceSummary> = {}): DeviceSummary {
  return {
    address: "ABC123",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF-ABC123",
    model: "HmIP-PSM",
    name: "Test device",
    available: true,
    channels_count: 2,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockItems = [];
  mockLoading = false;
  mockError = null;
  mockLastLoaded = null;
});

afterEach(() => {
  cleanup();
});

describe("DeviceList — empty and loading states", () => {
  it("shows loading text when loading=true and items is empty", () => {
    mockLoading = true;
    mockItems = [];
    const { getByText } = render(DeviceList);
    expect(getByText("devices.loading")).toBeTruthy();
  });

  it("shows empty text when not loading and no items match", () => {
    mockLoading = false;
    mockItems = [];
    const { getByText } = render(DeviceList);
    expect(getByText("devices.empty")).toBeTruthy();
  });

  it("shows an error banner when the store reports an error", () => {
    mockError = "connection refused";
    mockItems = [];
    const { container } = render(DeviceList);
    // The error text is interpolated via t("devicelist.load_error", …);
    // our mock t() returns the key. The component renders the key.
    const banner = container.querySelector(".text-red-800, .text-red-200");
    expect(banner).not.toBeNull();
  });
});

describe("DeviceList — renders device cards", () => {
  it("renders one card per device when items are populated", () => {
    mockItems = [
      makeDevice({ address: "DEV1", name: "Device One" }),
      makeDevice({ address: "DEV2", name: "Device Two" }),
    ];
    const { getAllByRole } = render(DeviceList);
    // Each DeviceCard renders an <h3> heading.
    const headings = getAllByRole("heading", { level: 3 });
    expect(headings).toHaveLength(2);
  });

  it("shows the page title", () => {
    const { getByRole } = render(DeviceList);
    expect(getByRole("heading", { level: 1 }).textContent).toContain(
      "devices.title",
    );
  });
});

describe("DeviceList — search filter", () => {
  it("hides cards that do not match the search query", async () => {
    mockItems = [
      makeDevice({ address: "MATCH1", name: "Alpha lamp" }),
      makeDevice({ address: "OTHER2", name: "Beta switch" }),
    ];
    const { getAllByRole, getByPlaceholderText } = render(DeviceList);

    const searchBox = getByPlaceholderText("devicelist.search_placeholder");
    await fireEvent.input(searchBox, { target: { value: "Alpha" } });

    const headings = getAllByRole("heading", { level: 3 });
    // Only the matching device should be visible.
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Alpha lamp");
  });

  it("shows all cards when search is cleared", async () => {
    mockItems = [
      makeDevice({ address: "D1", name: "Alpha" }),
      makeDevice({ address: "D2", name: "Beta" }),
    ];
    const { getAllByRole, getByPlaceholderText } = render(DeviceList);

    const searchBox = getByPlaceholderText("devicelist.search_placeholder");
    await fireEvent.input(searchBox, { target: { value: "Alpha" } });
    await fireEvent.input(searchBox, { target: { value: "" } });

    const headings = getAllByRole("heading", { level: 3 });
    expect(headings).toHaveLength(2);
  });
});

describe("DeviceList — availability filter", () => {
  it("renders only available devices when the store only has available ones", () => {
    // The $derived filter runs on every render; supplying only available
    // devices verifies that the rendered output matches the filtered set.
    mockItems = [
      makeDevice({ address: "AV1", name: "Reachable", available: true }),
    ];
    const { getAllByRole } = render(DeviceList);
    const headings = getAllByRole("heading", { level: 3 });
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Reachable");
  });

  it("renders unavailable devices when they are in the store", () => {
    mockItems = [
      makeDevice({ address: "B1", name: "Online", available: true }),
      makeDevice({ address: "B2", name: "Unreachable", available: false }),
    ];
    const { getAllByRole } = render(DeviceList);
    const headings = getAllByRole("heading", { level: 3 });
    // Both appear by default (filter = 'all').
    expect(headings).toHaveLength(2);
    const names = headings.map((h) => h.textContent ?? "");
    expect(names.some((n) => n.includes("Unreachable"))).toBe(true);
  });

  it("renders the availability select with the correct options", () => {
    mockItems = [];
    const { getByTitle } = render(DeviceList);
    const select = getByTitle("devicelist.availability") as HTMLSelectElement;
    const opts = Array.from(select.options).map((o) => o.value);
    expect(opts).toContain("all");
    expect(opts).toContain("available");
    expect(opts).toContain("unavailable");
  });
});

describe("DeviceList — update-only filter", () => {
  it("shows only devices with updates when the checkbox is checked", async () => {
    mockItems = [
      makeDevice({ address: "U1", name: "Has update", update_available: true }),
      makeDevice({
        address: "U2",
        name: "No update",
        update_available: false,
      }),
    ];
    const { getAllByRole, container } = render(DeviceList);

    // The "update available" checkbox is the first checkbox without
    // aria-label="Select device" (that belongs to DeviceCard).
    const checkboxes = Array.from(
      container.querySelectorAll('input[type="checkbox"]'),
    ) as HTMLInputElement[];
    // The update-only checkbox comes before any DeviceCard checkboxes.
    const updateCheck = checkboxes[0];
    await fireEvent.click(updateCheck);

    const headings = getAllByRole("heading", { level: 3 });
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Has update");
  });
});

describe("DeviceList — device count footer", () => {
  it("renders the count footer when items are present", () => {
    mockItems = [
      makeDevice({ address: "C1", name: "First" }),
      makeDevice({ address: "C2", name: "Second" }),
    ];
    const { container } = render(DeviceList);
    // The count paragraph contains the i18n key "devicelist.count".
    const countP = Array.from(container.querySelectorAll("p")).find((p) =>
      p.textContent?.includes("devicelist.count"),
    );
    expect(countP).not.toBeUndefined();
  });
});

describe("DeviceList — last-updated footer", () => {
  it("shows the lastLoaded timestamp when available", () => {
    mockLastLoaded = new Date("2026-06-16T10:00:00Z");
    const { container } = render(DeviceList);
    const el = Array.from(container.querySelectorAll("p")).find((p) =>
      p.textContent?.includes("devicelist.last_updated"),
    );
    expect(el).not.toBeUndefined();
  });

  it("shows common.loading when lastLoaded is null", () => {
    mockLastLoaded = null;
    const { getByText } = render(DeviceList);
    expect(getByText("common.loading")).toBeTruthy();
  });
});
