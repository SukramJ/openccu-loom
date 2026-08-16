// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Mutable mock fns — this spec only exercises the reliability +
// values-cache admin panel (U4); everything else on the page gets a
// minimal, non-throwing default so the component mounts cleanly.
// ---------------------------------------------------------------------------

const {
  mockHealth,
  mockListInterfaces,
  mockIncidents,
  mockListLogLevels,
  mockListCaptures,
  mockListRpcRecordings,
  mockDiagnostics,
  mockGetReliability,
  mockGetValuesCacheStats,
  mockResetValuesCache,
  mockToastSuccess,
  mockToastError,
  mockConfirmAsk,
} = vi.hoisted(() => ({
  mockHealth: vi.fn(),
  mockListInterfaces: vi.fn(),
  mockIncidents: vi.fn(),
  mockListLogLevels: vi.fn(),
  mockListCaptures: vi.fn(),
  mockListRpcRecordings: vi.fn(),
  mockDiagnostics: vi.fn(),
  mockGetReliability: vi.fn(),
  mockGetValuesCacheStats: vi.fn(),
  mockResetValuesCache: vi.fn(),
  mockToastSuccess: vi.fn(),
  mockToastError: vi.fn(),
  mockConfirmAsk: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    health: (...args: unknown[]) => mockHealth(...args),
    listInterfaces: (...args: unknown[]) => mockListInterfaces(...args),
    incidents: (...args: unknown[]) => mockIncidents(...args),
    listLogLevels: (...args: unknown[]) => mockListLogLevels(...args),
    listCaptures: (...args: unknown[]) => mockListCaptures(...args),
    listRpcRecordings: (...args: unknown[]) => mockListRpcRecordings(...args),
    diagnostics: (...args: unknown[]) => mockDiagnostics(...args),
    getReliability: (...args: unknown[]) => mockGetReliability(...args),
    getValuesCacheStats: (...args: unknown[]) => mockGetValuesCacheStats(...args),
    resetValuesCache: (...args: unknown[]) => mockResetValuesCache(...args),
    captureDownloadURL: () => "",
    rpcRecordingDownloadUrl: () => "",
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, _body: unknown, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

// Partial catalogue rather than an echo: the status/severity cells resolve
// through t() and fall back to the raw token on a miss, so an always-echoing
// stub would make the two branches indistinguishable.
vi.mock("$lib/i18n", () => {
  const catalog: Record<string, string> = {
    "diagnostics.capture_status.stopped": "Stopped",
    "diagnostics.incident_severity.critical": "Critical",
    "health.status.degraded": "Degraded",
  };
  return { t: (key: string) => catalog[key] ?? key };
});

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: mockConfirmAsk },
}));

import Diagnostics from "./Diagnostics.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockHealth.mockResolvedValue({ status: "healthy", components: [] });
  mockListInterfaces.mockResolvedValue([]);
  mockIncidents.mockResolvedValue([]);
  mockListLogLevels.mockResolvedValue(null);
  mockListCaptures.mockResolvedValue([]);
  mockListRpcRecordings.mockResolvedValue([]);
  mockDiagnostics.mockResolvedValue({});
  mockGetReliability.mockResolvedValue([]);
  mockGetValuesCacheStats.mockResolvedValue({
    rows: 0,
    value_json_bytes: 0,
    restored_rows: 0,
    cast_failures: 0,
    gc_rows_deleted: 0,
    flush_batches: 0,
    flushed_entries: 0,
  });
  mockConfirmAsk.mockResolvedValue(true);
});

afterEach(() => cleanup());

describe("Diagnostics — page load", () => {
  it("renders the health status once load() resolves", async () => {
    mockHealth.mockResolvedValue({
      status: "healthy",
      components: [{ name: "central-01", status: "healthy" }],
    });
    render(Diagnostics);

    await waitFor(() => expect(mockHealth).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText("central-01")).toBeInTheDocument();
    });
  });

  it("shows ErrorState with retry when health() fails", async () => {
    mockHealth.mockRejectedValueOnce(new Error("daemon unreachable"));
    render(Diagnostics);

    await waitFor(() => {
      expect(screen.getByText(/daemon unreachable/)).toBeInTheDocument();
    });

    mockHealth.mockResolvedValueOnce({ status: "healthy", components: [] });
    const retryButtons = screen.getAllByText("common.reload");
    retryButtons[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));

    await waitFor(() => expect(mockHealth).toHaveBeenCalledTimes(2));
    await waitFor(() => {
      expect(screen.queryByText(/daemon unreachable/)).not.toBeInTheDocument();
    });
  });
});

