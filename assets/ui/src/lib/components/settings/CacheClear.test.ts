// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, fireEvent, waitFor } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Module-level mock state
// ---------------------------------------------------------------------------

const mockClearCache = vi.fn();
const mockToastError = vi.fn();
const mockToastSuccess = vi.fn();
const mockConfirmAsk = vi.fn();

// ---------------------------------------------------------------------------
// Mocks — registered before any component import
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    clearCache: (...args: unknown[]) => mockClearCache(...args),
    getConfigSchema: vi.fn().mockResolvedValue({ sections: [], fields: [] }),
    getEffectiveConfig: vi.fn().mockResolvedValue({ config: {}, sources: {} }),
    getConfigChanges: vi.fn().mockResolvedValue({ fields: [] }),
    getStartupCapture: vi
      .fn()
      .mockResolvedValue({ enabled: false, duration_seconds: 600, anonymise: true }),
    info: vi.fn().mockResolvedValue({ capabilities: [] }),
    reloadMQTT: vi.fn().mockResolvedValue({ reloaded: true, took_ms: 1 }),
    restartDaemon: vi.fn().mockResolvedValue({ status: "ok", at: "" }),
    getRestartPending: vi.fn().mockResolvedValue({ pending: false, fields: [] }),
  },
  setUnauthorizedHandler: vi.fn(),
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
    error: (...args: unknown[]) => mockToastError(...args),
    success: (...args: unknown[]) => mockToastSuccess(...args),
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

vi.mock("$lib/stores/restartPending.svelte", () => ({
  restartPending: { pending: false, fields: [] },
  restartCaps: { supervised: false, loaded: false },
  refreshRestartPending: vi.fn().mockResolvedValue(undefined),
  loadRestartCaps: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, _params?: unknown) => key,
}));

// ---------------------------------------------------------------------------
// Delayed component import so mocks are hoisted first
// ---------------------------------------------------------------------------

import Settings from "../../../routes/Settings.svelte";

// ---------------------------------------------------------------------------
// Helper: click the "System" tab (appears twice — mobile strip + desktop
// sidebar — so we pick the first one).
// ---------------------------------------------------------------------------

async function navigateToSystemTab(container: HTMLElement) {
  // Wait for the tab bar to render
  await waitFor(() => {
    const tabs = container.querySelectorAll('button[type="button"]');
    const systemTab = Array.from(tabs).find(
      (b) => b.textContent?.trim() === "settings.tab.system",
    );
    if (!systemTab) throw new Error("settings.tab.system button not found");
  });

  const tabs = container.querySelectorAll('button[type="button"]');
  const systemTab = Array.from(tabs).find(
    (b) => b.textContent?.trim() === "settings.tab.system",
  ) as HTMLElement;

  await fireEvent.click(systemTab);
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("Settings – Clear CCU cache", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the Clear CCU cache button in the system tab", async () => {
    const { container } = render(Settings);

    await navigateToSystemTab(container);

    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll('button[type="button"]'));
      const found = buttons.some(
        (b) => b.textContent?.trim() === "admin.cache_clear.button",
      );
      expect(found).toBe(true);
    });
  });

  it("opens the confirm dialog when the button is clicked", async () => {
    mockConfirmAsk.mockResolvedValue(false);
    const { container } = render(Settings);

    await navigateToSystemTab(container);

    let btn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll('button[type="button"]'));
      btn = buttons.find(
        (b) => b.textContent?.trim() === "admin.cache_clear.button",
      ) as HTMLElement | undefined;
      if (!btn) throw new Error("cache clear button not found");
    });

    await fireEvent.click(btn!);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalledWith(
        expect.objectContaining({ destructive: true }),
      );
    });
  });

  it("calls clearCache and shows success toast on confirm", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockClearCache.mockResolvedValue({
      scope: "global",
      devices: 12,
      paramsets: 48,
      values: 200,
      master: 5,
      centrals_reinit: 1,
      errors: 0,
    });

    const { container } = render(Settings);

    await navigateToSystemTab(container);

    let btn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll('button[type="button"]'));
      btn = buttons.find(
        (b) => b.textContent?.trim() === "admin.cache_clear.button",
      ) as HTMLElement | undefined;
      if (!btn) throw new Error("cache clear button not found");
    });

    await fireEvent.click(btn!);

    await waitFor(() => {
      expect(mockClearCache).toHaveBeenCalledWith({ kind: "global" });
      expect(mockToastSuccess).toHaveBeenCalled();
    });
  });

  it("shows error toast when clearCache rejects", async () => {
    mockConfirmAsk.mockResolvedValue(true);
    mockClearCache.mockRejectedValue(new Error("network error"));

    const { container } = render(Settings);

    await navigateToSystemTab(container);

    let btn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll('button[type="button"]'));
      btn = buttons.find(
        (b) => b.textContent?.trim() === "admin.cache_clear.button",
      ) as HTMLElement | undefined;
      if (!btn) throw new Error("cache clear button not found");
    });

    await fireEvent.click(btn!);

    await waitFor(() => {
      expect(mockToastError).toHaveBeenCalled();
    });
  });

  it("does not call clearCache when the confirm dialog is cancelled", async () => {
    mockConfirmAsk.mockResolvedValue(false);

    const { container } = render(Settings);

    await navigateToSystemTab(container);

    let btn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll('button[type="button"]'));
      btn = buttons.find(
        (b) => b.textContent?.trim() === "admin.cache_clear.button",
      ) as HTMLElement | undefined;
      if (!btn) throw new Error("cache clear button not found");
    });

    await fireEvent.click(btn!);

    await waitFor(() => {
      expect(mockConfirmAsk).toHaveBeenCalled();
    });

    expect(mockClearCache).not.toHaveBeenCalled();
  });
});
