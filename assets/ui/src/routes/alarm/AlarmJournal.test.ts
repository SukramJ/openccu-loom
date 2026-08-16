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

// Partial catalogue rather than an echo: the Event and Class cells resolve
// through t() and fall back to the raw token on a miss, so a stub that always
// echoes would make both branches look identical. "armed" and the maintenance
// class resolve here; "disarmed" deliberately does not, which exercises the
// fallback.
vi.mock("$lib/i18n", () => {
  const catalog: Record<string, string> = {
    "alarm.journal_event.armed": "Armed",
    "alarm.journal_class.maintenance": "Maintenance",
  };
  return { t: (key: string) => catalog[key] ?? key };
});

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
    expect(within(rows[1]).getByText("Armed")).toBeTruthy();
    expect(within(rows[2]).getByText("disarmed")).toBeTruthy();
  });

  it("localizes the event token and falls back to the raw token on a miss", async () => {
    mockListAlarmJournal.mockResolvedValueOnce([
      entry({ id: 1, event: "armed" }),
      entry({ id: 2, event: "pending_started", class: "trigger" }),
    ]);
    const { findByRole, getAllByRole } = render(AlarmJournal);

    await findByRole("table");
    const rows = getAllByRole("row");
    // Translated: the catalogue stub knows this token.
    expect(within(rows[1]).getByText("Armed")).toBeTruthy();
    expect(within(rows[1]).queryByText("armed")).toBeNull();
    // Not in the stub catalogue: the raw engine token, never the dotted key.
    expect(within(rows[2]).getByText("pending_started")).toBeTruthy();
    expect(
      within(rows[2]).queryByText("alarm.journal_event.pending_started"),
    ).toBeNull();
  });

  it("renders a maintenance-class entry with its own class badge", async () => {
    // Every arm files a motion-detector reset under the maintenance class,
    // so the badge has to carry a label. The catalogue side of this — that
    // the key exists in both locales — is pinned in i18n.enum-labels.test.ts,
    // because this suite stubs t().
    mockListAlarmJournal.mockResolvedValueOnce([
      entry({ id: 1, class: "maintenance", event: "motion_reset" }),
    ]);
    const { findByRole, getAllByRole } = render(AlarmJournal);

    await findByRole("table");
    const rows = getAllByRole("row");
    expect(within(rows[1]).getByText("Maintenance")).toBeTruthy();
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

  it("ignores a broad response that lands after a narrower filter was applied", async () => {
    // Filter changes race: a broad query started first can answer last, and
    // without a generation guard it repaints rows the filter controls no
    // longer describe.
    let resolveBroad!: (v: AlarmJournalEntry[]) => void;
    const broad = new Promise<AlarmJournalEntry[]>((res) => (resolveBroad = res));
    mockListAlarmJournal.mockReturnValueOnce(broad);

    const { getByText, findByRole, getAllByRole } = render(AlarmJournal);

    // Narrow the range while the initial broad query is still in flight.
    mockListAlarmJournal.mockResolvedValueOnce([
      entry({ id: 2, event: "disarmed", class: "disarm" }),
    ]);
    const fromInput = getByText("alarm.journal.filter.from").parentElement!.querySelector(
      "input",
    ) as HTMLInputElement;
    await fireEvent.input(fromInput, { target: { value: "2026-07-14T09:00" } });

    await findByRole("table");
    expect(getAllByRole("row")).toHaveLength(2);

    // The superseded broad query answers now — it must not repaint the table.
    resolveBroad([
      entry({ id: 1, event: "armed" }),
      entry({ id: 2, event: "disarmed", class: "disarm" }),
    ]);
    await new Promise((r) => setTimeout(r, 0));

    const rows = getAllByRole("row");
    expect(rows).toHaveLength(2);
    expect(within(rows[1]).getByText("disarmed")).toBeTruthy();
  });
});
