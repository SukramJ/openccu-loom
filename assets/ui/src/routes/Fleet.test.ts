// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  cleanup,
  waitFor,
  screen,
  fireEvent,
} from "@testing-library/svelte";

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
  onResync: () => () => {},
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
    auth_enabled: true,
    https_redirect_enabled: false,
    // The CCU reports a third interface the daemon is not configured for —
    // the mismatch the fleet card highlights.
    ccu_interfaces: [
      {
        type: "HmIP-RF",
        address: "HmIP-RF",
        port: 2010,
        url: "http://172.18.4.10:2010",
      },
      { type: "BidCos-RF", address: "BidCos-RF", port: 2001 },
      { type: "CUxD", address: "CUxD", port: 8701 },
    ],
    readiness: {
      phase: "ready",
      ready: true,
      interfaces_loaded: 2,
      interfaces_total: 2,
    },
  },
  {
    // No CCU-sourced facts at all — the pre-first-connect shape.
    name: "ccu-offline",
    host: "172.18.4.11",
    available: false,
    is_ha_app: false,
    configured_interfaces: ["HmIP-RF"],
    auth_enabled: false,
    https_redirect_enabled: false,
    readiness: {
      phase: "waiting_for_ccu",
      ready: false,
      interfaces_loaded: 0,
      interfaces_total: 0,
    },
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

describe("Fleet — readiness badge", () => {
  it("renders a ready badge for a ready CCU and an offline badge for an unavailable one", async () => {
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockResolvedValue(devicesPage(DEVICES));

    const { container } = render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
      expect(screen.getByText("ccu-offline")).toBeInTheDocument();
    });

    // Cards are sorted by name ("ccu-offline" < "ccu-online"), rendered as
    // the direct children of the responsive grid. Badge text comes from
    // CentralStatusBadge.svelte, driven by the (available, readiness) pair.
    const cards = container.querySelectorAll(".grid > div");
    expect(cards).toHaveLength(2);
    expect(cards[0].textContent).toContain("ccu-offline");
    expect(cards[0].textContent).toContain("central.readiness.offline");
    expect(cards[0].textContent).not.toContain("central.readiness.ready");
    expect(cards[1].textContent).toContain("ccu-online");
    expect(cards[1].textContent).toContain("central.readiness.ready");
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
// 2b. deviceStore fetch failure must not read as "0 devices everywhere"
// ---------------------------------------------------------------------------

describe("Fleet — device fetch failure", () => {
  it("renders the shared error state instead of a 0-device grid when the device fetch fails", async () => {
    // deviceStore.refresh() swallows its own fetch errors into
    // deviceStore.error rather than throwing (see devices.svelte.ts),
    // so Promise.all([getSystemCCUs(), deviceStore.refresh()]) resolves
    // normally even though the device list never loaded. Fleet must
    // read deviceStore.error itself instead of rendering every CCU
    // card with a device count of 0.
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockRejectedValue(new Error("network down"));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText(/fleet\.load_error/)).toBeInTheDocument();
    });
    expect(screen.queryByText("ccu-online")).not.toBeInTheDocument();
    expect(screen.queryByText("fleet.field.devices")).not.toBeInTheDocument();
  });

  it("retries both the CCU list and the device list on the error state's retry action", async () => {
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockRejectedValueOnce(new Error("network down"));
    mockListDevices.mockResolvedValue(devicesPage(DEVICES));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText(/fleet\.load_error/)).toBeInTheDocument();
    });

    await fireEvent.click(screen.getByRole("button", { name: "common.reload" }));

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
    });
    expect(screen.queryByText(/fleet\.load_error/)).not.toBeInTheDocument();
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

// ---------------------------------------------------------------------------
// 5. CCU-reported facts: security posture + the CCU's own interface list
// ---------------------------------------------------------------------------

describe("Fleet — CCU security posture", () => {
  it("labels the flags per CCU instead of collapsing them to one state", async () => {
    mockGetSystemCCUs.mockResolvedValue(CCUS);
    mockListDevices.mockResolvedValue(devicesPage(DEVICES));

    const { container } = render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
    });

    // Sorted order: "ccu-offline" first, "ccu-online" second.
    const cards = container.querySelectorAll(".grid > div");
    expect(cards[0].textContent).toContain("fleet.field.auth_enabled.off");
    expect(cards[1].textContent).toContain("fleet.field.auth_enabled.on");
    // Both CCUs have the redirect off — the flags are independent, so the
    // auth flag being on must not pull the redirect label with it.
    expect(cards[1].textContent).toContain("fleet.field.https_redirect.off");
    expect(cards[1].textContent).not.toContain("fleet.field.https_redirect.on");
  });

  it("renders the security block even before the first connect round", async () => {
    mockGetSystemCCUs.mockResolvedValue([CCUS[1]]);
    mockListDevices.mockResolvedValue(devicesPage([]));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-offline")).toBeInTheDocument();
    });

    // The label is always present — a CCU that never answered still gets a
    // row, reading "no authentication" rather than silently dropping out.
    expect(screen.getByText("fleet.field.ccu_security")).toBeInTheDocument();
    expect(
      screen.getByText("fleet.field.auth_enabled.off"),
    ).toBeInTheDocument();
  });
});

describe("Fleet — CCU-reported interface list", () => {
  it("lists the CCU's interfaces with ports and flags the unmanaged one", async () => {
    mockGetSystemCCUs.mockResolvedValue([CCUS[0]]);
    mockListDevices.mockResolvedValue(devicesPage([]));

    const { container } = render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-online")).toBeInTheDocument();
    });

    expect(screen.getByText("fleet.field.ccu_interfaces")).toBeInTheDocument();
    expect(screen.getByText("HmIP-RF:2010")).toBeInTheDocument();
    expect(screen.getByText("BidCos-RF:2001")).toBeInTheDocument();
    expect(screen.getByText("CUxD:8701")).toBeInTheDocument();

    // CUxD is reported by the CCU but not in configured_interfaces, so it
    // carries the unmanaged hint; a managed one falls back to its URL/address.
    const cuxd = screen.getByText("CUxD:8701");
    expect(cuxd.getAttribute("title")).toBe(
      "fleet.field.ccu_interfaces.unmanaged",
    );
    const hmip = screen.getByText("HmIP-RF:2010");
    expect(hmip.getAttribute("title")).toBe("http://172.18.4.10:2010");

    // The daemon's own configured list stays a separate row.
    expect(screen.getByText("fleet.field.interfaces")).toBeInTheDocument();
    expect(container.textContent).toContain("fleet.field.interfaces");
  });

  it("omits the block entirely when the CCU has not reported interfaces yet", async () => {
    mockGetSystemCCUs.mockResolvedValue([CCUS[1]]);
    mockListDevices.mockResolvedValue(devicesPage([]));

    render(Fleet);

    await waitFor(() => {
      expect(screen.getByText("ccu-offline")).toBeInTheDocument();
    });

    expect(
      screen.queryByText("fleet.field.ccu_interfaces"),
    ).not.toBeInTheDocument();
  });
});
