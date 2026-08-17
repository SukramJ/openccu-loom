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

  it("hints that a password is stored instead of the generic storage-location text", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
    const { container, getByText } = render(CentralsAdmin);

    await openEditModal(container);
    expect(getByText("centrals.field.password_hint_unchanged")).toBeTruthy();

    const pwInput = container.querySelector<HTMLInputElement>("input[type='password']");
    if (!pwInput) throw new Error("Password input not found");
    await fireEvent.input(pwInput, { target: { value: "x" } });

    // Once touched, the "a password is stored" hint gives way to the
    // regular storage-location hint every other field shows.
    expect(getByText("centrals.field.password_hint")).toBeTruthy();
  });

  it("never renders the GET-masked sentinel into the password field's value", async () => {
    // GET /centrals masks a stored password to the literal "***" so the
    // response is safe to log. That sentinel must never appear as the
    // input's own value — an operator who clicks in and types would
    // otherwise persist "***" plus whatever they typed as the credential.
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);
    const pwInput = container.querySelector<HTMLInputElement>("input[type='password']");
    if (!pwInput) throw new Error("Password input not found");
    expect(pwInput.value).toBe("");
  });

  it("sends the emptied password as an explicit empty string so the daemon clears it", async () => {
    // The daemon reads an absent password_plain as "unchanged" — it has to,
    // because GET masks the stored credential and a client that round-trips a
    // central would otherwise wipe it. Dropping the key when the input is
    // empty would therefore make the password impossible to clear from here.
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);
    const pwInput = container.querySelector<HTMLInputElement>("input[type='password']");
    if (!pwInput) throw new Error("Password input not found");
    await fireEvent.input(pwInput, { target: { value: "" } });

    await clickSave(container);

    await waitFor(() => {
      expect(mockUpdateCentral).toHaveBeenCalledOnce();
    });
    const payload = mockUpdateCentral.mock.calls[0][1] as { password_plain?: string };
    expect(payload.password_plain).toBe("");
  });

  it("omits password_plain entirely when the password was not touched, so the daemon keeps the stored credential", async () => {
    // Before the fix this echoed the "***" sentinel back verbatim, which
    // the daemon would have persisted as the literal 3-character password.
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);
    await clickSave(container);

    await waitFor(() => {
      expect(mockUpdateCentral).toHaveBeenCalledOnce();
    });
    const payload = mockUpdateCentral.mock.calls[0][1] as { password_plain?: string };
    expect(payload.password_plain).toBeUndefined();
  });

  it("sends the typed password verbatim once the operator replaces it", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
    const { container } = render(CentralsAdmin);

    await openEditModal(container);
    const pwInput = container.querySelector<HTMLInputElement>("input[type='password']");
    if (!pwInput) throw new Error("Password input not found");
    await fireEvent.input(pwInput, { target: { value: "new-secret" } });

    await clickSave(container);

    await waitFor(() => {
      expect(mockUpdateCentral).toHaveBeenCalledOnce();
    });
    const payload = mockUpdateCentral.mock.calls[0][1] as { password_plain?: string };
    expect(payload.password_plain).toBe("new-secret");
  });

  it("does not claim a restart-relevant change from an untouched, GET-masked password", async () => {
    // Regression guard for the omitted-password_plain fix above:
    // buildRow() now sends `undefined` (not "***") when the password was
    // not touched, so the restart-signal diff must treat that omission
    // as "no change" rather than comparing it against the "***" sentinel
    // stored on the loaded row and reporting a spurious restart requirement.
    mockListCentrals.mockResolvedValue([{ ...baseRow, password_plain: "***" }]);
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
