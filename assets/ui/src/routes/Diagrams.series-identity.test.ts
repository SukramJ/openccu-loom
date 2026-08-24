// @vitest-environment happy-dom
//
// DiagramSeriesPicker seeds its device / channel / data-point state once at
// construction and never re-reads the `series` prop, so the editor's series
// list must be keyed by a stable identity. Keyed by array index, removing a
// row shifts the surviving pickers onto another row's data: the picker then
// offers device A's channels and parameters while emitting device B's
// address, and a save persists a series whose parameter belongs to a
// different device (and central) than its channel address.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  cleanup,
  waitFor,
  screen,
  fireEvent,
} from "@testing-library/svelte";

const {
  mockListDiagrams,
  mockListChannels,
  mockListDataPoints,
  mockUpdateDiagram,
} = vi.hoisted(() => ({
  mockListDiagrams: vi.fn(),
  mockListChannels: vi.fn(),
  mockListDataPoints: vi.fn(),
  mockUpdateDiagram: vi.fn(),
}));

vi.mock("$lib/api/client", () => ({
  api: {
    listChannels: (...a: unknown[]) => mockListChannels(...a),
    listDataPoints: (...a: unknown[]) => mockListDataPoints(...a),
  },
  listDiagrams: (...a: unknown[]) => mockListDiagrams(...a),
  createDiagram: vi.fn(),
  updateDiagram: (...a: unknown[]) => mockUpdateDiagram(...a),
  deleteDiagram: vi.fn(),
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

vi.mock("$lib/stores/info.svelte", () => ({
  infoStore: {
    get info() {
      return { capabilities: ["history.v1", "diagrams.v1"] };
    },
    ensure: vi.fn().mockResolvedValue(undefined),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));
vi.mock("$lib/i18n", () => ({ t: (k: string) => k }));

// The saved diagrams below render a chart each; it only needs to exist.
vi.mock("$lib/components/MultiSeriesChart.svelte", () => ({
  default: () => {},
}));

const DEVICES = [
  {
    address: "AAA0000001",
    name: "Dev A",
    model: "HmIP-A",
    central: "ccu-a",
    interface_id: "ccu-a-HmIP-RF",
  },
  {
    address: "BBB0000002",
    name: "Dev B",
    model: "HmIP-B",
    central: "ccu-b",
    interface_id: "ccu-b-HmIP-RF",
  },
  {
    address: "CCC0000003",
    name: "Dev C",
    model: "HmIP-C",
    central: "ccu-c",
    interface_id: "ccu-c-HmIP-RF",
  },
];

vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: {
    get items() {
      return DEVICES;
    },
    refresh: vi.fn().mockResolvedValue(undefined),
  },
}));

import Diagrams from "./Diagrams.svelte";

/** "AAA0000001" → "A" — the per-device marker used in labels below. */
function tag(address: string): string {
  return address.slice(0, 1);
}

function seriesOf(device: (typeof DEVICES)[number]) {
  return {
    central: device.central,
    interface_id: device.interface_id,
    channel_address: `${device.address}:1`,
    parameter: `P_${tag(device.address)}`,
    label: `${device.name} / Label ${tag(device.address)}`,
  };
}

const DIAGRAM = {
  id: "d1",
  name: "Three series",
  visibility: "private",
  config: { series: DEVICES.map(seriesOf) },
};

function optionLabels(select: HTMLSelectElement): string[] {
  return Array.from(select.options).map((o) => o.textContent?.trim() ?? "");
}

beforeEach(() => {
  vi.clearAllMocks();
  mockListDiagrams.mockResolvedValue([DIAGRAM]);
  // GET /devices/{addr}/channels answers a bare array, like listDataPoints
  // below — see the array schema in assets/openapi.yaml.
  mockListChannels.mockImplementation((addr: string) =>
    Promise.resolve([
      { address: `${addr}:1`, number: 1, name: `Chan ${tag(addr)}` },
    ]),
  );
  mockListDataPoints.mockImplementation((addr: string) =>
    Promise.resolve([
      {
        parameter: `P_${tag(addr)}`,
        type: "FLOAT",
        parameter_label: `Label ${tag(addr)}`,
        unit: "",
      },
    ]),
  );
});

afterEach(() => cleanup());

async function openEditorWithThreeSeries() {
  render(Diagrams);
  await waitFor(() =>
    expect(screen.getByText(DIAGRAM.name)).toBeInTheDocument(),
  );
  await fireEvent.click(screen.getByText("common.edit"));
  await waitFor(() =>
    expect(screen.getAllByLabelText("diagrams.picker.channel")).toHaveLength(3),
  );
  // Each picker resolves its own device's channels and data points.
  await waitFor(() =>
    expect(screen.getAllByLabelText("diagrams.picker.value")).toHaveLength(3),
  );
}

describe("Diagrams — series editor identity", () => {
  it("leaves the surviving pickers on their own device after a row is removed", async () => {
    await openEditorWithThreeSeries();

    // Remove the first of the three series.
    await fireEvent.click(screen.getAllByText("diagrams.series.remove")[0]);

    await waitFor(() =>
      expect(screen.getAllByLabelText("diagrams.picker.channel")).toHaveLength(
        2,
      ),
    );
    const channels = screen.getAllByLabelText(
      "diagrams.picker.channel",
    ) as HTMLSelectElement[];
    const values = screen.getAllByLabelText(
      "diagrams.picker.value",
    ) as HTMLSelectElement[];

    // The two survivors are series B and C — the channel dropdown must offer
    // their own device's channel, and the picked value must stay selected.
    expect(optionLabels(channels[0])).toContain("#1 Chan B");
    expect(optionLabels(channels[1])).toContain("#1 Chan C");
    expect(channels[0].value).toBe("BBB0000002:1");
    expect(channels[1].value).toBe("CCC0000003:1");

    // ...and the value dropdown must list their own device's parameters, or
    // a pick would save a parameter from the removed device.
    expect(optionLabels(values[0])).toContain("Label B");
    expect(optionLabels(values[1])).toContain("Label C");
  });

  it("saves each surviving series with its own device's parameter", async () => {
    await openEditorWithThreeSeries();
    await fireEvent.click(screen.getAllByText("diagrams.series.remove")[0]);
    await waitFor(() =>
      expect(screen.getAllByLabelText("diagrams.picker.value")).toHaveLength(2),
    );

    // Re-pick the value on the first survivor: its dropdown must only be able
    // to emit a parameter belonging to the device its address points at.
    const values = screen.getAllByLabelText(
      "diagrams.picker.value",
    ) as HTMLSelectElement[];
    await fireEvent.change(values[0], { target: { value: "P_B" } });

    await fireEvent.click(screen.getByText("common.save"));
    await waitFor(() => expect(mockUpdateDiagram).toHaveBeenCalledTimes(1));

    const [, body] = mockUpdateDiagram.mock.calls[0] as [
      string,
      { config: { series: unknown[] } },
    ];
    expect(body.config.series).toEqual([
      expect.objectContaining({
        central: "ccu-b",
        channel_address: "BBB0000002:1",
        parameter: "P_B",
      }),
      expect.objectContaining({
        central: "ccu-c",
        channel_address: "CCC0000003:1",
        parameter: "P_C",
      }),
    ]);
  });
});
