// @vitest-environment happy-dom
//
// A sysvar mirror writes a CCU system variable. An openccu-lite box has no
// system variables, so the add dialog must not offer the class — or a lite
// central as its target — when the fleet reports the feature absent for a
// lasting reason.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen, within } from "@testing-library/svelte";
import type { AlarmZone, DeviceSummary } from "$lib/api/types";

vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return [{ id: "zone-1", name: "Ground floor" }] as AlarmZone[];
    },
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

let mockDevices: DeviceSummary[] = [];
vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    get items() {
      return mockDevices;
    },
    refresh: vi.fn(),
    ensureStream: vi.fn(),
  },
}));

vi.mock("$lib/stores/areas.svelte", () => ({
  areasStore: {
    get areas() {
      return [];
    },
    ensureLoaded: vi.fn(),
    areaIdOf: vi.fn(() => undefined),
  },
}));

type Central = { name: string; features: Record<string, { available: boolean; reason?: string }> };
let mockCentrals: Central[] = [];
vi.mock("$lib/stores/centrals.svelte", () => ({
  centralStore: {
    get items() {
      return mockCentrals;
    },
    // Mirrors the real store: only a lasting reason counts as lacking.
    centralsLacking: (key: string) =>
      mockCentrals.filter((c) => {
        const f = c.features[key];
        return f !== undefined && !f.available && f.reason !== "not_ready";
      }),
  },
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listAlarmZoneOutputs: vi.fn().mockResolvedValue([]),
    putAlarmZoneOutputs: vi.fn().mockResolvedValue(undefined),
    listAlarmOutputCandidates: vi.fn().mockResolvedValue([]),
    listSysvars: vi.fn().mockResolvedValue([]),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(true) },
}));

vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

// The real Select wraps bits-ui's floating-portal listbox, which happy-dom
// cannot drive (see SelectStub.svelte).
vi.mock("$lib/components/ui/Select.svelte", async () => {
  const mod = await import("../__testutils__/SelectStub.svelte");
  return { default: mod.default };
});

import AlarmOutputs from "./AlarmOutputs.svelte";

const lite = (name: string): Central => ({
  name,
  features: { "hub.sysvars": { available: false, reason: "not_supported_by_system" } },
});
const ccu = (name: string): Central => ({ name, features: { "hub.sysvars": { available: true } } });

function device(central: string): DeviceSummary {
  return { address: `${central}-DEV`, central } as DeviceSummary;
}

async function openAddDialog() {
  render(AlarmOutputs);
  const [addButton] = await screen.findAllByRole("button", { name: /alarm.outputs.add/ });
  await fireEvent.click(addButton);
  return screen.findByRole("dialog");
}

function classPicker(dialog: HTMLElement): HTMLElement {
  return within(dialog).getAllByRole("listbox")[0];
}

beforeEach(() => {
  vi.clearAllMocks();
  mockCentrals = [];
  mockDevices = [];
});

afterEach(() => cleanup());

describe("AlarmOutputs — sysvar mirror gating", () => {
  it("does not offer the sysvar mirror class when every central is an openccu-lite box", async () => {
    mockCentrals = [lite("box")];
    const dialog = await openAddDialog();

    const picker = classPicker(dialog);
    expect(within(picker).getByText("alarm.output_class.acoustic_siren")).toBeTruthy();
    expect(within(picker).queryByText("alarm.output_class.sysvar_mirror")).toBeNull();
  });

  it("offers the sysvar mirror class when a CCU central has system variables", async () => {
    mockCentrals = [ccu("ccu1")];
    const dialog = await openAddDialog();

    expect(within(classPicker(dialog)).getByText("alarm.output_class.sysvar_mirror")).toBeTruthy();
  });

  it("offers the sysvar mirror class while a CCU is still booting", async () => {
    mockCentrals = [
      { name: "ccu1", features: { "hub.sysvars": { available: false, reason: "not_ready" } } },
    ];
    const dialog = await openAddDialog();

    expect(within(classPicker(dialog)).getByText("alarm.output_class.sysvar_mirror")).toBeTruthy();
  });

  it("leaves an openccu-lite central out of a mixed fleet's sysvar mirror target picker", async () => {
    mockCentrals = [ccu("ccu1"), lite("box")];
    mockDevices = [device("ccu1"), device("box")];
    const dialog = await openAddDialog();

    await fireEvent.click(within(classPicker(dialog)).getByText("alarm.output_class.sysvar_mirror"));
    await waitFor(() => expect(within(dialog).getByText("alarm.outputs.sysvar.central")).toBeTruthy());

    const centralPicker = within(dialog).getAllByRole("listbox")[1];
    expect(within(centralPicker).getByText("ccu1")).toBeTruthy();
    expect(within(centralPicker).queryByText("box")).toBeNull();
  });
});
