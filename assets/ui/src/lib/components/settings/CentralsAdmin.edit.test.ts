// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/svelte";
import { fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Module-level mock state
// ---------------------------------------------------------------------------
const mockListCentrals = vi.fn();
const mockUpdateCentral = vi.fn();
const mockToastSuccess = vi.fn();
const mockToastWarn = vi.fn();
const mockToastError = vi.fn();

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentrals(...args),
    listDiscoveredCentrals: vi.fn().mockResolvedValue([]),
    ignoreDiscoveredCentral: vi.fn().mockResolvedValue(undefined),
    updateCentralV2: (...args: unknown[]) => mockUpdateCentral(...args),
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
    warn: (...args: unknown[]) => mockToastWarn(...args),
    error: (...args: unknown[]) => mockToastError(...args),
  },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(true) },
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

const baseRow = {
  name: "prod-ccu",
  host: "192.168.1.10",
  enabled: true,
  interfaces: [{ name: "HmIP-RF", port: 2010 }],
  primary_interface: "HmIP-RF",
};

async function openEditModal(container: HTMLElement) {
  let editBtn: HTMLElement | undefined;
  await waitFor(() => {
    const buttons = Array.from(container.querySelectorAll("button"));
    editBtn = buttons.find((b) => b.textContent?.trim() === "common.edit") as
      | HTMLElement
      | undefined;
    if (!editBtn) throw new Error("Edit button not found");
  });
  await fireEvent.click(editBtn!);
  await waitFor(() => {
    const saveBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "common.save",
    );
    if (!saveBtn) throw new Error("Save button not found in edit modal");
  });
}

function clickSave(container: HTMLElement) {
  const saveBtn = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => b.textContent?.trim() === "common.save",
  );
  if (!saveBtn) throw new Error("Save button not found");
  return fireEvent.click(saveBtn);
}

describe("CentralsAdmin — edit save toast honesty", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUpdateCentral.mockResolvedValue(undefined);
  });

  it("shows a plain success toast when no southbound-relevant field changed", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);
    await clickSave(container);

    await waitFor(() => {
      expect(mockUpdateCentral).toHaveBeenCalledOnce();
    });
    expect(mockToastSuccess).toHaveBeenCalledWith("centrals.updated");
    expect(mockToastWarn).not.toHaveBeenCalled();
  });

  it("shows a restart-required warning toast (not a bare success) when the host changes on an already-enabled CCU", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);

    const hostInput = Array.from(
      container.querySelectorAll<HTMLInputElement>("input[type='text']"),
    ).find((inp) => inp.value === baseRow.host);
    if (!hostInput) throw new Error("Host input not found");
    await fireEvent.input(hostInput, { target: { value: "10.0.0.99" } });

    await clickSave(container);

    await waitFor(() => {
      expect(mockUpdateCentral).toHaveBeenCalledOnce();
    });
    expect(mockToastWarn).toHaveBeenCalledWith("centrals.updated_restart_required");
    expect(mockToastSuccess).not.toHaveBeenCalled();
  });
});
