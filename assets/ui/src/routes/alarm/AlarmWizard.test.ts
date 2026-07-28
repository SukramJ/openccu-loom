// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor, screen } from "@testing-library/svelte";
import type { AlarmOutputCandidate, DeviceSummary } from "$lib/api/types";

const mockRefresh = vi.fn();
vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: { refresh: (...args: unknown[]) => mockRefresh(...args) },
}));

// The sensors step reads deviceStore.items directly — replace the whole
// module (same pattern as AlarmSensors.test.ts) so this file never pulls
// in the real store's auth/WS import chain. `mockDevices` must be set
// BEFORE render(): the wizard's candidate list is a plain $derived read of
// this getter, not a subscription, so it only ever sees the value present
// at the component's first evaluation.
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

const mockCreateAlarmArea = vi.fn();
const mockPutAlarmArea = vi.fn();
const mockPutAlarmAreaSensors = vi.fn();
const mockPutAlarmAreaOutputs = vi.fn();
const mockListAlarmOutputCandidates = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    createAlarmArea: (...args: unknown[]) => mockCreateAlarmArea(...args),
    putAlarmArea: (...args: unknown[]) => mockPutAlarmArea(...args),
    putAlarmAreaSensors: (...args: unknown[]) => mockPutAlarmAreaSensors(...args),
    putAlarmAreaOutputs: (...args: unknown[]) => mockPutAlarmAreaOutputs(...args),
    listAlarmOutputCandidates: (...args: unknown[]) => mockListAlarmOutputCandidates(...args),
  },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
}));

import AlarmWizard from "./AlarmWizard.svelte";
import { alarmWizardStore, ALARM_WIZARD_MAX_TRIGGER_SECONDS } from "$lib/stores/alarmWizard.svelte";

function device(overrides: Partial<DeviceSummary> = {}): DeviceSummary {
  return {
    address: "SWDO001",
    central: "ccu1",
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-SWDO",
    name: "Front door",
    available: true,
    channels_count: 2,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...overrides,
  };
}

// The canonical (and, deliberately, only) output candidate this file ever
// fetches. alarmWizardStore's output-candidate cache is intentionally NOT
// cleared by store.reset() (it is a read-only capability list meant to
// survive a wizard reset — see alarmWizard.svelte.ts) — so once any test in
// this file reaches step 3, the module-singleton store caches whatever this
// mock resolved for the rest of the file's run. Keeping that resolved value
// fixed and consistent (rather than reconfiguring it per test) means every
// later test that lands on step 3 sees the same, predictable candidate
// regardless of which test happened to trigger the first real fetch.
function outputCandidate(overrides: Partial<AlarmOutputCandidate> = {}): AlarmOutputCandidate {
  return {
    central: "ccu1",
    device_address: "SIR001",
    device_name: "Hallway siren",
    model: "HmIP-ASIR-2",
    channel_address: "SIR001:3",
    channel_no: 3,
    channel_name: "Hallway siren",
    classes: ["acoustic_siren"],
    kind: "acoustic_siren",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockDevices = [];
  mockRefresh.mockResolvedValue(undefined);
  mockCreateAlarmArea.mockResolvedValue({ id: "area-1", name: "Ground floor" });
  mockPutAlarmArea.mockResolvedValue(undefined);
  mockPutAlarmAreaSensors.mockResolvedValue(undefined);
  mockPutAlarmAreaOutputs.mockResolvedValue(undefined);
  mockListAlarmOutputCandidates.mockResolvedValue([outputCandidate()]);
  location.hash = "";
  // The wizard store is a module singleton (state survives navigating away
  // from the route) — reset it so each test starts from step 1 regardless
  // of where a previous test left off. This does NOT clear the output-
  // candidate cache by design (see the comment on outputCandidate() above).
  alarmWizardStore.reset();
});

afterEach(() => {
  cleanup();
});

// `screen` binds queries to document.body, so every step's footer button
// can be found the same way regardless of which step is currently mounted —
// mirrors the pattern in routes/Setup.test.ts.
async function next() {
  await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.next/ }));
}
async function back() {
  await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.back/ }));
}
async function skip() {
  await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.skip/ }));
}

