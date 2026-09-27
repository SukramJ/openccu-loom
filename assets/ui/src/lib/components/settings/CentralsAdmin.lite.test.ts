// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor, fireEvent } from "@testing-library/svelte";

const mockListCentrals = vi.fn();
const mockUpdateCentral = vi.fn();
const mockCreateCentral = vi.fn();
const fleetFeatures: Record<string, { available: boolean; reason?: string; scope?: string }> = {};

vi.mock("$lib/api/client", () => ({
  api: {
    listCentralsV2: (...args: unknown[]) => mockListCentrals(...args),
    listDiscoveredCentrals: vi.fn().mockResolvedValue([]),
    ignoreDiscoveredCentral: vi.fn().mockResolvedValue(undefined),
    updateCentralV2: (...args: unknown[]) => mockUpdateCentral(...args),
    createCentralV2: (...args: unknown[]) => mockCreateCentral(...args),
    deleteCentralV2: vi.fn().mockResolvedValue({}),
    probeCentral: vi.fn(),
    startCentralPairing: vi.fn(),
    getCentralPairing: vi.fn(),
    cancelCentralPairing: vi.fn(),
  },
  friendlyError: () => "mocked error",
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

vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    items: [],
    offers: () => true,
    featureAvailable: () => true,
    centralsLacking: () => [],
    featureOf: () => undefined,
    byName: (n: string) => (n === "box" ? { name: "box", system_type: "openccu-lite", features: fleetFeatures } : undefined),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), warn: vi.fn(), error: vi.fn() },
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
  bindSystemTheme: vi.fn(() => () => {}),
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import CentralsAdmin from "./CentralsAdmin.svelte";

const liteRow = {
  name: "box",
  host: "192.0.2.50",
  enabled: true,
  system_type: "openccu-lite",
  api_token_plain: "***",
  tls: true,
  tls_fingerprint: "ab".repeat(32),
  interfaces: [{ name: "HmIP-RF" }],
  primary_interface: "HmIP-RF",
};

function button(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => b.textContent?.trim() === label,
  );
}

async function openEdit(container: HTMLElement) {
  await waitFor(() => {
    if (!button(container, "common.edit")) throw new Error("no edit button");
  });
  await fireEvent.click(button(container, "common.edit")!);
  await waitFor(() => {
    if (!button(container, "common.save")) throw new Error("no save button");
  });
}

describe("CentralsAdmin — openccu-lite central", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    for (const k of Object.keys(fleetFeatures)) delete fleetFeatures[k];
    mockUpdateCentral.mockResolvedValue(undefined);
    mockListCentrals.mockResolvedValue([{ ...liteRow }]);
  });

  it("edits without username, password or CUxD and keeps the stored token", async () => {
    const { container, queryByText } = render(CentralsAdmin);
    await openEdit(container);

    expect(queryByText("centrals.field.username")).toBeNull();
    expect(queryByText("centrals.field.password")).toBeNull();
    expect(queryByText("CUxD")).toBeNull();

    await fireEvent.click(button(container, "common.save")!);
    await waitFor(() => expect(mockUpdateCentral).toHaveBeenCalledOnce());
    const sent = mockUpdateCentral.mock.calls[0][1];
    expect(sent.system_type).toBe("openccu-lite");
    expect(sent.tls).toBe(true);
    expect(sent.tls_fingerprint).toBe(liteRow.tls_fingerprint);
    // The masked token never goes back, and nothing replaces it.
    expect(sent.api_token_plain).toBeUndefined();
    expect(sent.pairing_id).toBeUndefined();
    expect(sent.password_plain).toBeUndefined();
    expect(sent.username).toBeUndefined();
    expect(JSON.stringify(sent)).not.toContain("***");
  });

  it("sends a pasted token", async () => {
    const { container, getByText } = render(CentralsAdmin);
    await openEdit(container);
    await fireEvent.click(getByText("onboarding.tab.token"));
    const input = container.querySelector<HTMLInputElement>('[data-testid="central-onboarding"] input[type="password"]');
    expect(input).toBeTruthy();
    await fireEvent.input(input!, { target: { value: "olt_new" } });

    await fireEvent.click(button(container, "common.save")!);
    await waitFor(() => expect(mockUpdateCentral).toHaveBeenCalledOnce());
    expect(mockUpdateCentral.mock.calls[0][1].api_token_plain).toBe("olt_new");
  });

  it("names the features a missing scope would unlock", async () => {
    fleetFeatures["system.reboot"] = { available: false, reason: "missing_scope", scope: "power" };
    fleetFeatures["system.poweroff"] = { available: false, reason: "missing_scope", scope: "power" };
    fleetFeatures["hub.programs"] = { available: false, reason: "not_supported_by_system" };
    const { container, getByText } = render(CentralsAdmin);
    await openEdit(container);
    expect(getByText("centrals.lite.missing_scopes")).toBeTruthy();
    const list = getByText("power").closest("li");
    expect(list?.textContent).toContain("system.reboot");
    expect(list?.textContent).toContain("system.poweroff");
    expect(list?.textContent).not.toContain("hub.programs");
  });
});
