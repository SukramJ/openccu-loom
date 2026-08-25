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

const mockListCentralsV2 = vi.fn();
const mockGetEnergy = vi.fn();

// ---------------------------------------------------------------------------
// Module mocks — hoisted before any import of the component
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentralsV2(...args),
  },
  getEnergy: (...args: unknown[]) => mockGetEnergy(...args),
  HistoryDisabledError: class HistoryDisabledError extends Error {
    constructor() {
      super("history feature not enabled");
      this.name = "HistoryDisabledError";
    }
  },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

// ---------------------------------------------------------------------------
// Component under test
// ---------------------------------------------------------------------------

import Energy from "./Energy.svelte";

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

const CENTRALS = [{ name: "ccu1", host: "192.0.2.29", enabled: true, interfaces: [] }];

// Figures are chosen so every rendered "<n> kWh" string is unique across the
// whole view (device rows + range totals) — otherwise Testing Library's
// getByText throws on an ambiguous match.
const ENERGY_RESPONSE = {
  group: "day" as const,
  from: "2026-06-01T00:00:00Z",
  to: "2026-07-01T00:00:00Z",
  devices: [
    {
      address: "00021BE9957782",
      name: "Bücherregal",
      buckets: [
        {
          ts: "2026-06-30T00:00:00Z",
          consumed_wh: 9123,
          feed_in_wh: 0,
          avg_power_w: 18.2,
          peak_power_w: 240,
          reset: false,
        },
      ],
      total_consumed_wh: 9123,
      total_feed_in_wh: 0,
    },
    {
      address: "00021BE9957783",
      name: "Solar Feed",
      buckets: [
        {
          ts: "2026-06-30T00:00:00Z",
          consumed_wh: 500,
          feed_in_wh: 8000,
          avg_power_w: 5,
          peak_power_w: 20,
          reset: true,
        },
      ],
      total_consumed_wh: 500,
      total_feed_in_wh: 8000,
    },
  ],
  total_consumed_wh: 15000,
  total_feed_in_wh: 20000,
};

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

beforeEach(() => {
  vi.clearAllMocks();
  mockListCentralsV2.mockResolvedValue(CENTRALS);
});

afterEach(() => {
  cleanup();
});

// ---------------------------------------------------------------------------
// 1. Wh → kWh conversion
// ---------------------------------------------------------------------------

describe("Energy — Wh to kWh conversion", () => {
  it("renders the range totals converted from Wh to kWh", async () => {
    mockGetEnergy.mockResolvedValue(ENERGY_RESPONSE);
    render(Energy);
    await waitFor(() => {
      // total_consumed_wh 15000 / 1000 = 15.00 kWh
      expect(screen.getByText("15.00 kWh")).toBeInTheDocument();
      // total_feed_in_wh 20000 / 1000 = 20.00 kWh
      expect(screen.getByText("20.00 kWh")).toBeInTheDocument();
    });
  });

  it("renders a per-device consumed total converted from Wh to kWh", async () => {
    mockGetEnergy.mockResolvedValue(ENERGY_RESPONSE);
    render(Energy);
    await waitFor(() => {
      // Bücherregal total_consumed_wh 9123 / 1000 = 9.12 kWh
      expect(screen.getByText("9.12 kWh")).toBeInTheDocument();
    });
  });
});

// ---------------------------------------------------------------------------
// 2. Reset-badge logic
// ---------------------------------------------------------------------------

describe("Energy — reset badge", () => {
  it("shows exactly one reset badge and the footnote when one device has a reset bucket", async () => {
    mockGetEnergy.mockResolvedValue(ENERGY_RESPONSE);
    render(Energy);
    await waitFor(() => {
      expect(screen.getByText("Solar Feed")).toBeInTheDocument();
    });
    // Only "Solar Feed" carries a bucket with reset: true.
    expect(screen.getAllByText("energy.col.reset")).toHaveLength(1);
    expect(screen.getByText("energy.reset_note")).toBeInTheDocument();
  });

  it("omits the reset badge and footnote when no bucket reset", async () => {
    const noReset = {
      ...ENERGY_RESPONSE,
      devices: [ENERGY_RESPONSE.devices[0]],
      total_consumed_wh: 9123,
      total_feed_in_wh: 0,
    };
    mockGetEnergy.mockResolvedValue(noReset);
    render(Energy);
    await waitFor(() => {
      expect(screen.getByText("Bücherregal")).toBeInTheDocument();
    });
    expect(screen.queryByText("energy.col.reset")).toBeNull();
    expect(screen.queryByText("energy.reset_note")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 3. Feature-off (404 → HistoryDisabledError) empty state
// ---------------------------------------------------------------------------

describe("Energy — feature-off state", () => {
  it("shows the disabled-title empty state when getEnergy throws HistoryDisabledError", async () => {
    const { HistoryDisabledError } = await import("$lib/api/client");
    mockGetEnergy.mockRejectedValue(new HistoryDisabledError());

    render(Energy);

    await waitFor(() => {
      expect(screen.getByText("energy.disabled_title")).toBeInTheDocument();
    });
  });

  it("shows a generic error state (not the disabled state) for other failures", async () => {
    mockGetEnergy.mockRejectedValue(new Error("network error"));

    render(Energy);

    await waitFor(
      () => {
        expect(screen.getByText(/network error/)).toBeInTheDocument();
      },
      { timeout: 3000 },
    );
    expect(screen.queryByText("energy.disabled_title")).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// 4. Out-of-order responses
// ---------------------------------------------------------------------------

describe("Energy — overlapping loads", () => {
  it("keeps the newest selection's data when an earlier response lands last", async () => {
    // The toolbar stays interactive while a query is in flight and the cost
    // of a query varies by orders of magnitude with the range, so a slow
    // earlier response can arrive after a fast later one. Without a
    // generation guard it overwrites the figures the operator is looking at
    // while the controls report the newer selection.
    let resolveSlow!: (value: unknown) => void;
    const slow = new Promise((resolve) => {
      resolveSlow = resolve;
    });
    const fresh = {
      ...ENERGY_RESPONSE,
      devices: [ENERGY_RESPONSE.devices[0]],
      total_consumed_wh: 42000,
      total_feed_in_wh: 0,
    };

    mockGetEnergy.mockReturnValueOnce(slow).mockResolvedValueOnce(fresh);
    render(Energy);

    // The first (slow) query is in flight; switch the range preset, which
    // starts the second one.
    const rangeButton = await waitFor(() =>
      screen.getByText("energy.preset.24h"),
    );
    await fireEvent.click(rangeButton);

    await waitFor(() => {
      expect(screen.getByText("42.00 kWh")).toBeInTheDocument();
    });

    // Now let the superseded response land.
    resolveSlow(ENERGY_RESPONSE);
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.getByText("42.00 kWh")).toBeInTheDocument();
    expect(screen.queryByText("15.00 kWh")).toBeNull();
  });
});