// This describe MUST run first: it is the only place in the file that
// controls the *first* call to listAlarmOutputCandidates (a pending
// promise, then a rejection), and it is what seeds the shared cache with
// outputCandidate() via the retry. Every later test that reaches step 3
// relies on that cached candidate already being present.
describe("AlarmWizard — output picker (step 3)", () => {
  it("shows a loading state, then an error on failure; retry recovers with the candidate list", async () => {
    let rejectFirstFetch!: (err: Error) => void;
    mockListAlarmOutputCandidates.mockImplementationOnce(
      () =>
        new Promise<AlarmOutputCandidate[]>((_resolve, reject) => {
          rejectFirstFetch = reject;
        }),
    );
    render(AlarmWizard);
    await next(); // -> sensors
    await next(); // -> outputs

    expect(screen.getByRole("status")).toBeTruthy();
    expect(screen.getByText("common.loading")).toBeTruthy();

    rejectFirstFetch(new Error("boom"));
    expect(await screen.findByText(/boom/)).toBeTruthy();
    expect(mockListAlarmOutputCandidates).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByRole("button", { name: "common.reload" }));

    // The retry's call falls through to the mock's persistent resolved
    // value (the canonical candidate) — recovery shows the row and clears
    // the error.
    expect(await screen.findByText("Hallway siren")).toBeTruthy();
    expect(screen.queryByText(/boom/)).toBeNull();
    expect(mockListAlarmOutputCandidates).toHaveBeenCalledTimes(2);
  });

  it("toggling a candidate checkbox adds/removes an output row in the wizard store", async () => {
    render(AlarmWizard);
    await next(); // -> sensors
    await next(); // -> outputs

    // Reuses the candidate the previous test already cached — the guard in
    // loadOutputCandidates() skips a redundant fetch once loaded.
    await screen.findByText("Hallway siren");
    const checkbox = screen.getByRole("checkbox") as HTMLInputElement;
    expect(checkbox.checked).toBe(false);

    await fireEvent.click(checkbox);

    expect(alarmWizardStore.selectedOutputs).toHaveLength(1);
    expect(alarmWizardStore.selectedOutputs[0]).toMatchObject({
      class: "acoustic_siren",
      central: "ccu1",
      channel_address: "SIR001:3",
    });
    expect(checkbox.checked).toBe(true);

    await fireEvent.click(checkbox);

    expect(alarmWizardStore.selectedOutputs).toHaveLength(0);
    expect(checkbox.checked).toBe(false);
  });
});

describe("AlarmWizard — step navigation", () => {
  it("advances through every step in order via Next", async () => {
    render(AlarmWizard);

    expect(screen.getByText("alarm.wizard.step.areas")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.sensors")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.outputs")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.delays")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.codes")).toBeTruthy();
    await next();
    expect(screen.getByText("alarm.wizard.step.done")).toBeTruthy();
  });

  it("Back returns to the previous step without losing what was already entered", async () => {
    render(AlarmWizard);
    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });
    await next();
    expect(screen.getByText("alarm.wizard.step.sensors")).toBeTruthy();

    await back();

    expect(screen.getByText("alarm.wizard.step.areas")).toBeTruthy();
    expect((screen.getByLabelText("alarm.area.name") as HTMLInputElement).value).toBe(
      "Ground floor",
    );
  });

  it("Back is disabled on the first step", () => {
    render(AlarmWizard);
    expect(screen.getByRole("button", { name: /alarm.wizard.back/ })).toBeDisabled();
  });
});

describe("AlarmWizard — skip clears the current step's data before advancing", () => {
  it("skip on the area step clears the entered name", async () => {
    render(AlarmWizard);
    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });

    await skip();

    expect(alarmWizardStore.step).toBe(2);
    expect(alarmWizardStore.areaName).toBe("");
  });

  it("skip on the sensors step clears every sensor selected on it", async () => {
    mockDevices = [device()];
    render(AlarmWizard);
    await next(); // -> sensors
    const [, deviceCheckbox] = screen.getAllByRole("checkbox");
    await fireEvent.click(deviceCheckbox);
    expect(alarmWizardStore.selectedSensors).toHaveLength(1);

    await skip();

    expect(alarmWizardStore.step).toBe(3);
    expect(alarmWizardStore.selectedSensors).toHaveLength(0);
  });

  it("skip on the outputs step clears every output selected on it", async () => {
    render(AlarmWizard);
    await next(); // -> sensors
    await next(); // -> outputs
    await screen.findByText("Hallway siren"); // the shared cached candidate
    await fireEvent.click(screen.getByRole("checkbox"));
    expect(alarmWizardStore.selectedOutputs).toHaveLength(1);

    await skip();

    expect(alarmWizardStore.step).toBe(4);
    expect(alarmWizardStore.selectedOutputs).toHaveLength(0);
  });

  it("skip on the delays step resets every mode's delays back to the defaults", async () => {
    render(AlarmWizard);
    await next(); // -> sensors
    await next(); // -> outputs
    await next(); // -> delays
    const perimeterExit = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    await fireEvent.input(perimeterExit, { target: { value: "99" } });
    expect(alarmWizardStore.delays.perimeter.exit).toBe(99);

    await skip();

    expect(alarmWizardStore.step).toBe(5);
    expect(alarmWizardStore.delays.perimeter.exit).toBe(30);
  });
});

