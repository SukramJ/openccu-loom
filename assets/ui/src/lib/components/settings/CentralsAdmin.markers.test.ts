// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, waitFor, fireEvent } from "@testing-library/svelte";

// ---------------------------------------------------------------------------
// Module-level mock state
// ---------------------------------------------------------------------------
const mockListCentrals = vi.fn();
const mockUpdateCentral = vi.fn();

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
  setDeviceView: vi.fn(),
  bindSystemTheme: vi.fn(() => () => {}),
}));

// The i18n mock echoes the key, so a missing translation is indistinguishable
// from a present one here. The catalogue itself is asserted separately below
// against the real module.
vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
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

async function openBehaviour(container: HTMLElement) {
  await waitFor(() => {
    const editBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "common.edit",
    );
    if (!editBtn) throw new Error("Edit button not found");
    return fireEvent.click(editBtn);
  });
  const toggle = await waitFor(() => {
    const b = Array.from(container.querySelectorAll("button")).find((x) =>
      x.textContent?.includes("centrals.behavior.title"),
    );
    if (!b) throw new Error("Behaviour section toggle not found");
    return b;
  });
  await fireEvent.click(toggle);
  await waitFor(() => {
    if (!container.textContent?.includes("centrals.behavior.program_markers")) {
      throw new Error("Behaviour section did not open");
    }
  });
}

/** Marker codes rendered inside the group whose label is `labelKey`. */
function markersOf(container: HTMLElement, labelKey: string): string[] {
  const group = Array.from(container.querySelectorAll("div")).find(
    (d) =>
      d.firstElementChild?.textContent?.trim() === labelKey &&
      d.querySelector("input[type=checkbox]"),
  );
  if (!group) throw new Error(`marker group ${labelKey} not found`);
  return Array.from(group.querySelectorAll("code")).map((c) => c.textContent!.trim());
}

describe("CentralsAdmin — marker pickers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUpdateCentral.mockResolvedValue(undefined);
  });

  it("offers HAHM for system variables but not for programs", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    expect(markersOf(container, "centrals.behavior.sysvar_markers")).toEqual([
      "HAHM",
      "HX",
      "INTERNAL",
      "MQTT",
    ]);
    // HAHM makes a sysvar writable; a program has no value to write, so
    // offering it there would promise an effect that does not exist.
    expect(markersOf(container, "centrals.behavior.program_markers")).toEqual([
      "HX",
      "INTERNAL",
      "MQTT",
    ]);
  });

  it("explains every offered marker instead of showing a bare code", async () => {
    mockListCentrals.mockResolvedValue([{ ...baseRow }]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    for (const m of ["HAHM", "HX", "INTERNAL", "MQTT"]) {
      expect(container.textContent).toContain(`centrals.behavior.marker.${m.toLowerCase()}`);
    }
    expect(container.textContent).toContain("centrals.behavior.markers_hint");
  });

  it("drops a stored HAHM program marker so what is shown is what gets saved", async () => {
    mockListCentrals.mockResolvedValue([
      { ...baseRow, behavior: { program_markers: ["HAHM", "INTERNAL"] } },
    ]);
    const { container } = render(CentralsAdmin);
    await openBehaviour(container);

    const saveBtn = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
      (b) => b.textContent?.trim() === "common.save",
    );
    await fireEvent.click(saveBtn!);

    await waitFor(() => expect(mockUpdateCentral).toHaveBeenCalledOnce());
    const body = mockUpdateCentral.mock.calls[0]![1] as {
      behavior: { program_markers: string[] };
    };
    expect(body.behavior.program_markers).toEqual(["INTERNAL"]);
  });
});

describe("marker catalogue", () => {
  // The catalogues are module-private, so this reads the source: every key
  // must appear once in EN and once in DE. A single hit means one locale
  // silently falls back to the raw key.
  it("carries a description for every marker in both locales", async () => {
    const { readFileSync } = await import("node:fs");
    // vitest runs with assets/ui as the working directory.
    const src = readFileSync("src/lib/i18n.ts", "utf8");
    const keys = [
      "centrals.behavior.markers_hint",
      "centrals.behavior.marker.hahm",
      "centrals.behavior.marker.hx",
      "centrals.behavior.marker.internal",
      "centrals.behavior.marker.mqtt",
    ];
    for (const k of keys) {
      const hits = src.split(`"${k}":`).length - 1;
      expect(hits, `${k} (expected EN + DE)`).toBe(2);
    }
  });
});
