// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Module-level mock state
// ---------------------------------------------------------------------------
const mockListDiscovered = vi.fn();
const mockIgnore = vi.fn();
const mockListCentrals = vi.fn();
const mockConfirmAsk = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastError = vi.fn();

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentrals(...args),
    listDiscoveredCentrals: (...args: unknown[]) => mockListDiscovered(...args),
    ignoreDiscoveredCentral: (...args: unknown[]) => mockIgnore(...args),
    updateCentralV2: vi.fn().mockResolvedValue({}),
    createCentralV2: vi.fn().mockResolvedValue({}),
    deleteCentralV2: vi.fn().mockResolvedValue({}),
    getConfigSchema: vi.fn().mockResolvedValue({ sections: [], fields: [] }),
    getEffectiveConfig: vi.fn().mockResolvedValue({ config: {}, sources: {} }),
  },
  friendlyError: (_err: unknown, _t: unknown) => "mocked error",
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

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: {
    success: (...args: unknown[]) => mockToastSuccess(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: (...args: unknown[]) => mockConfirmAsk(...args) },
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { expertMode: false, locale: "en" },
  applyTheme: vi.fn(),
  setLocale: vi.fn(),
  setTheme: vi.fn(),
  setNavCollapsed: vi.fn(),
  setExpertMode: vi.fn(),
  setDeviceView: vi.fn(),
  bindSystemTheme: vi.fn(() => () => {}),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, params?: Record<string, string>) => {
    if (params) {
      return Object.entries(params).reduce(
        (s, [k, v]) => s.replace(`{${k}}`, String(v)),
        key,
      );
    }
    return key;
  },
}));

// ---------------------------------------------------------------------------
// Component import (after mocks)
// ---------------------------------------------------------------------------
import CentralsAdmin from "./CentralsAdmin.svelte";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
const makeCCU = (overrides: Partial<{
  serial: string;
  name: string;
  host: string;
  manufacturer?: string;
  model?: string;
  last_seen: string;
  already_configured: boolean;
}> = {}) => ({
  serial: "ABC123",
  name: "My CCU",
  host: "192.168.0.10",
  last_seen: new Date().toISOString(),
  already_configured: false,
  ...overrides,
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------
describe("CentralsAdmin — discovered CCUs", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListCentrals.mockResolvedValue([]);
  });

  it("renders discovered CCUs in the list", async () => {
    mockListDiscovered.mockResolvedValue([makeCCU({ name: "CCU-3000", host: "10.0.0.1" })]);

    const { container } = render(CentralsAdmin);

    await waitFor(() => {
      expect(container.textContent).toContain("CCU-3000");
      expect(container.textContent).toContain("10.0.0.1");
    });
  });

  it("calls ignoreDiscoveredCentral with the serial when confirmed", async () => {
    const serial = "SN-TESTSERIAL";
    mockListDiscovered.mockResolvedValue([makeCCU({ serial, name: "CCU-Ignore" })]);
    mockIgnore.mockResolvedValue(undefined);
    mockConfirmAsk.mockResolvedValue(true);

    const { container } = render(CentralsAdmin);

    let ignoreBtn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll("button"));
      ignoreBtn = buttons.find(
        (b) => b.textContent?.trim() === "discovery.ignore",
      ) as HTMLElement | undefined;
      if (!ignoreBtn) throw new Error("Ignore button not found");
    });

    await fireEvent.click(ignoreBtn!);

    await waitFor(() => {
      expect(mockIgnore).toHaveBeenCalledWith(serial);
      expect(mockToastSuccess).toHaveBeenCalled();
    });
  });

  it("does not call ignore when user cancels the confirm dialog", async () => {
    const serial = "SN-CANCEL";
    mockListDiscovered.mockResolvedValue([makeCCU({ serial })]);
    mockConfirmAsk.mockResolvedValue(false);

    const { container } = render(CentralsAdmin);

    let ignoreBtn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll("button"));
      ignoreBtn = buttons.find(
        (b) => b.textContent?.trim() === "discovery.ignore",
      ) as HTMLElement | undefined;
      if (!ignoreBtn) throw new Error("Ignore button not found");
    });

    await fireEvent.click(ignoreBtn!);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalled();
    });
    expect(mockIgnore).not.toHaveBeenCalled();
  });

  it("shows the already-configured badge and no Add button for configured CCUs", async () => {
    mockListDiscovered.mockResolvedValue([
      makeCCU({ serial: "CONF-1", name: "CCU-Configured", already_configured: true }),
    ]);

    const { container } = render(CentralsAdmin);

    await waitFor(() => {
      expect(container.textContent).toContain("discovery.already_configured");
    });

    const buttons = Array.from(container.querySelectorAll("button"));
    const addBtn = buttons.find((b) => b.textContent?.trim() === "discovery.add");
    expect(addBtn).toBeUndefined();
  });
});
