// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns
// ---------------------------------------------------------------------------

const mockGetSystemCCUs = vi.fn();
const mockListDevices = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component / the real
// deviceStore. Fleet.svelte drives device counts off the real
// `deviceStore` singleton (not a mock) so the test exercises the actual
// `central === ccu.name` filter; the store's own dependencies (api,
// the WS event bus, authStore) are stubbed the same way
// `lib/stores/devices.test.ts` stubs them.
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    getSystemCCUs: (...args: unknown[]) => mockGetSystemCCUs(...args),
    listDevices: (...args: unknown[]) => mockListDevices(...args),
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

vi.mock("$lib/stores/events.svelte", () => ({
  subscribe: vi.fn(() => () => {}),
}));

vi.mock("$lib/stores/auth.svelte", () => ({
  authStore: { probe: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

// ---------------------------------------------------------------------------
// Component + store under test
// ---------------------------------------------------------------------------

import Fleet from "./Fleet.svelte";
import { deviceStore } from "$lib/stores/devices.svelte";

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

const CCUS = [
  {
    name: "ccu-online",
    host: "172.18.4.10",
    available: true,
    model: "CCU3",
    version: "3.75.7",
    hostname: "ccu-online.local",
    serial: "SERIAL0001",
    url: "https://172.18.4.10",
    is_ha_app: false,
    configured_interfaces: ["HmIP-RF", "BidCos-RF"],
  },
  {
    name: "ccu-offline",
    host: "172.18.4.11",
    available: false,
    is_ha_app: false,
    configured_interfaces: ["HmIP-RF"],
  },
];

// Three devices on "ccu-online", one on "ccu-offline", one on an unrelated
// central — proves the per-card count is a strict `central === ccu.name`
// filter, not a raw total.
const DEVICES = [
  { address: "AAA0000001", central: "ccu-online" },
  { address: "AAA0000002", central: "ccu-online" },
  { address: "AAA0000003", central: "ccu-online" },
  { address: "BBB0000001", central: "ccu-offline" },
  { address: "CCC0000001", central: "ccu-other" },
];

function devicesPage(items: { address: string; central: string }[]) {
  return { items, total: items.length };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
  deviceStore.close();
});

// ---------------------------------------------------------------------------
// 1. Online/offline badge mapping
// ---------------------------------------------------------------------------

describe("Fleet — availability badge", () => {
  it("renders an online badge for an available CCU and an offline badge for an unavailable one", async () => {
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockResolvedValue(devicesPage(DEVICES));

    const { container } = render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
      expect(screen.getByText("ccu-offline")).toBeInTheDocument();
    });

    // Cards are sorted by name ("ccu-offline" < "ccu-online"), rendered as
    // the direct children of the responsive grid.
    const cards = container.querySelectorAll(".grid > div");
    expect(cards).toHaveLength(2);
    expect(cards[0].textContent).toContain("ccu-offline");
    expect(cards[0].textContent).toContain("fleet.status.offline");
    expect(cards[0].textContent).not.toContain("fleet.status.online");
    expect(cards[1].textContent).toContain("ccu-online");
    expect(cards[1].textContent).toContain("fleet.status.online");
  });
});

// ---------------------------------------------------------------------------
// 2. Per-central device-count derivation
// ---------------------------------------------------------------------------

describe("Fleet — per-central device count", () => {
  it("counts only the devices whose `central` matches the CCU name", async () => {
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockResolvedValue(devicesPage(DEVICES));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
    });

    const deviceLabels = screen.getAllByText("fleet.field.devices");
    expect(deviceLabels).toHaveLength(2);
    // Sorted order: "ccu-offline" (1 device) then "ccu-online" (3 devices).
    expect(deviceLabels[0].nextElementSibling?.textContent?.trim()).toBe("1");
    expect(deviceLabels[1].nextElementSibling?.textContent?.trim()).toBe("3");
  });

  it("shows 0 devices for a CCU with no matching devices", async () => {
    mockGetSystemCCUs.mockResolvedValue([CCUS[1]]);
    mockListDevices.mockResolvedValue(devicesPage([]));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-offline")).toBeInTheDocument();
    });

    const deviceLabel = screen.getByText("fleet.field.devices");
    expect(deviceLabel.nextElementSibling?.textContent?.trim()).toBe("0");
  });
});

// ---------------------------------------------------------------------------
// 3. Empty state (no CCUs configured)
// ---------------------------------------------------------------------------

describe("Fleet — empty state", () => {
  it("shows the empty-state message when no CCUs are configured", async () => {
    mockGetSystemCCUs.mockResolvedValue([]);
    mockListDevices.mockResolvedValue(devicesPage([]));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("fleet.empty")).toBeInTheDocument();
    });
    expect(screen.queryByText("fleet.field.devices")).toBeNull();
  });
});