describe("Diagnostics — reliability panel", () => {
  it("renders a row per (central, interface) breaker snapshot", async () => {
    mockGetReliability.mockResolvedValue([
      { central: "alpha", interface: "HmIP-RF", circuit_state: 0 },
      { central: "alpha", interface: "BidCos-RF", circuit_state: 1 },
    ]);
    render(Diagnostics);

    await waitFor(() => expect(mockGetReliability).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText("HmIP-RF")).toBeInTheDocument();
      expect(screen.getByText("BidCos-RF")).toBeInTheDocument();
    });
  });

  it("shows ErrorState with retry when the reliability read fails", async () => {
    mockGetReliability.mockRejectedValueOnce(new Error("breaker read failed"));
    render(Diagnostics);

    await waitFor(() => {
      expect(screen.getByText(/breaker read failed/)).toBeInTheDocument();
    });

    mockGetReliability.mockResolvedValueOnce([]);
    const retryButtons = screen.getAllByText("common.reload");
    retryButtons[retryButtons.length - 1].dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );

    await waitFor(() => expect(mockGetReliability).toHaveBeenCalledTimes(2));
  });
});

describe("Diagnostics — interfaces panel", () => {
  it("renders duty cycle as a percentage badge and blanks unknown values", async () => {
    mockListInterfaces.mockResolvedValue([
      {
        id: "BidCos-RF",
        name: "BidCos-RF",
        connected: true,
        interface: "BidCos-RF",
        duty_cycle: 85,
      },
      {
        id: "HmIP-RF",
        name: "HmIP-RF",
        connected: true,
        interface: "HmIP-RF",
      },
    ]);
    render(Diagnostics);

    await waitFor(() => expect(mockListInterfaces).toHaveBeenCalled());
    await waitFor(() => {
      // BidCos-RF surfaces its duty cycle as a percentage badge.
      expect(screen.getByText("85%")).toBeInTheDocument();
    });
    // HmIP-RF has no radio-utilisation data → the placeholder is shown.
    // Both duty_cycle and carrier_sense columns fall back to "—", and
    // BidCos-RF's carrier_sense is unknown too, so at least two appear.
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(2);
  });

  it("renders a legitimate 0% duty cycle rather than treating it as unknown", async () => {
    mockListInterfaces.mockResolvedValue([
      {
        id: "BidCos-RF",
        name: "BidCos-RF",
        connected: true,
        interface: "BidCos-RF",
        duty_cycle: 0,
      },
    ]);
    render(Diagnostics);

    await waitFor(() => expect(mockListInterfaces).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText("0%")).toBeInTheDocument();
    });
  });

  it("renders carrier_sense as its own percentage badge", async () => {
    mockListInterfaces.mockResolvedValue([
      {
        id: "BidCos-RF",
        name: "BidCos-RF",
        connected: true,
        interface: "BidCos-RF",
        carrier_sense: 33,
      },
    ]);
    render(Diagnostics);

    await waitFor(() => expect(mockListInterfaces).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText("33%")).toBeInTheDocument();
    });
  });

  it("colors the utilisation badge by threshold: green below 60, yellow 60-79, red at 80+", async () => {
    mockListInterfaces.mockResolvedValue([
      { id: "A", name: "A", connected: true, interface: "BidCos-RF", duty_cycle: 59 },
      { id: "B", name: "B", connected: true, interface: "BidCos-RF", duty_cycle: 60 },
      { id: "C", name: "C", connected: true, interface: "BidCos-RF", duty_cycle: 79 },
      { id: "D", name: "D", connected: true, interface: "BidCos-RF", duty_cycle: 80 },
    ]);
    render(Diagnostics);

    await waitFor(() => expect(mockListInterfaces).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText("59%")).toBeInTheDocument());

    // Below 60 → success (green).
    expect(screen.getByText("59%").className).toContain("ha-success-color");
    // 60..79 → warning (yellow), inclusive of both boundary values.
    expect(screen.getByText("60%").className).toContain("ha-warning-color");
    expect(screen.getByText("79%").className).toContain("ha-warning-color");
    // 80 and above → danger (red).
    expect(screen.getByText("80%").className).toContain("ha-error-color");
  });
});

