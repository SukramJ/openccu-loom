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

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

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