describe("AlarmWizard — sensor picker (step 2)", () => {
  it("renders device-store candidates and toggling a checkbox adds/removes a sensor row", async () => {
    mockDevices = [device()];
    render(AlarmWizard);
    await next(); // -> sensors

    expect(await screen.findByText("Front door")).toBeTruthy();
    const [, deviceCheckbox] = screen.getAllByRole("checkbox") as HTMLInputElement[];
    expect(deviceCheckbox.checked).toBe(false);

    await fireEvent.click(deviceCheckbox);

    expect(alarmWizardStore.selectedSensors).toHaveLength(1);
    expect(alarmWizardStore.selectedSensors[0]).toMatchObject({
      central: "ccu1",
      channel_address: "SWDO001:1",
      // guessSensorType matches "swdo" in the model string before it falls
      // back to "door" — the candidate's model drives the guessed type.
      type: "window",
    });
    expect(deviceCheckbox.checked).toBe(true);
    expect(screen.getByText('alarm.sensors.selected:{"count":1}')).toBeTruthy();

    await fireEvent.click(deviceCheckbox);

    expect(alarmWizardStore.selectedSensors).toHaveLength(0);
    expect(deviceCheckbox.checked).toBe(false);
  });
});

describe("AlarmWizard — delay clamp (step 4)", () => {
  async function toDelaysStep() {
    render(AlarmWizard);
    await next(); // -> sensors
    await next(); // -> outputs
    await next(); // -> delays
  }

  it("clamps a trigger-delay value above the 600s cap down to the max", async () => {
    await toDelaysStep();

    // Rows follow ALARM_WIZARD_MODE_ORDER, each with (exit, entry, trigger)
    // spinbuttons in that order — index 2 is the first mode's (perimeter)
    // trigger field, the one field on the table that carries a `max`.
    const perimeterTrigger = screen.getAllByRole("spinbutton")[2] as HTMLInputElement;

    await fireEvent.input(perimeterTrigger, { target: { value: "900" } });

    expect(alarmWizardStore.delays.perimeter.trigger).toBe(ALARM_WIZARD_MAX_TRIGGER_SECONDS);
    await waitFor(() =>
      expect(perimeterTrigger.value).toBe(String(ALARM_WIZARD_MAX_TRIGGER_SECONDS)),
    );
  });

  it("does not clamp the exit/entry delay fields, which carry no upper cap", async () => {
    await toDelaysStep();

    const perimeterExit = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    await fireEvent.input(perimeterExit, { target: { value: "900" } });

    expect(alarmWizardStore.delays.perimeter.exit).toBe(900);
  });
});

