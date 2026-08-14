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
const mockCreateCentral = vi.fn();
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
    createCentralV2: (...args: unknown[]) => mockCreateCentral(...args),
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
  suggested_host: string;
  manufacturer?: string;
  model?: string;
  last_seen: string;
  already_configured: boolean;
}> = {}) => ({
  serial: "ABC123",
  name: "My CCU",
  host: "192.168.0.10",
  suggested_host: "192.168.0.10",
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
    mockCreateCentral.mockResolvedValue({});
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

  it("adopting a discovered CCU sanitises its friendly name into a routable central name", async () => {
    // An SSDP friendly name routinely carries spaces. The name becomes a path
    // segment of the callback URL the daemon announces to the CCU, and the
    // callback router only accepts [A-Za-z0-9_-] — an unsanitised prefill
    // yields a central that never receives a single push event.
    mockListDiscovered.mockResolvedValue([
      makeCCU({
        host: "192.168.0.11",
        suggested_host: "192.168.0.11",
        serial: "SER-10",
        name: "CCU Wohnzimmer",
        already_configured: false,
      }),
    ]);

    const { container } = render(CentralsAdmin);

    let adoptBtn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll("button"));
      adoptBtn = buttons.find(
        (b) => b.textContent?.trim() === "discovery.add",
      ) as HTMLElement | undefined;
      if (!adoptBtn) throw new Error("Adopt button not found");
    });
    await fireEvent.click(adoptBtn!);

    const saveBtn = await waitFor(() => {
      const btn = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
        (b) => b.textContent?.trim() === "common.save",
      );
      if (!btn) throw new Error("Save button not found");
      return btn;
    });
    await fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(mockCreateCentral).toHaveBeenCalledOnce();
      const payload = mockCreateCentral.mock.calls[0][0] as Record<string, unknown>;
      expect(payload.name).toBe("CCU-Wohnzimmer");
    });
  });

  it("adopting a discovered CCU pre-fills host from suggested_host and carries serial into createCentralV2", async () => {
    mockListDiscovered.mockResolvedValue([
      makeCCU({
        host: "192.168.0.10",
        suggested_host: "localhost",
        serial: "SER-9",
        name: "CCU-Adopt",
        already_configured: false,
      }),
    ]);

    const { container } = render(CentralsAdmin);

    // Wait for the "Add" button to appear next to the discovered CCU
    let adoptBtn: HTMLElement | undefined;
    await waitFor(() => {
      const buttons = Array.from(container.querySelectorAll("button"));
      adoptBtn = buttons.find(
        (b) => b.textContent?.trim() === "discovery.add",
      ) as HTMLElement | undefined;
      if (!adoptBtn) throw new Error("Adopt button not found");
    });

    // Click adopt — this calls prefillFromDiscovered which opens the modal
    await fireEvent.click(adoptBtn!);

    // The modal should be visible; verify host input was set to suggested_host
    await waitFor(() => {
      const allInputs = Array.from(
        container.querySelectorAll<HTMLInputElement>("input[type='text']"),
      );
      const hostField = allInputs.find((inp) => inp.value === "localhost");
      if (!hostField) throw new Error("Host input with suggested_host value not found in modal");
      expect(hostField.value).toBe("localhost");
    });

    // Trigger save — HmIP-RF is pre-checked by freshInterfaceForm so no_interface
    // error is not expected; name is pre-filled from ccu.name
    const saveBtn = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
      (b) => b.textContent?.trim() === "common.save",
    );
    expect(saveBtn).toBeDefined();
    await fireEvent.click(saveBtn!);

    // Assert createCentralV2 was called with host === "localhost" and serial === "SER-9"
    await waitFor(() => {
      expect(mockCreateCentral).toHaveBeenCalledOnce();
      const payload = mockCreateCentral.mock.calls[0][0] as Record<string, unknown>;
      expect(payload.host).toBe("localhost");
      expect(payload.serial).toBe("SER-9");
    });
  });
});
