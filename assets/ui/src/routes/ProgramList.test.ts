// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor, fireEvent } from "@testing-library/svelte";

const mockListPrograms = vi.fn();
const mockExecuteProgram = vi.fn();
const mockDeleteProgram = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listPrograms: (...args: unknown[]) => mockListPrograms(...args),
    executeProgram: (...args: unknown[]) => mockExecuteProgram(...args),
    deleteProgram: (...args: unknown[]) => mockDeleteProgram(...args),
    setProgramEnabled: vi.fn(),
  },
  // Module-load hook of the auth store, which the favorites store imports
  // to scope pinned items to the signed-in operator.
  setUnauthorizedHandler: vi.fn(),
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

// confirmStore is mocked so the "run program" tests below can drive the
// check_conditions checkbox result directly instead of round-tripping
// through the real ConfirmDialog component (that rendering/toggling path
// is covered by ConfirmDialog.test.ts).
vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: {
    ask: vi.fn().mockResolvedValue(true),
    checkboxChecked: false,
  },
}));

import ProgramList from "./ProgramList.svelte";
import { confirmStore } from "$lib/stores/confirm.svelte";
import { toastStore } from "$lib/stores/toast.svelte";

// confirmStore.checkboxChecked is a getter-only property on the real store
// (callers only ever set it through the ConfirmDialog UI); the mock above
// backs it with a plain writable field, so tests reach it through this cast
// instead of fighting the real store's read-only type.
function setCheckboxChecked(v: boolean) {
  (confirmStore as unknown as { checkboxChecked: boolean }).checkboxChecked = v;
}

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
  confirmStore.ask = vi.fn().mockResolvedValue(true);
  setCheckboxChecked(false);
  toastStore.dismissAll();
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

describe("ProgramList run — check_conditions wiring", () => {
  async function clickFirstRunButton(container: HTMLElement) {
    // The DataTable body paints asynchronously past the load() promise
    // settling (a Svelte effect flush), so poll for the button instead of
    // racing the render — mirrors the header-column test above.
    let runButton: HTMLButtonElement | undefined;
    await waitFor(() => {
      runButton = [...container.querySelectorAll("button")].find((b) =>
        /^(Execute|Ausführen)$/.test(b.textContent?.trim() ?? ""),
      );
      expect(runButton).toBeTruthy();
    });
    await fireEvent.click(runButton!);
  }

  it("forwards check_conditions=false by default and shows a success toast", async () => {
    mockExecuteProgram.mockResolvedValue({ executed: true });
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstRunButton(container);

    await waitFor(() => expect(mockExecuteProgram).toHaveBeenCalledTimes(1));
    expect(mockExecuteProgram.mock.calls[0][2]).toBe(false);
    await waitFor(() => expect(toastStore.items.length).toBe(1));
    expect(toastStore.items[0].severity).toBe("success");
  });

  it("forwards check_conditions=true and shows a success toast when the CCU reports executed=true", async () => {
    setCheckboxChecked(true);
    mockExecuteProgram.mockResolvedValue({ executed: true });
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstRunButton(container);

    await waitFor(() => expect(mockExecuteProgram).toHaveBeenCalledTimes(1));
    expect(mockExecuteProgram.mock.calls[0][2]).toBe(true);
    await waitFor(() => expect(toastStore.items.length).toBe(1));
    expect(toastStore.items[0].severity).toBe("success");
  });

  it("shows an info toast (not an error) when the checked condition was not met", async () => {
    setCheckboxChecked(true);
    mockExecuteProgram.mockResolvedValue({ executed: false });
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstRunButton(container);

    await waitFor(() => expect(mockExecuteProgram).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(toastStore.items.length).toBe(1));
    expect(toastStore.items[0].severity).toBe("info");
  });

  it("cancelling the confirm dialog does not call the API", async () => {
    confirmStore.ask = vi.fn().mockResolvedValue(false);
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstRunButton(container);

    // Give any (incorrect) async execute() call a chance to fire before
    // asserting its absence.
    await new Promise((r) => setTimeout(r, 0));
    expect(mockExecuteProgram).not.toHaveBeenCalled();
  });
});

describe("ProgramList delete", () => {
  async function clickFirstDeleteButton(container: HTMLElement) {
    let btn: HTMLButtonElement | undefined;
    await waitFor(() => {
      btn = [...container.querySelectorAll("button")].find((b) =>
        /^(Remove|Entfernen)$/.test(b.textContent?.trim() ?? ""),
      );
      expect(btn).toBeTruthy();
    });
    await fireEvent.click(btn!);
  }

  it("deletes the program after confirmation, shows a success toast and reloads", async () => {
    mockDeleteProgram.mockResolvedValue(undefined);
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstDeleteButton(container);

    await waitFor(() => expect(mockDeleteProgram).toHaveBeenCalledTimes(1));
    // The table sorts by name ascending, so "Bare" (P-BARE) is the first row.
    expect(mockDeleteProgram.mock.calls[0][0]).toBe("P-BARE");
    await waitFor(() => expect(toastStore.items.length).toBe(1));
    expect(toastStore.items[0].severity).toBe("success");
    // A reload runs after a successful delete.
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(2));
  });

  it("does not call the API when the destructive confirm is cancelled", async () => {
    confirmStore.ask = vi.fn().mockResolvedValue(false);
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstDeleteButton(container);

    await new Promise((r) => setTimeout(r, 0));
    expect(mockDeleteProgram).not.toHaveBeenCalled();
  });

  it("surfaces an error toast when the delete request fails", async () => {
    mockDeleteProgram.mockRejectedValue(new Error("boom"));
    const { container } = render(ProgramList);
    await waitFor(() => expect(mockListPrograms).toHaveBeenCalledTimes(1));

    await clickFirstDeleteButton(container);

    await waitFor(() => expect(toastStore.items.length).toBe(1));
    expect(toastStore.items[0].severity).toBe("error");
  });
});
