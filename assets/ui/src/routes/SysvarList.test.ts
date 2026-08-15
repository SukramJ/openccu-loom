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
    getSysvarUsage: vi.fn(),
    // The channel-assignment picker pulls the shared device store (and,
    // per picked device, the channel list); an empty catalogue keeps the
    // dialogs rendering without a daemon.
    listDevices: vi.fn().mockResolvedValue({ items: [], total: 0 }),
    listChannels: vi.fn().mockResolvedValue([]),
  },
  // Module-load hook of the auth store, which the device store imports.
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

vi.mock("$lib/stores/confirm.svelte", () => ({
  confirmStore: { ask: vi.fn().mockResolvedValue(false) },
}));

// Capture the handler the view registers on the WS pump so a test can
// deliver a `sysvar` envelope without a socket.
const eventHandlers: Array<(ev: unknown) => void> = [];
vi.mock("$lib/stores/events.svelte", () => ({
  onResync: () => () => {},
  subscribe: (handler: (ev: unknown) => void) => {
    eventHandlers.push(handler);
    return () => {
      const idx = eventHandlers.indexOf(handler);
      if (idx >= 0) eventHandlers.splice(idx, 1);
    };
  },
}));

import { toastStore } from "$lib/stores/toast.svelte";
import { ApiError, api } from "$lib/api/client";
import { confirmStore } from "$lib/stores/confirm.svelte";
import { t } from "$lib/i18n";
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
  vi.mocked(confirmStore.ask).mockResolvedValue(false);
  vi.mocked(api.getSysvarUsage).mockResolvedValue({ sysvar: "", programs: [] });
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

