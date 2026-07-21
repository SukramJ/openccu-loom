// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

// The reload path must force a CCU re-pull (POST /sysvars/fetch) BEFORE
// re-reading the list: GET /sysvars only serves the daemon's
// periodic-poll state, so without the fetch a value just changed at the
// CCU stays invisible for up to one sysvar-scan interval.
const mockListSysvars = vi.fn();
const mockFetchSysvars = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: {
    listSysvars: (...args: unknown[]) => mockListSysvars(...args),
    fetchSysvars: (...args: unknown[]) => mockFetchSysvars(...args),
    listCentrals: vi.fn().mockResolvedValue([]),
    setSysvar: vi.fn(),
    patchSysvar: vi.fn(),
    createSysvar: vi.fn(),
    deleteSysvar: vi.fn(),
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

import { toastStore } from "$lib/stores/toast.svelte";
import { ApiError } from "$lib/api/client";
import SysvarList from "./SysvarList.svelte";

const alarmStatus = {
  name: "S_Alarm_System_Status",
  central: "",
  value_type: "ENUM",
  value: 0,
  value_list: ["Aus", "Aktivierung", "Hüllschutz", "Vollschutz"],
  unit: "",
  is_internal: false,
  is_extended: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockListSysvars.mockResolvedValue([alarmStatus]);
  mockFetchSysvars.mockResolvedValue(undefined);
});

afterEach(() => cleanup());

// findReloadButton locates the header reload control by its localized
// label (the catalogue resolves common.reload for the active locale).
function findReloadButton(container: HTMLElement): HTMLButtonElement {
  const buttons = [...container.querySelectorAll("button")];
  const btn = buttons.find((b) => /reload|neu laden/i.test(b.textContent ?? ""));
  if (!btn) throw new Error("reload button not found");
  return btn as HTMLButtonElement;
}

describe("SysvarList value widget", () => {
  // The daemon passes the CCU wire type straight through, so LOGIC and
  // ALARM (the most common boolean sysvars) must render as a switch —
  // the same control the BOOL alias gets — not the free-text fallback.
  async function renderWith(sv: Record<string, unknown>): Promise<HTMLElement> {
    mockListSysvars.mockResolvedValue([sv]);
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(container.querySelector("tbody tr")).not.toBeNull(),
    );
    return container;
  }

  it("renders a switch for a LOGIC sysvar", async () => {
    const c = await renderWith({
      name: "S_Light",
      central: "",
      value_type: "LOGIC",
      value: false,
      value_list: [],
      unit: "",
    });
    expect(c.querySelector('[role="switch"]')).not.toBeNull();
    expect(c.querySelector('input[type="text"]')).toBeNull();
  });

  it("renders a switch for an ALARM sysvar even with a label list", async () => {
    const c = await renderWith({
      name: "S_Alarm",
      central: "",
      value_type: "ALARM",
      value: true,
      value_list: ["nicht ausgelöst", "ausgelöst"],
      unit: "",
    });
    expect(c.querySelector('[role="switch"]')).not.toBeNull();
    expect(c.querySelector('[aria-haspopup="listbox"]')).toBeNull();
  });

  it("renders a dropdown for a labelled LIST sysvar", async () => {
    const c = await renderWith({
      name: "S_Alarm_System_Status",
      central: "",
      value_type: "LIST",
      value: 0,
      value_list: ["Aus", "Aktivierung", "Vollschutz"],
      unit: "",
    });
    expect(c.querySelector('[aria-haspopup="listbox"]')).not.toBeNull();
    expect(c.querySelector('[role="switch"]')).toBeNull();
  });

  it("renders a number input for an INTEGER sysvar with no value_list", async () => {
    const c = await renderWith({
      name: "S_Counter",
      central: "",
      value_type: "INTEGER",
      value: 3,
      value_list: [],
      unit: "",
    });
    expect(c.querySelector('input[type="number"]')).not.toBeNull();
    expect(c.querySelector('[role="switch"]')).toBeNull();
    expect(c.querySelector('[aria-haspopup="listbox"]')).toBeNull();
  });

  it("renders a text input for a STRING sysvar", async () => {
    const c = await renderWith({
      name: "S_Note",
      central: "",
      value_type: "STRING",
      value: "hello",
      value_list: [],
      unit: "",
    });
    expect(c.querySelector('input[type="text"]')).not.toBeNull();
    expect(c.querySelector('input[type="number"]')).toBeNull();
    expect(c.querySelector('[role="switch"]')).toBeNull();
  });

  it("prefers the dropdown over a number input for an INTEGER sysvar that still ships a label list", async () => {
    const c = await renderWith({
      name: "S_Mode",
      central: "",
      value_type: "INTEGER",
      value: 1,
      value_list: ["Off", "On"],
      unit: "",
    });
    expect(c.querySelector('[aria-haspopup="listbox"]')).not.toBeNull();
    expect(c.querySelector('input[type="number"]')).toBeNull();
  });
});

describe("SysvarList reload", () => {
  it("forces a CCU re-pull before re-reading the list", async () => {
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    // The button carries disabled={loading}; wait for the initial load
    // to fully settle or the click lands on a disabled control.
    await waitFor(() => expect(findReloadButton(container).disabled).toBe(false));

    await fireEvent.click(findReloadButton(container));

    await waitFor(() => {
      expect(mockFetchSysvars).toHaveBeenCalledTimes(1);
      expect(mockListSysvars).toHaveBeenCalledTimes(2);
    });
    // The fetch must precede the list read, or the list still serves
    // the pre-refresh daemon state.
    const fetchOrder = mockFetchSysvars.mock.invocationCallOrder[0];
    const listOrder = mockListSysvars.mock.invocationCallOrder[1];
    expect(fetchOrder).toBeLessThan(listOrder);
  });

  it("still reads the current daemon state when the CCU re-pull fails", async () => {
    const errorSpy = vi.spyOn(toastStore, "error");
    mockFetchSysvars.mockRejectedValue(new ApiError(502, null, "ccu unreachable"));

    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(findReloadButton(container).disabled).toBe(false));

    await fireEvent.click(findReloadButton(container));

    await waitFor(() => {
      expect(mockListSysvars).toHaveBeenCalledTimes(2);
      expect(errorSpy).toHaveBeenCalledWith("502: ccu unreachable");
    });
  });
});
