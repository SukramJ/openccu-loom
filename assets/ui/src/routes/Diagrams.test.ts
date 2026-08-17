// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, screen, fireEvent } from "@testing-library/svelte";

const mockList = vi.fn();
const mockUpdate = vi.fn();
let capabilities: string[] = [];

vi.mock("$lib/api/client", () => ({
  listDiagrams: (...a: unknown[]) => mockList(...a),
  createDiagram: vi.fn(),
  updateDiagram: (...a: unknown[]) => mockUpdate(...a),
  deleteDiagram: vi.fn(),
  getHistory: vi.fn().mockResolvedValue([]),
  HistoryDisabledError: class extends Error {},
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
      return { capabilities };
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
// The series editor mounts DiagramSeriesPicker, which pulls the device store
// (and, transitively, the real API client). Stub it so the module graph loads
// without the full client mock — these tests never open the picker.
vi.mock("$lib/stores/devices.svelte", () => ({
  deviceStore: { items: [], refresh: vi.fn() },
}));
vi.mock("$lib/i18n", () => ({ t: (k: string) => k }));

import Diagrams from "./Diagrams.svelte";

beforeEach(() => {
  vi.clearAllMocks();
  capabilities = [];
  mockList.mockResolvedValue([]);
});
afterEach(() => cleanup());

describe("Diagrams — editing an existing diagram", () => {
  it("carries default_range_hours through to the save body unchanged", async () => {
    capabilities = ["history.v1"];
    mockList.mockResolvedValue([
      {
        id: "1",
        name: "Climate",
        visibility: "private",
        owner: "alice",
        config: {
          series: [
            { central: "ccu-a", channel_address: "AAA0000001:1", parameter: "TEMPERATURE" },
          ],
          default_range_hours: 168,
        },
      },
    ]);
    render(Diagrams);
    await waitFor(() => expect(screen.getByText("Climate")).toBeInTheDocument());

    await fireEvent.click(screen.getByText("common.edit"));
    await waitFor(() => expect(screen.getByText("common.save")).toBeInTheDocument());
    await fireEvent.click(screen.getByText("common.save"));

    await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(1));
    const [, body] = mockUpdate.mock.calls[0] as [string, { config: { default_range_hours?: number } }];
    // Before the fix this was dropped: save() rebuilt config from scratch
    // as `{ series }`, discarding the stored 7-day default range.
    expect(body.config.default_range_hours).toBe(168);
  });
});

describe("Diagrams — history gating (SV03)", () => {
  it("shows a 'history required' message when history recording is off", async () => {
    capabilities = [];
    render(Diagrams);
    await waitFor(() => {
      expect(screen.getByText("diagrams.history_required")).toBeInTheDocument();
    });
    // No 'New diagram' action when history is off.
    expect(screen.queryByText("diagrams.new")).not.toBeInTheDocument();
  });

  it("renders the diagrams surface when history recording is on", async () => {
    capabilities = ["history.v1"];
    mockList.mockResolvedValue([
      { id: "1", name: "Climate", visibility: "private", owner: "alice", config: { series: [] } },
    ]);
    render(Diagrams);
    await waitFor(() => {
      expect(screen.getByText("Climate")).toBeInTheDocument();
    });
    expect(screen.queryByText("diagrams.history_required")).not.toBeInTheDocument();
    expect(screen.getByText("diagrams.new")).toBeInTheDocument();
  });
});
