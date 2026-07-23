// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  cleanup,
  fireEvent,
  waitFor,
} from "@testing-library/svelte";

const mockListChannels = vi.fn();
const mockListDataPoints = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listChannels: (...a: unknown[]) => mockListChannels(...a),
    listDataPoints: (...a: unknown[]) => mockListDataPoints(...a),
  },
}));

vi.mock("$lib/stores/devices.svelte", () => ({
  // Inlined (not a top-level const) because vi.mock is hoisted above declarations.
  deviceStore: {
    items: [
      {
        address: "ABC0000001",
        central: "ccu1",
        interface_id: "ccu1-HmIP-RF",
        name: "Wohnzimmer",
        model: "HmIP-eTRV",
        model_label: "",
      },
      {
        address: "DEF0000002",
        central: "ccu1",
        interface_id: "ccu1-HmIP-RF",
        name: "Küche",
        model: "HmIP-STHD",
        model_label: "",
      },
    ],
    refresh: vi.fn(),
  },
}));
vi.mock("$lib/stores/toast.svelte", () => ({
  toastStore: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("$lib/i18n", () => ({ t: (k: string) => k }));

import DiagramSeriesPicker from "./DiagramSeriesPicker.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  mockListChannels.mockResolvedValue({
    items: [
      { address: "ABC0000001:1", number: 1, name: "", type_label: "CLIMATE" },
    ],
  });
  mockListDataPoints.mockResolvedValue([
    { parameter: "ACTUAL_TEMPERATURE", parameter_label: "Ist-Temperatur" },
  ]);
});
afterEach(() => cleanup());

describe("DiagramSeriesPicker — device pick derives central/interface", () => {
  it("clicking a device emits derived central + interface_id and loads its channels", async () => {
    let emitted: Record<string, unknown> | null = null;
    const { getByText } = render(DiagramSeriesPicker, {
      props: {
        series: { central: "" },
        index: 0,
        onChange: (s: Record<string, unknown>) => (emitted = s),
        onRemove: vi.fn(),
      },
    });

    await fireEvent.click(getByText("Wohnzimmer"));

    await waitFor(() =>
      expect(mockListChannels).toHaveBeenCalledWith("ABC0000001"),
    );
    expect(emitted).toMatchObject({
      central: "ccu1",
      interface_id: "ccu1-HmIP-RF",
      channel_address: "",
      parameter: "",
    });
  });

  it("filters the device list by the search box", async () => {
    const { getByPlaceholderText, queryByText } = render(DiagramSeriesPicker, {
      props: {
        series: { central: "" },
        index: 0,
        onChange: vi.fn(),
        onRemove: vi.fn(),
      },
    });
    expect(queryByText("Küche")).not.toBeNull();

    await fireEvent.input(getByPlaceholderText("diagrams.picker.search"), {
      target: { value: "Wohn" },
    });

    await waitFor(() => expect(queryByText("Küche")).toBeNull());
    expect(queryByText("Wohnzimmer")).not.toBeNull();
  });
});
