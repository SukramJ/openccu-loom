// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

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

describe("ProgramList system-programs toggle", () => {
  it("requests programs with include_internal=false on first load", async () => {
    render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));
    expect(mockListPrograms.mock.calls[0][0]).toBe(false);
  });

  it("reloads with include_internal=true when the system-programs switch is turned on", async () => {
    const { getByRole } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));
    // The switch is disabled while the initial load() is in flight; the mock
    // call above resolves synchronously before that promise settles, so wait
    // for the switch to re-enable before interacting with it — otherwise the
    // click is a no-op (bits-ui's Switch ignores clicks while disabled).
    await waitFor(() => expect(getByRole("switch")).not.toBeDisabled());

    await fireEvent.click(getByRole("switch"));

    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(2));
    expect(mockListPrograms.mock.calls[1][0]).toBe(true);
  });
});

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