describe("SysvarList live values", () => {
  // The daemon already pushes every system-variable change; without a
  // consumer the table shows the values of the last REST read until the
  // operator hits reload.
  it("applies a sysvar broadcast to the matching row", async () => {
    mockListSysvars.mockResolvedValue([
      {
        name: "S_Temperatur",
        central: "ccu1",
        value_type: "FLOAT",
        value: 21.5,
        value_list: [],
        unit: "°C",
      },
    ]);
    const { container } = render(SysvarList);
    await waitFor(() =>
      expect(
        container.querySelector<HTMLInputElement>('input[type="number"]')?.value,
      ).toBe("21.5"),
    );

    for (const handler of [...eventHandlers]) {
      handler({
        type: "sysvar",
        payload: { central: "ccu1", name: "S_Temperatur", value: 23.5 },
      });
    }

    await waitFor(() =>
      expect(
        container.querySelector<HTMLInputElement>('input[type="number"]')?.value,
      ).toBe("23.5"),
    );
  });

  it("ignores a broadcast for the same name on another central", async () => {
    mockListSysvars.mockResolvedValue([
      {
        name: "S_Temperatur",
        central: "ccu1",
        value_type: "FLOAT",
        value: 21.5,
        value_list: [],
        unit: "°C",
      },
    ]);
    const { container } = render(SysvarList);
    await waitFor(() =>
      expect(
        container.querySelector<HTMLInputElement>('input[type="number"]')?.value,
      ).toBe("21.5"),
    );

    for (const handler of [...eventHandlers]) {
      handler({
        type: "sysvar",
        payload: { central: "ccu2", name: "S_Temperatur", value: 23.5 },
      });
    }
    await new Promise((r) => setTimeout(r, 0));

    expect(
      container.querySelector<HTMLInputElement>('input[type="number"]')?.value,
    ).toBe("21.5");
  });
});

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

  it("renders the current-state value label next to a LOGIC switch", async () => {
    const c = await renderWith({
      name: "S_Door",
      central: "",
      value_type: "LOGIC",
      value: true,
      value_list: [],
      unit: "",
      value_name_0: "zugesperrt",
      value_name_1: "geoeffnet",
    });
    expect(c.querySelector('[role="switch"]')).not.toBeNull();
    // value === true → the true-state label is shown; the false-state
    // label is not.
    expect(c.textContent).toContain("geoeffnet");
    expect(c.textContent).not.toContain("zugesperrt");
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

// findByLabel locates the <input> whose enclosing <label> carries the
// given (localized) label span text. Both the create card and the edit
// dialog wrap each Input in a `<label><span>…</span><Input/></label>`.
function findByLabel(scope: HTMLElement, labelText: string): HTMLInputElement {
  const label = [...scope.querySelectorAll("label")].find(
    (l) => l.querySelector("span")?.textContent?.trim() === labelText,
  );
  const input = label?.querySelector("input");
  if (!input) throw new Error(`input for label "${labelText}" not found`);
  return input as HTMLInputElement;
}

// switchByLabel locates a role="switch" control by the text of the <span>
// sharing its wrapping <label> — the is_visible/is_logged flags render as
// a Switch, not an <input>, so findByLabel cannot see them.
function switchByLabel(scope: HTMLElement, labelText: string): HTMLElement {
  const label = [...scope.querySelectorAll("label")].find(
    (l) => l.querySelector("span")?.textContent?.trim() === labelText,
  );
  const sw = label?.querySelector('[role="switch"]');
  if (!sw) throw new Error(`switch for label "${labelText}" not found`);
  return sw as HTMLElement;
}

function buttonByText(scope: HTMLElement, re: RegExp): HTMLButtonElement {
  const btn = [...scope.querySelectorAll("button")].find((b) =>
    re.test(b.textContent ?? ""),
  );
  if (!btn) throw new Error(`button matching ${re} not found`);
  return btn as HTMLButtonElement;
}

// findSelectByLabel mirrors findByLabel but for the <select> controls
// (e.g. the create-form's value-type picker), which findByLabel cannot
// locate since it only looks for <input>.
function findSelectByLabel(scope: HTMLElement, labelText: string): HTMLSelectElement {
  const label = [...scope.querySelectorAll("label")].find(
    (l) => l.querySelector("span")?.textContent?.trim() === labelText,
  );
  const select = label?.querySelector("select");
  if (!select) throw new Error(`select for label "${labelText}" not found`);
  return select as HTMLSelectElement;
}

describe("SysvarList edit dialog dispatch", () => {
  const listVar = {
    name: "Mode",
    central: "ccu1",
    value_type: "LIST",
    value: 1,
    value_list: ["Off", "Home", "Away"],
    unit: "",
    is_internal: false,
    is_extended: false,
  };

  async function openEditDialog(sv: Record<string, unknown>): Promise<{
    container: HTMLElement;
    dialog: HTMLElement;
  }> {
    mockListSysvars.mockResolvedValue([sv]);
    const { container } = render(SysvarList);
    await waitFor(() => expect(container.querySelector("tbody tr")).not.toBeNull());
    // The gear button (⚙) opens the metadata edit dialog for the row.
    await fireEvent.click(buttonByText(container, /^⚙$/));
    await waitFor(() =>
      expect(container.querySelector('[role="dialog"]')).not.toBeNull(),
    );
    return { container, dialog: container.querySelector('[role="dialog"]') as HTMLElement };
  }

  it("renders the value-list field for a LIST sysvar (wire type, not ENUM)", async () => {
    const { dialog } = await openEditDialog(listVar);
    // The bug: the dialog gated the value-list field on value_type ===
    // "ENUM", but the daemon delivers "LIST" — so a real CCU list
    // variable never got the field. It must show, pre-filled.
    const valuesInput = findByLabel(dialog, t("sysvars.create.values"));
    expect(valuesInput.value).toBe("Off;Home;Away");
  });

  it("patches the value_list of a LIST sysvar on save", async () => {
    const { dialog } = await openEditDialog(listVar);
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    expect(api.patchSysvar).toHaveBeenCalledWith(
      "Mode",
      expect.objectContaining({ value_list: ["Off", "Home", "Away"] }),
      "ccu1",
    );
  });

  it("sends the new name in the patch body when the operator renames", async () => {
    const { dialog } = await openEditDialog(listVar);
    const nameInput = findByLabel(dialog, t("sysvars.edit.name"));
    nameInput.value = "RenamedMode";
    await fireEvent.input(nameInput);
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    expect(api.patchSysvar).toHaveBeenCalledWith(
      "Mode",
      expect.objectContaining({ name: "RenamedMode" }),
      "ccu1",
    );
  });

  it("omits the name field when the name is unchanged", async () => {
    const { dialog } = await openEditDialog(listVar);
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    const body = vi.mocked(api.patchSysvar).mock.calls[0][1] as Record<string, unknown>;
    expect(body.name).toBeUndefined();
  });

  const logicVar = {
    name: "Door",
    central: "ccu1",
    value_type: "LOGIC",
    value: false,
    value_list: [],
    unit: "",
    is_internal: false,
    is_extended: false,
    is_visible: true,
    is_logged: false,
    value_name_0: "zu",
    value_name_1: "offen",
  };

  it("renders the value-label fields pre-filled for a LOGIC sysvar", async () => {
    const { dialog } = await openEditDialog(logicVar);
    expect(findByLabel(dialog, t("sysvars.labels.value0")).value).toBe("zu");
    expect(findByLabel(dialog, t("sysvars.labels.value1")).value).toBe("offen");
  });

  it("sends a changed value label in the patch body", async () => {
    const { dialog } = await openEditDialog(logicVar);
    const vn1 = findByLabel(dialog, t("sysvars.labels.value1"));
    vn1.value = "aufgesperrt";
    await fireEvent.input(vn1);
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    expect(api.patchSysvar).toHaveBeenCalledWith(
      "Door",
      expect.objectContaining({ value_name_1: "aufgesperrt" }),
      "ccu1",
    );
  });

  it("does not resend unchanged labels or flags on a plain save", async () => {
    const { dialog } = await openEditDialog(logicVar);
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    const body = vi.mocked(api.patchSysvar).mock.calls[0][1] as Record<string, unknown>;
    expect(body.value_name_0).toBeUndefined();
    expect(body.value_name_1).toBeUndefined();
    expect(body.is_visible).toBeUndefined();
    expect(body.is_logged).toBeUndefined();
  });

  // Toggling the visibility / archive switches must reach the PATCH body
  // as the new boolean — the counterpart of the "unchanged flags stay
  // out of the body" case above, exercising the actual send path.
  it("sends a toggled visibility/archive flag in the patch body", async () => {
    const { dialog } = await openEditDialog(logicVar);
    await fireEvent.click(switchByLabel(dialog, t("sysvars.flags.visible")));
    await fireEvent.click(switchByLabel(dialog, t("sysvars.flags.logged")));
    await fireEvent.click(buttonByText(dialog, /save|speichern/i));
    await waitFor(() => expect(api.patchSysvar).toHaveBeenCalledTimes(1));
    // logicVar starts is_visible: true, is_logged: false — toggling both
    // flips them.
    expect(api.patchSysvar).toHaveBeenCalledWith(
      "Door",
      expect.objectContaining({ is_visible: false, is_logged: true }),
      "ccu1",
    );
  });
});

describe("SysvarList create dialog dispatch", () => {
  it("includes the description in the create body", async () => {
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    // Toggle the create card open.
    await fireEvent.click(buttonByText(container, /\+\s*(new|neu)/i));
    await waitFor(() =>
      expect(
        [...container.querySelectorAll("label")].some(
          (l) => l.querySelector("span")?.textContent?.trim() === t("sysvars.create.name"),
        ),
      ).toBe(true),
    );
    const nameInput = findByLabel(container, t("sysvars.create.name"));
    nameInput.value = "NewVar";
    await fireEvent.input(nameInput);
    const descInput = findByLabel(container, t("sysvars.edit.description"));
    descInput.value = "a helpful note";
    await fireEvent.input(descInput);
    await fireEvent.click(buttonByText(container, /^(add|hinzufügen)$/i));
    await waitFor(() => expect(api.createSysvar).toHaveBeenCalledTimes(1));
    expect(api.createSysvar).toHaveBeenCalledWith(
      expect.objectContaining({ name: "NewVar", description: "a helpful note" }),
      "",
    );
  });

  // The create-type picker must offer ALARM alongside the other create
  // codes, and the hint text must stay hidden until ALARM is the active
  // selection (the default form value_type is BOOL, so the hint must not
  // leak into the form before the operator has chosen ALARM).
  //
  // Driving the native <select> through a simulated "change" event and
  // asserting the reactive follow-on (the conditional hint paragraph) is
  // not exercised here: happy-dom's `:checked` selector match — which
  // Svelte's select binding relies on to read back the chosen option —
  // only supports <input>, not <option>, so the binding never observes
  // the simulated selection in this test environment.
  it("offers ALARM in the create-type select and hides the alarm hint by default", async () => {
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    await fireEvent.click(buttonByText(container, /\+\s*(new|neu)/i));
    await waitFor(() =>
      expect(
        [...container.querySelectorAll("label")].some(
          (l) => l.querySelector("span")?.textContent?.trim() === t("sysvars.create.name"),
        ),
      ).toBe(true),
    );

    const typeSelect = findSelectByLabel(container, t("sysvars.create.type"));
    const opts = Array.from(typeSelect.options).map((o) => o.value);
    expect(opts).toEqual(["BOOL", "INTEGER", "FLOAT", "STRING", "ENUM", "ALARM"]);
    expect(container.textContent).not.toContain(t("sysvars.create.alarm_hint"));
  });

  // The default create type is BOOL, so the value-label fields must be
  // available and thread into the create body when the operator fills
  // them in.
  it("includes the binary value labels in the create body for a BOOL variable", async () => {
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    await fireEvent.click(buttonByText(container, /\+\s*(new|neu)/i));
    await waitFor(() =>
      expect(
        [...container.querySelectorAll("label")].some(
          (l) => l.querySelector("span")?.textContent?.trim() === t("sysvars.create.name"),
        ),
      ).toBe(true),
    );
    const nameInput = findByLabel(container, t("sysvars.create.name"));
    nameInput.value = "Door";
    await fireEvent.input(nameInput);
    const vn0 = findByLabel(container, t("sysvars.labels.value0"));
    vn0.value = "zu";
    await fireEvent.input(vn0);
    const vn1 = findByLabel(container, t("sysvars.labels.value1"));
    vn1.value = "offen";
    await fireEvent.input(vn1);
    await fireEvent.click(buttonByText(container, /^(add|hinzufügen)$/i));
    await waitFor(() => expect(api.createSysvar).toHaveBeenCalledTimes(1));
    expect(api.createSysvar).toHaveBeenCalledWith(
      expect.objectContaining({ name: "Door", value_name_0: "zu", value_name_1: "offen" }),
      "",
    );
  });

  it("warns about referencing programs before deleting (SV07)", async () => {
    vi.mocked(api.getSysvarUsage).mockResolvedValue({
      sysvar: alarmStatus.name,
      programs: [{ id: "P1", name: "Morning Routine", is_internal: false }],
    });
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    const delBtn = () =>
      container.querySelector('button[title="' + t("sysvars.remove.tooltip") + '"]') as HTMLButtonElement | null;
    await waitFor(() => expect(delBtn()).not.toBeNull());
    await fireEvent.click(delBtn()!);

    await waitFor(() => expect(confirmStore.ask).toHaveBeenCalledTimes(1));
    const arg = vi.mocked(confirmStore.ask).mock.calls[0][0] as { body?: string };
    expect(arg.body).toContain("Morning Routine");
  });

  it("still confirms deletion when the usage lookup fails (non-blocking)", async () => {
    vi.mocked(api.getSysvarUsage).mockRejectedValue(new ApiError(500, null, "boom"));
    const { container } = render(SysvarList);
    await waitFor(() => expect(mockListSysvars).toHaveBeenCalledTimes(1));
    const delBtn = () =>
      container.querySelector('button[title="' + t("sysvars.remove.tooltip") + '"]') as HTMLButtonElement | null;
    await waitFor(() => expect(delBtn()).not.toBeNull());
    await fireEvent.click(delBtn()!);

    // The confirm dialog must still appear — a usage-lookup failure must not
    // block deletion. ask() resolves false here, so no delete follows.
    await waitFor(() => expect(confirmStore.ask).toHaveBeenCalledTimes(1));
  });
});
