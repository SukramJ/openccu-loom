// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";

const mockListPrograms = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listPrograms: (...args: unknown[]) => mockListPrograms(...args),
    executeProgram: vi.fn(),
    setProgramEnabled: vi.fn(),
  },
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

import ProgramList from "./ProgramList.svelte";

const heater = {
  id: "P-RULE",
  unique_id: "loom_vccu0000000_program_heater",
  name: "Heater",
  central: "",
  active: true,
  condition_summary: "Wohnzimmer >= 20.00 && Flur == 1.00",
  activity_summary: "Bücherregal := 1.00",
  last_executed: "2026-07-20T08:15:00Z",
};

const bare = {
  id: "P-BARE",
  unique_id: "loom_vccu0000000_program_bare",
  name: "Bare",
  central: "",
  active: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockListPrograms.mockResolvedValue([heater, bare]);
});

afterEach(() => cleanup());

describe("ProgramList rule-summary columns", () => {
  it("renders the condition and activity summaries and a formatted last-executed timestamp", async () => {
    const { container, findByText } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    // The compact rule summaries surface verbatim from the DTO.
    expect(await findByText("Wohnzimmer >= 20.00 && Flur == 1.00")).toBeTruthy();
    expect(await findByText("Bücherregal := 1.00")).toBeTruthy();

    // The last-executed cell is formatted from the ISO timestamp (locale
    // string always carries the year).
    const text = container.textContent ?? "";
    expect(text).toContain("2026");
  });

  it("shows the never-executed placeholder for a program without a last-executed timestamp", async () => {
    const { findAllByText } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    // Both the header column and the empty row can match "never"/"nie";
    // at least the bare program's cell must render the placeholder.
    const placeholders = await findAllByText(/^(never|nie)$/);
    expect(placeholders.length).toBeGreaterThan(0);
  });

  it("renders the condition/activity column headers", async () => {
    const { container } = render(ProgramList);
    // The DataTable header row only paints once the async load settles, so
    // retry the query until the columns exist instead of racing the render.
    await waitFor(() => {
      const joined = [...container.querySelectorAll("th")]
        .map((h) => h.textContent ?? "")
        .join("|");
      expect(joined).toMatch(/Condition|Bedingung/);
      expect(joined).toMatch(/Activity|Aktivität/);
      expect(joined).toMatch(/Last executed|Zuletzt ausgeführt/);
    });
  });
});
