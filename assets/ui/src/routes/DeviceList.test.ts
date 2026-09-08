// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import { deviceListFilters } from "$lib/stores/deviceListFilters.svelte";

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

// DeviceList reads centralStore.items for the per-central filter chips, the
// "still initializing" banner and the reconciliation of a persisted central
// filter. Most cases leave the fleet empty, which keeps those paths inert.
let mockCentrals: unknown[] = [];

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    get items() {
      return mockCentrals;
    },
    loading: false,
    error: null,
    refresh: vi.fn().mockResolvedValue(undefined),
    ensureStream: vi.fn(),
    byName: vi.fn(() => undefined),
    close: vi.fn(),
  },
}));

const { mockListChannels } = vi.hoisted(() => ({ mockListChannels: vi.fn() }));

vi.mock("$lib/api/client", () => ({
  api: {
    refreshDevices: vi.fn().mockResolvedValue(undefined),
    updateFirmware: vi.fn(),
    setDeviceRooms: vi.fn(),
    listChannels: (...args: unknown[]) => mockListChannels(...args),
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

// areasStore drives the (hideable) Area filter select. Empty by default so
// existing tests above never see an extra select; the area-filter describe
// block below opts in per test.
let mockAreas: { id: string; name: string }[] = [];
let mockAreaIdOf: (central: string, room: string) => string | undefined = () => undefined;

vi.mock("$lib/stores/areas.svelte", () => ({
  areasStore: {
    get areas() {
      return mockAreas;
    },
    ensureLoaded: vi.fn(),
    areaIdOf: (central: string, room: string) => mockAreaIdOf(central, room),
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
  // Interpolation is kept: the per-column filter labels differ only by their
  // `column` variable, so a mock that drops vars makes every filter control
  // in the table indistinguishable.
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${Object.values(vars).join(",")}` : key,
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
    has_sub_devices: false,
    firmware: { Current: "1.4.8", Available: "1.4.8", Updatable: false, UpdateState: "UP_TO_DATE" },
    availability: { IsReachable: true, LastUpdated: "2026-01-01T00:00:00Z", BatteryLevel: null, LowBattery: null, SignalStrength: -62 },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockItems = [];
  mockCentrals = [];
  mockLoading = false;
  mockError = null;
  mockLastLoaded = null;
  mockAreas = [];
  mockAreaIdOf = () => undefined;
  // The list filter/sort state lives in a module-scoped store (it must
  // survive navigation in the app), so reset it between tests to avoid a
  // search term from one test leaking into the next.
  Object.assign(deviceListFilters, {
    filter: "",
    availability: "all",
    updateOnly: false,
    roomFilter: "",
    centralFilter: "",
    areaFilter: "",
    sortColumn: "name",
    sortAsc: true,
    groupByInterface: true,
  });
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

  it("shows an error surface when the store reports an error", () => {
    mockError = "connection refused";
    mockItems = [];
    const { container } = render(DeviceList);
    // The error now renders through the shared <ErrorState> component, which
    // styles the message with `text-red-600` (dark: `text-red-400`).
    const errorSurface = container.querySelector(".text-red-600, .text-red-400");
    expect(errorSurface).not.toBeNull();
  });
});

// The device list is a table now: a device is a <tbody> row, not a card with
// an <h3>. Rows carry the device name and address, so counting body rows is
// the direct replacement for counting card headings.
function deviceRows(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll("tbody tr")) as HTMLElement[];
}

describe("DeviceList — renders device rows", () => {
  it("renders one row per device when items are populated", () => {
    mockItems = [
      makeDevice({ address: "DEV1", name: "Device One" }),
      makeDevice({ address: "DEV2", name: "Device Two" }),
    ];
    const { container } = render(DeviceList);
    expect(deviceRows(container)).toHaveLength(2);
  });

  it("shows the page title", () => {
    const { getByRole } = render(DeviceList);
    expect(getByRole("heading", { level: 1 }).textContent).toContain(
      "devices.title",
    );
  });
});

describe("DeviceList — search filter", () => {
  it("hides rows that do not match the search query", async () => {
    mockItems = [
      makeDevice({ address: "MATCH1", name: "Alpha lamp" }),
      makeDevice({ address: "OTHER2", name: "Beta switch" }),
    ];
    const { container, getByPlaceholderText } = render(DeviceList);

    const searchBox = getByPlaceholderText("devicelist.search_placeholder");
    await fireEvent.input(searchBox, { target: { value: "Alpha" } });

    const rows = deviceRows(container);
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("Alpha lamp");
  });

  it("shows every row again when search is cleared", async () => {
    mockItems = [
      makeDevice({ address: "D1", name: "Alpha" }),
      makeDevice({ address: "D2", name: "Beta" }),
    ];
    const { container, getByPlaceholderText } = render(DeviceList);

    const searchBox = getByPlaceholderText("devicelist.search_placeholder");
    await fireEvent.input(searchBox, { target: { value: "Alpha" } });
    await fireEvent.input(searchBox, { target: { value: "" } });

    expect(deviceRows(container)).toHaveLength(2);
  });
});

describe("DeviceList — availability filter", () => {
  it("renders only available devices when the store only has available ones", () => {
    // The $derived filter runs on every render; supplying only available
    // devices verifies that the rendered output matches the filtered set.
    mockItems = [
      makeDevice({ address: "AV1", name: "Reachable", available: true }),
    ];
    const { container } = render(DeviceList);
    const headings = deviceRows(container);
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Reachable");
  });

  it("renders unavailable devices when they are in the store", () => {
    mockItems = [
      makeDevice({ address: "B1", name: "Online", available: true }),
      makeDevice({ address: "B2", name: "Unreachable", available: false }),
    ];
    const { container } = render(DeviceList);
    const headings = deviceRows(container);
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
    const { container } = render(DeviceList);

    // The "update available" checkbox is the first checkbox without
    // aria-label="Select device" (that belongs to DeviceCard).
    const checkboxes = Array.from(
      container.querySelectorAll('input[type="checkbox"]'),
    ) as HTMLInputElement[];
    // The update-only checkbox comes before any DeviceCard checkboxes.
    const updateCheck = checkboxes[0];
    await fireEvent.click(updateCheck);

    const headings = deviceRows(container);
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Has update");
  });
});

describe("DeviceList — area filter", () => {
  it("hides the area select entirely when no areas are defined", () => {
    mockAreas = [];
    const { queryByTitle } = render(DeviceList);
    expect(queryByTitle("devicelist.area")).toBeNull();
  });

  it("shows the area select once areas exist, with an 'all areas' option", () => {
    mockAreas = [{ id: "a1", name: "Upstairs" }];
    const { getByTitle } = render(DeviceList);
    const select = getByTitle("devicelist.area") as HTMLSelectElement;
    const opts = Array.from(select.options).map((o) => o.value);
    expect(opts).toEqual(["", "a1"]);
  });

  // The DeviceList component seeds its local `areaFilter` $state from the
  // persisted `deviceListFilters` store on init (mirrors how the search/
  // availability/room filters are exercised via that same seed elsewhere
  // in this suite) — pre-setting it here is equivalent to the operator
  // picking a value in the (real-browser-only) native <select>, without
  // depending on jsdom/happy-dom's incomplete `:checked` support that
  // Svelte 5's `bind:value` for <select> relies on.
  it("narrows to devices whose central+room resolves to the selected area", () => {
    mockAreas = [{ id: "upstairs", name: "Upstairs" }];
    mockAreaIdOf = (central, room) =>
      central === "ccu1" && room === "Bedroom" ? "upstairs" : undefined;
    mockItems = [
      makeDevice({ address: "D1", name: "In area", central: "ccu1", rooms: ["Bedroom"] }),
      makeDevice({ address: "D2", name: "Out of area", central: "ccu1", rooms: ["Kitchen"] }),
    ];
    deviceListFilters.areaFilter = "upstairs";

    const { container } = render(DeviceList);
    const headings = deviceRows(container);
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("In area");
  });

  it("never merges the same room name across two different centrals", () => {
    mockAreas = [{ id: "upstairs", name: "Upstairs" }];
    mockAreaIdOf = (central, room) =>
      central === "ccu1" && room === "Bedroom" ? "upstairs" : undefined;
    mockItems = [
      makeDevice({ address: "D1", name: "CCU1 bedroom", central: "ccu1", rooms: ["Bedroom"] }),
      makeDevice({ address: "D2", name: "CCU2 bedroom", central: "ccu2", rooms: ["Bedroom"] }),
    ];
    deviceListFilters.areaFilter = "upstairs";

    const { container } = render(DeviceList);
    const headings = deviceRows(container);
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("CCU1 bedroom");
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

describe("DeviceList — persisted central filter", () => {
  it("clears a filter naming a CCU that is no longer configured", async () => {
    // The operator filtered to "ccu2" and then removed (or renamed) that CCU
    // in Settings. The select that could clear the filter is only rendered
    // while two centrals have devices, so without reconciliation the list
    // stays empty with no visible cause and no way back short of clearing
    // localStorage.
    mockCentrals = [{ name: "ccu1", readiness: { ready: true } }];
    mockItems = [makeDevice({ address: "D1", name: "Still here", central: "ccu1" })];
    deviceListFilters.centralFilter = "ccu2";

    const { container } = render(DeviceList);

    const headings = await waitFor(() => {
      const rows = deviceRows(container);
      expect(rows.length).toBeGreaterThan(0);
      return rows;
    });
    expect(headings).toHaveLength(1);
    expect(headings[0].textContent).toContain("Still here");
    expect(deviceListFilters.centralFilter).toBe("");
  });

  it("keeps the filter while the named CCU is still configured but not yet loaded", () => {
    // Cold start: the CCU is configured and inside its readiness-gated
    // bring-up, so it has no devices yet. Dropping the filter here would
    // silently widen the view the operator chose.
    mockCentrals = [
      { name: "ccu1", readiness: { ready: true } },
      { name: "ccu2", readiness: { ready: false } },
    ];
    mockItems = [makeDevice({ address: "D1", name: "CCU1 device", central: "ccu1" })];
    deviceListFilters.centralFilter = "ccu2";

    render(DeviceList);

    expect(deviceListFilters.centralFilter).toBe("ccu2");
  });
});

// The card grid is gone: the table is the only device-list layout, and it
// carries a per-column filter row plus a channel table per expanded row. Both
// are wired here rather than only in DataTable's own tests, because a table
// that supports filtering proves nothing about a device list that forgot to
// turn it on.
describe("DeviceList — column filters", () => {
  it("narrows the rows through a column filter", async () => {
    mockItems = [
      makeDevice({ address: "DEV1", name: "Alpha lamp", model: "HmIP-BSM" }),
      makeDevice({ address: "DEV2", name: "Beta switch", model: "HmIP-PS" }),
    ];
    const { container, getByLabelText } = render(DeviceList);
    expect(deviceRows(container)).toHaveLength(2);

    await fireEvent.input(getByLabelText("datatable.filter_by:devicelist.col.name"), {
      target: { value: "Alpha" },
    });
    const rows = deviceRows(container);
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("Alpha lamp");
  });
});

describe("DeviceList — expandable channels", () => {
  beforeEach(() => {
    mockListChannels.mockReset();
  });

  function channel(number: number, name: string) {
    return {
      address: `DEV1:${number}`,
      number,
      name,
      type: "SWITCH",
      paramset_key: "VALUES",
      data_points_count: 1,
    };
  }

  // Fetching every device's channels up front would cost a request per row
  // for content nobody opened.
  it("fetches a device's channels only when its row is expanded", async () => {
    mockListChannels.mockResolvedValue([channel(1, "Channel one")]);
    mockItems = [makeDevice({ address: "DEV1", name: "Alpha" })];
    const { container, getAllByRole } = render(DeviceList);

    expect(mockListChannels).not.toHaveBeenCalled();

    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    await waitFor(() => expect(mockListChannels).toHaveBeenCalledWith("DEV1"));
    await waitFor(() => {
      expect(container.textContent).toContain("Channel one");
    });
  });

  it("does not re-fetch when the same row is expanded twice", async () => {
    mockListChannels.mockResolvedValue([channel(1, "Channel one")]);
    mockItems = [makeDevice({ address: "DEV1", name: "Alpha" })];
    const { getAllByRole, getByRole } = render(DeviceList);

    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    await waitFor(() => expect(mockListChannels).toHaveBeenCalledTimes(1));
    await fireEvent.click(getByRole("button", { name: "datatable.collapse" }));
    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    expect(mockListChannels).toHaveBeenCalledTimes(1);
  });

  // The device page owns the real channel surface, so a failure here shows a
  // retryable state in the row rather than toasting over the whole list.
  it("shows a retryable error in the row when the channel load fails", async () => {
    mockListChannels.mockRejectedValue(new Error("boom"));
    mockItems = [makeDevice({ address: "DEV1", name: "Alpha" })];
    const { container, getAllByRole } = render(DeviceList);

    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    await waitFor(() => {
      expect(container.textContent).toContain("devicelist.channels_failed");
    });
  });

  // The device-level channel and :0 have their own surfaces on the device
  // page and nothing to configure from a list row.
  it("leaves out the device-level and :0 channels", async () => {
    mockListChannels.mockResolvedValue([
      { address: "DEV1", number: 0, type: "MAINTENANCE", paramset_key: "VALUES", data_points_count: 0 },
      channel(0, "Maintenance"),
      channel(1, "Channel one"),
    ]);
    mockItems = [makeDevice({ address: "DEV1", name: "Alpha" })];
    const { container, getAllByRole } = render(DeviceList);

    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    await waitFor(() => expect(container.textContent).toContain("Channel one"));
    expect(container.textContent).not.toContain("Maintenance");
  });
});