describe("Diagnostics — values-cache panel", () => {
  it("renders the cache row count from getValuesCacheStats", async () => {
    mockGetValuesCacheStats.mockResolvedValue({
      rows: 4321,
      value_json_bytes: 99,
      restored_rows: 1,
      cast_failures: 0,
      gc_rows_deleted: 0,
      flush_batches: 2,
      flushed_entries: 3,
    });
    render(Diagnostics);

    await waitFor(() => expect(mockGetValuesCacheStats).toHaveBeenCalled());
    await waitFor(() => {
      expect(screen.getByText("4321")).toBeInTheDocument();
    });
  });

  it("resets the cache after confirmation and shows a success toast", async () => {
    render(Diagnostics);
    await waitFor(() => expect(mockGetValuesCacheStats).toHaveBeenCalled());

    const resetButton = await waitFor(() => {
      const btn = screen.getByText(
        "diagnostics.values_cache.reset",
      ) as HTMLButtonElement;
      expect(btn.disabled).toBe(false);
      return btn;
    });
    await fireEvent.click(resetButton);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    await waitFor(() => expect(mockResetValuesCache).toHaveBeenCalledWith());
    await waitFor(() => expect(mockToastSuccess).toHaveBeenCalledTimes(1));
    expect(mockToastError).not.toHaveBeenCalled();
    // Stats are reloaded after a successful reset.
    expect(mockGetValuesCacheStats).toHaveBeenCalledTimes(2);
  });

  it("does not reset when the confirm dialog is declined", async () => {
    mockConfirmAsk.mockResolvedValueOnce(false);
    render(Diagnostics);
    await waitFor(() => expect(mockGetValuesCacheStats).toHaveBeenCalled());

    const resetButton = await waitFor(() => {
      const btn = screen.getByText(
        "diagnostics.values_cache.reset",
      ) as HTMLButtonElement;
      expect(btn.disabled).toBe(false);
      return btn;
    });
    await fireEvent.click(resetButton);

    await waitFor(() => expect(mockConfirmAsk).toHaveBeenCalled());
    expect(mockResetValuesCache).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// Localized enum surfaces
// ---------------------------------------------------------------------------

describe("Diagnostics — daemon enums reach the operator localized", () => {
  it("localizes a debug-capture state in the unified recordings table", async () => {
    // The RPC rows in the same column were already localized, so a raw
    // capture token put two languages in one column.
    mockListCaptures.mockResolvedValue([
      {
        id: "cap-1",
        status: "stopped",
        started_at: "2026-07-28T10:40:24Z",
        buffer_bytes: 2048,
        archive_size: 4096,
      },
    ]);
    render(Diagnostics);

    await waitFor(() => {
      expect(screen.getByText("Stopped")).toBeInTheDocument();
    });
    expect(screen.queryByText("stopped")).not.toBeInTheDocument();
  });

  it("localizes the client-health status the same way as the health card", async () => {
    mockDiagnostics.mockResolvedValue({
      health: {
        clients: [
          {
            name: "central-01-HmIP-RF",
            status: "degraded",
            requests: 12,
            failures: 1,
          },
        ],
      },
    });
    render(Diagnostics);

    await waitFor(() => {
      expect(screen.getByText("Degraded")).toBeInTheDocument();
    });
    expect(screen.queryByText("degraded")).not.toBeInTheDocument();
  });

  it("localizes the incident severity badge", async () => {
    mockIncidents.mockResolvedValue([
      {
        id: 1,
        when: "2026-07-28T10:40:24Z",
        severity: "critical",
        component: "central-01-HmIP-RF",
        summary: "interface unreachable",
      },
    ]);
    render(Diagnostics);

    await waitFor(() => {
      expect(screen.getByText("Critical")).toBeInTheDocument();
    });
    expect(screen.queryByText("critical")).not.toBeInTheDocument();
  });
});

// The RPC-recording poll costs one request per interval for as long as the
// view is open. Only a running recording changes on its own, so an idle
// page must not keep asking — the counterpart of the running case below.
describe("Diagnostics — RPC recording poll", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  async function settle() {
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
  }

  it("stays quiet while no recording is running", async () => {
    mockListRpcRecordings.mockResolvedValue([
      { central: "ccu1", active: false, entries: 0, randomize: false },
    ]);
    render(Diagnostics);
    await settle();
    const afterLoad = mockListRpcRecordings.mock.calls.length;

    await vi.advanceTimersByTimeAsync(20000);

    expect(mockListRpcRecordings.mock.calls.length).toBe(afterLoad);
  });

  it("polls while a recording is running", async () => {
    mockListRpcRecordings.mockResolvedValue([
      { central: "ccu1", active: true, entries: 3, randomize: false },
    ]);
    render(Diagnostics);
    await settle();
    const afterLoad = mockListRpcRecordings.mock.calls.length;

    await vi.advanceTimersByTimeAsync(11000);

    expect(mockListRpcRecordings.mock.calls.length).toBeGreaterThan(afterLoad);
  });
});
