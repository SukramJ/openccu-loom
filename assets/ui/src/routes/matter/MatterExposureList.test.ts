// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/svelte";
import type { MatterExposure } from "$lib/api/matter-types";

// The matterStore is a module-level singleton; mock the whole module so we
// can drive the exposure list without any real API or WebSocket traffic.
let mockExposures: MatterExposure[] = [];
const pending = new Map<string, unknown>();

function key(e: {
  central_name: string;
  device_address: string;
  channel_no: number;
  dp_kind: string;
  dp_key: string;
}): string {
  return `${e.central_name}|${e.device_address}|${e.channel_no}|${e.dp_kind}|${e.dp_key}`;
}

vi.mock("$lib/stores/matter.svelte", () => ({
  matterStore: {
    get exposures() {
      return mockExposures;
    },
    get exposuresLoading() {
      return false;
    },
    get exposuresError() {
      return null;
    },
    get pendingUpdates() {
      return pending;
    },
    get hasDirty() {
      return false;
    },
    exposureKey: key,
    loadExposures: vi.fn().mockResolvedValue(undefined),
    markDirty: vi.fn(),
    discardDirty: vi.fn(),
    saveBulk: vi.fn().mockResolvedValue(0),
  },
}));

vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn(), info: vi.fn() },
}));

vi.mock("$lib/i18n", () => ({
  t: (k: string) => k,
}));

import MatterExposureList from "./MatterExposureList.svelte";

function makeExposure(overrides: Partial<MatterExposure> = {}): MatterExposure {
  return {
    central_name: "ccu",
    device_address: "DEV",
    channel_no: 1,
    dp_kind: "generic",
    dp_key: "DEV:1:STATE",
    parameter_label: "State",
    display_name: "Device",
    enabled: false,
    friendly_name: "",
    mappable: "mappable",
    device_type: 0x0100,
    device_type_label: "On/Off Light",
    clusters: [6],
    reason: "",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  mockExposures = [];
  pending.clear();
});

afterEach(() => {
  cleanup();
});

describe("MatterExposureList — grouping by device", () => {
  it("prints each device name once as a group header, not per row", () => {
    mockExposures = [
      makeExposure({
        device_address: "SIR1",
        display_name: "Alarmsirene FL",
        channel_no: 1,
        dp_key: "SIR1:1:STATE",
        parameter_label: "State",
      }),
      makeExposure({
        device_address: "SIR1",
        display_name: "Alarmsirene FL",
        channel_no: 2,
        dp_key: "SIR1:2:LEVEL",
        parameter_label: "Level",
        mappable: "unmappable",
      }),
      makeExposure({
        device_address: "LMP1",
        display_name: "Bookshelf Lamp",
        channel_no: 1,
        dp_key: "LMP1:1:STATE",
        parameter_label: "State",
      }),
    ];
    const { getAllByText } = render(MatterExposureList);
    // Device name lives only in the group header now → exactly one match.
    expect(getAllByText("Alarmsirene FL")).toHaveLength(1);
    expect(getAllByText("Bookshelf Lamp")).toHaveLength(1);
    // Parameter labels still render once per exposure row.
    expect(getAllByText("Level")).toHaveLength(1);
  });

  it("renders one select checkbox per exposure row", () => {
    mockExposures = [
      makeExposure({ device_address: "SIR1", display_name: "Siren", channel_no: 1, dp_key: "SIR1:1:A" }),
      makeExposure({ device_address: "SIR1", display_name: "Siren", channel_no: 2, dp_key: "SIR1:2:B" }),
    ];
    const { container } = render(MatterExposureList);
    const checkboxes = container.querySelectorAll('input[type="checkbox"]');
    expect(checkboxes).toHaveLength(2);
  });
});

describe("MatterExposureList — status icons + legend", () => {
  it("shows the legend with the four state labels", () => {
    mockExposures = [makeExposure()];
    const { getByText } = render(MatterExposureList);
    expect(getByText("matter.expose.legend:")).toBeTruthy();
    expect(getByText("matter.expose.state_exposed")).toBeTruthy();
    expect(getByText("matter.expose.state_partial")).toBeTruthy();
    expect(getByText("matter.expose.state_available")).toBeTruthy();
    expect(getByText("matter.expose.state_unmappable")).toBeTruthy();
  });

  it("renders an SVG status icon (not a Unicode glyph) per row", () => {
    mockExposures = [makeExposure({ enabled: true, mappable: "mappable" })];
    const { container } = render(MatterExposureList);
    // The state cell renders a Lucide <svg>, not a text glyph.
    const svgs = container.querySelectorAll("tbody svg");
    expect(svgs.length).toBeGreaterThan(0);
  });
});

describe("MatterExposureList — disabled checkbox reason", () => {
  it("gives an unmappable row's checkbox a disabled + title reason", () => {
    mockExposures = [
      makeExposure({ dp_key: "DEV:1:X", mappable: "unmappable" }),
    ];
    const { container } = render(MatterExposureList);
    const box = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    expect(box.disabled).toBe(true);
    expect(box.getAttribute("title")).toBe("matter.expose.unmappable_checkbox_title");
  });
});

describe("MatterExposureList — empty + search", () => {
  it("shows the empty state when there are no exposures", () => {
    mockExposures = [];
    const { getByText } = render(MatterExposureList);
    expect(getByText("matter.expose.empty")).toBeTruthy();
  });

  it("filters rows via the search box", async () => {
    mockExposures = [
      makeExposure({ device_address: "A1", display_name: "Alpha", dp_key: "A1:1:STATE" }),
      makeExposure({ device_address: "B2", display_name: "Beta", dp_key: "B2:1:STATE" }),
    ];
    const { getByPlaceholderText, queryAllByText } = render(MatterExposureList);
    const search = getByPlaceholderText("matter.expose.search_placeholder");
    await fireEvent.input(search, { target: { value: "Alpha" } });
    expect(queryAllByText("Alpha")).toHaveLength(1);
    expect(queryAllByText("Beta")).toHaveLength(0);
  });
});