describe("AlarmWizard — finish (happy path)", () => {
  it("creates the area, then PUTs sensors, then PUTs outputs — in that order — refreshes the panel, resets the wizard, and returns to the overview", async () => {
    mockDevices = [device()];
    render(AlarmWizard);

    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });
    await next(); // -> sensors
    const [, sensorCheckbox] = screen.getAllByRole("checkbox");
    await fireEvent.click(sensorCheckbox);
    await next(); // -> outputs
    await screen.findByText("Hallway siren"); // the shared cached candidate
    await fireEvent.click(screen.getByRole("checkbox"));
    await next(); // -> delays
    await next(); // -> codes
    await next(); // -> done
    expect(screen.getByText("alarm.wizard.step.done")).toBeTruthy();

    await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.finish/ }));

    await waitFor(() => expect(mockRefresh).toHaveBeenCalledOnce());

    expect(mockCreateAlarmArea).toHaveBeenCalledOnce();
    expect(mockCreateAlarmArea.mock.calls[0][0]).toMatchObject({ name: "Ground floor" });

    expect(mockPutAlarmAreaSensors).toHaveBeenCalledOnce();
    expect(mockPutAlarmAreaSensors.mock.calls[0][0]).toBe("area-1");
    expect(mockPutAlarmAreaSensors.mock.calls[0][1]).toMatchObject([
      { channel_address: "SWDO001:1" },
    ]);

    expect(mockPutAlarmAreaOutputs).toHaveBeenCalledOnce();
    expect(mockPutAlarmAreaOutputs.mock.calls[0][0]).toBe("area-1");
    expect(mockPutAlarmAreaOutputs.mock.calls[0][1]).toMatchObject([
      { channel_address: "SIR001:3" },
    ]);

    // Ordering matters: the area write always precedes both PUTs, and
    // sensors are always written before outputs.
    expect(mockCreateAlarmArea.mock.invocationCallOrder[0]).toBeLessThan(
      mockPutAlarmAreaSensors.mock.invocationCallOrder[0],
    );
    expect(mockPutAlarmAreaSensors.mock.invocationCallOrder[0]).toBeLessThan(
      mockPutAlarmAreaOutputs.mock.invocationCallOrder[0],
    );
    expect(mockPutAlarmAreaOutputs.mock.invocationCallOrder[0]).toBeLessThan(
      mockRefresh.mock.invocationCallOrder[0],
    );

    expect(alarmWizardStore.step).toBe(1);
    expect(alarmWizardStore.areaName).toBe("");
    expect(alarmWizardStore.createdAreaId).toBeNull();
    expect(location.hash).toBe("#/alarm");
  });
});

describe("AlarmWizard — finish partial failure keeps the created area", () => {
  it("does not create a second area on retry once the sensors PUT has failed", async () => {
    mockDevices = [device()];
    render(AlarmWizard);

    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });
    await next(); // -> sensors
    const [, sensorCheckbox] = screen.getAllByRole("checkbox");
    await fireEvent.click(sensorCheckbox);
    await next(); // -> outputs (no output selected — irrelevant to this failure)
    await next(); // -> delays
    await next(); // -> codes
    await next(); // -> done

    mockPutAlarmAreaSensors.mockRejectedValueOnce(new Error("network down"));

    await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.finish/ }));

    await waitFor(() => expect(mockPutAlarmAreaSensors).toHaveBeenCalledOnce());
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /alarm.wizard.finish/ })).not.toBeDisabled(),
    );

    expect(mockCreateAlarmArea).toHaveBeenCalledOnce();
    expect(mockPutAlarmArea).not.toHaveBeenCalled();
    // The failure is caught, not thrown further — the area id it created
    // must survive so a retry never re-creates the area.
    expect(alarmWizardStore.createdAreaId).toBe("area-1");
    expect(mockRefresh).not.toHaveBeenCalled();
    expect(location.hash).toBe("");

    // Retry: the sensors PUT succeeds this time.
    mockPutAlarmAreaSensors.mockResolvedValueOnce(undefined);
    await fireEvent.click(screen.getByRole("button", { name: /alarm.wizard.finish/ }));

    await waitFor(() => expect(mockRefresh).toHaveBeenCalledOnce());

    // Still exactly one createAlarmArea call across both attempts — the
    // retry goes through putAlarmArea against the already-created id
    // instead of creating a duplicate area.
    expect(mockCreateAlarmArea).toHaveBeenCalledOnce();
    expect(mockPutAlarmArea).toHaveBeenCalledOnce();
    expect(mockPutAlarmArea).toHaveBeenCalledWith(
      "area-1",
      expect.objectContaining({ id: "area-1", name: "Ground floor" }),
    );
    expect(mockPutAlarmAreaSensors).toHaveBeenCalledTimes(2);
    expect(location.hash).toBe("#/alarm");
  });
});

describe("AlarmWizard — progress survives a route unmount/remount", () => {
  it("keeps step + collected data in the store across unmounting and remounting the wizard route", async () => {
    const first = render(AlarmWizard);
    await fireEvent.input(screen.getByLabelText("alarm.area.name"), {
      target: { value: "Ground floor" },
    });
    await next(); // -> sensors
    expect(alarmWizardStore.step).toBe(2);

    first.unmount();

    // Same store singleton, freshly mounted component — as if the operator
    // navigated away to double-check something and came back to the wizard.
    render(AlarmWizard);

    expect(screen.getByText("alarm.wizard.step.sensors")).toBeTruthy();
    expect(alarmWizardStore.areaName).toBe("Ground floor");
  });
});
