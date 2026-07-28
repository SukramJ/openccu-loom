// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, fireEvent, within } from "@testing-library/svelte";
import type { AlarmJournalEntry } from "$lib/api/types";

vi.mock("$lib/stores/alarmPanel.svelte", () => ({
  alarmPanelStore: {
    get zonesConfig() {
      return [{ id: "zone-1", name: "Ground floor" }];
    },
    get journal() {
      return [] as AlarmJournalEntry[];
    },
  },
}));

const mockListAlarmJournal = vi.fn();
vi.mock("$lib/api/client", () => ({
  api: { listAlarmJournal: (...args: unknown[]) => mockListAlarmJournal(...args) },
  friendlyError: (err: unknown) => (err instanceof Error ? err.message : "error"),
}));

vi.mock("$lib/stores/preferences.svelte", () => ({
  prefs: { locale: "en" },
}));

vi.mock("$lib/i18n", () => ({
  t: (key: string) => key,
}));

import AlarmJournal from "./AlarmJournal.svelte";

function entry(overrides: Partial<AlarmJournalEntry> = {}): AlarmJournalEntry {
  return {
    id: 1,
    when: "2026-07-14T10:00:00Z",
    zone_id: "zone-1",
    class: "arm",
    event: "armed",
    actor: "admin",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

describe("AlarmJournal — table", () => {
  it("renders one row per journal entry", async () => {
    mockListAlarmJournal.mockResolvedValueOnce([
      entry({ id: 1, event: "armed" }),
      entry({ id: 2, event: "disarmed", class: "disarm" }),
    ]);
    const { findByRole, getAllByRole } = render(AlarmJournal);

    await findByRole("table");
    const rows = getAllByRole("row");
    // Header row + two data rows.
    expect(rows).toHaveLength(3);
    expect(within(rows[1]).getByText("armed")).toBeTruthy();
    expect(within(rows[2]).getByText("disarmed")).toBeTruthy();
  });
});

describe("AlarmJournal — filter narrows the result set", () => {
  it("re-fetches with a from-date filter and renders only the narrower response", async () => {
    mockListAlarmJournal.mockResolvedValueOnce([
      entry({ id: 1, event: "armed" }),
      entry({ id: 2, event: "disarmed", class: "disarm" }),
    ]);
    const { findByRole, getAllByRole, getByText } = render(AlarmJournal);
    await findByRole("table");
    expect(getAllByRole("row")).toHaveLength(3);

    // The second call (triggered by the from-date filter) returns a
    // narrower set — the "from" input is a plain <input type="datetime-local">,
    // not the bits-ui Select portal, so it is directly drivable in happy-dom.
    mockListAlarmJournal.mockResolvedValueOnce([entry({ id: 2, event: "disarmed", class: "disarm" })]);
    const fromInput = getByText("alarm.journal.filter.from").parentElement!.querySelector(
      "input",
    ) as HTMLInputElement;
    await fireEvent.input(fromInput, { target: { value: "2026-07-14T09:00" } });

    await findByRole("table");
    expect(mockListAlarmJournal).toHaveBeenCalledTimes(2);
    const lastCallArgs = mockListAlarmJournal.mock.calls.at(-1)?.[0];
    expect(lastCallArgs.from).toBeDefined();

    const rows = getAllByRole("row");
    expect(rows).toHaveLength(2);
    expect(within(rows[1]).getByText("disarmed")).toBeTruthy();
  });
});
