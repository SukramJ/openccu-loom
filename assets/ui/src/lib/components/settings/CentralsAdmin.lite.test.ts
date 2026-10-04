// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor, fireEvent } from "@testing-library/svelte";

const mockListCentrals = vi.fn();
const mockUpdateCentral = vi.fn();
const mockCreateCentral = vi.fn();
const fleetFeatures: Record<string, { available: boolean; reason?: string; scope?: string }> = {};
// The fleet centralsLacking answers from, with the real store's rule: only a
// lasting reason counts, a booting CCU ("not_ready") does not.
type FleetEntry = { name: string; features: Record<string, { available: boolean; reason?: string }> };
let fleet: FleetEntry[] = [];

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
    centralsLacking: (key: string) =>
      fleet.filter((c) => {
        const f = c.features[key];
        return f !== undefined && !f.available && f.reason !== "not_ready";
      }),
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
import { prefs } from "$lib/stores/preferences.svelte";

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
    fleet = [];
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

  it("offers the box's port in expert mode, under its own label", async () => {
    prefs.expertMode = true;
    mockListCentrals.mockResolvedValue([{ ...liteRow, json_rpc_port: 8443 }]);
    const { container, queryByText, unmount } = render(CentralsAdmin);
    try {
      await openEdit(container);

      // A box has no JSON-RPC endpoint; the field is its web server's port.
      expect(queryByText("centrals.field.json_rpc_port")).toBeNull();
      expect(queryByText("centrals.field.port")).not.toBeNull();
      expect(queryByText("centrals.field.lite_port_hint")).not.toBeNull();
    } finally {
      // The queries search the whole document, so a dialog left mounted
      // would answer the next test's absence checks.
      unmount();
      prefs.expertMode = false;
    }
  });

  it("does not offer the port field and keeps a stored port on save", async () => {
    mockListCentrals.mockResolvedValue([{ ...liteRow, json_rpc_port: 8443 }]);
    const { container, queryByText } = render(CentralsAdmin);
    await openEdit(container);

    expect(queryByText("centrals.field.json_rpc_port")).toBeNull();
    expect(queryByText("centrals.field.port")).toBeNull();

    await fireEvent.click(button(container, "common.save")!);
    await waitFor(() => expect(mockUpdateCentral).toHaveBeenCalledOnce());
    expect(mockUpdateCentral.mock.calls[0][1].json_rpc_port).toBe(8443);
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

// The seven controls that only mean something on a system with ReGa
// variables and programs.
const SYSVAR_PROGRAM_CONTROLS = [
  "centrals.behavior.enable_sysvar_scan",
  "centrals.behavior.include_internal_sysvars",
  "centrals.behavior.sysvar_scan_interval",
  "centrals.behavior.sysvar_markers",
  "centrals.behavior.enable_program_scan",
  "centrals.behavior.include_internal_programs",
  "centrals.behavior.program_markers",
];

const ccuRow = {
  name: "prod-ccu",
  host: "192.168.1.10",
  enabled: true,
  interfaces: [{ name: "HmIP-RF", port: 2010 }],
  primary_interface: "HmIP-RF",
};

// Every field set away from its form default, so a value the form dropped
// or reset on save shows up as a difference.
const storedBehavior = {
  light_last_brightness: false,
  use_group_channel_for_cover_state: false,
  enable_sysvar_scan: false,
  enable_program_scan: false,
  include_internal_sysvars: false,
  include_internal_programs: true,
  enable_device_firmware_check: false,
  delay_new_device_creation: true,
  sysvar_markers: ["HAHM"],
  program_markers: ["INTERNAL"],
  sysvar_scan_interval: 30_000_000_000,
};

async function openBehaviour(container: HTMLElement) {
  await openEdit(container);
  const toggle = Array.from(container.querySelectorAll("button")).find((b) =>
    b.textContent?.includes("centrals.behavior.title"),
  );
  if (!toggle) throw new Error("behaviour section toggle not found");
  await fireEvent.click(toggle);
  await waitFor(() => {
    if (!container.textContent?.includes("centrals.behavior.light_last_brightness")) {
      throw new Error("behaviour section did not open");
    }
  });
}

describe("CentralsAdmin — sysvar and program behaviour controls", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fleet = [];
    mockUpdateCentral.mockResolvedValue(undefined);
  });

  it("hides them for an openccu-lite central and saves the stored values unchanged", async () => {
    fleet = [
      {
        name: "box",
        features: {
          "hub.sysvars": { available: false, reason: "not_supported_by_system" },
          "hub.programs": { available: false, reason: "not_supported_by_system" },
        },
      },
    ];
    mockListCentrals.mockResolvedValue([{ ...liteRow, behavior: { ...storedBehavior } }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    for (const key of SYSVAR_PROGRAM_CONTROLS) {
      expect(container.textContent, key).not.toContain(key);
    }
    // The device-pipeline toggles apply to openccu-lite too.
    expect(container.textContent).toContain("centrals.behavior.light_last_brightness");
    expect(container.textContent).toContain("centrals.behavior.enable_device_firmware_check");

    await fireEvent.click(button(container, "common.save")!);
    await waitFor(() => expect(mockUpdateCentral).toHaveBeenCalledOnce());
    expect(mockUpdateCentral.mock.calls[0][1].behavior).toEqual(storedBehavior);
  });

  it("shows them for a CCU central", async () => {
    fleet = [
      { name: "prod-ccu", features: { "hub.sysvars": { available: true }, "hub.programs": { available: true } } },
    ];
    mockListCentrals.mockResolvedValue([{ ...ccuRow }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    for (const key of SYSVAR_PROGRAM_CONTROLS) {
      expect(container.textContent, key).toContain(key);
    }
    expect(container.textContent).toContain("centrals.field.json_rpc_port");
  });

  it("shows them for a CCU that is still booting", async () => {
    fleet = [
      {
        name: "prod-ccu",
        features: {
          "hub.sysvars": { available: false, reason: "not_ready" },
          "hub.programs": { available: false, reason: "not_ready" },
        },
      },
    ];
    mockListCentrals.mockResolvedValue([{ ...ccuRow }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    for (const key of SYSVAR_PROGRAM_CONTROLS) {
      expect(container.textContent, key).toContain(key);
    }
  });

  it("hides only the program controls for a central that reports programs absent", async () => {
    fleet = [
      {
        name: "prod-ccu",
        features: {
          "hub.sysvars": { available: true },
          "hub.programs": { available: false, reason: "not_supported_by_system" },
        },
      },
    ];
    mockListCentrals.mockResolvedValue([{ ...ccuRow }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    expect(container.textContent).toContain("centrals.behavior.enable_sysvar_scan");
    expect(container.textContent).toContain("centrals.behavior.sysvar_markers");
    expect(container.textContent).not.toContain("centrals.behavior.enable_program_scan");
    expect(container.textContent).not.toContain("centrals.behavior.include_internal_programs");
    expect(container.textContent).not.toContain("centrals.behavior.program_markers");
  });
});
