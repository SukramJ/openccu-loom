// @vitest-environment happy-dom
import type { ComponentProps } from "svelte";
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";

// i18n is mocked to echo keys so assertions stay locale-independent.
vi.mock("$lib/i18n", () => ({ t: (key: string) => key }));

// Icon renders Lucide SVGs; stub it out to keep the test DOM simple and
// avoid bundling SVG assets in the unit-test environment.
vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn().mockReturnValue(null),
}));

import DataTable from "./DataTable.svelte";
import type { DataColumn } from "./data-table";

// ---------------------------------------------------------------------------
// Shared fixtures
// ---------------------------------------------------------------------------

type Row = { id: string; name: string; score: number };

const columns: DataColumn<Row>[] = [
  { key: "name", label: "Name", sortable: true, get: (r) => r.name },
  { key: "score", label: "Score", sortable: true, get: (r) => r.score },
];

const rows: Row[] = [
  { id: "1", name: "Alpha", score: 30 },
  { id: "2", name: "Beta", score: 10 },
  { id: "3", name: "Gamma", score: 20 },
];

// DataTable is generic (`generics="Row"`); rendered directly via
// testing-library the generic resolves to `unknown`, so cast the concrete
// fixtures to the component's prop shape. The runtime values stay correct.
const baseProps = {
  rows,
  columns,
  rowKey: (r: Row) => r.id,
  emptyMessage: "Nothing here",
} as unknown as ComponentProps<typeof DataTable>;

afterEach(() => cleanup());

// ---------------------------------------------------------------------------
// 1. Basic rendering
// ---------------------------------------------------------------------------

describe("DataTable — basic rendering", () => {
  it("renders a table row per data row and column labels in the header", () => {
    const { container, getByText } = render(DataTable, { props: { ...baseProps } });

    // Column header labels are present.
    expect(getByText("Name")).toBeTruthy();
    expect(getByText("Score")).toBeTruthy();

    // One tbody <tr> per data row.
    const bodyRows = container.querySelectorAll("tbody tr");
    expect(bodyRows.length).toBe(rows.length);
  });

  it("renders cell text via col.get when no cell snippet is provided", () => {
    const { container } = render(DataTable, { props: { ...baseProps } });

    const nameCells = Array.from(container.querySelectorAll('td[data-label="Name"]'));
    const names = nameCells.map((td) => td.textContent?.trim());
    expect(names).toContain("Alpha");
    expect(names).toContain("Beta");
    expect(names).toContain("Gamma");
  });
});

// ---------------------------------------------------------------------------
// 2. Empty state
// ---------------------------------------------------------------------------

describe("DataTable — empty state", () => {
  it("shows emptyMessage and no data rows when rows is empty", () => {
    const { getByText, container } = render(DataTable, {
      props: { ...baseProps, rows: [] },
    });

    expect(getByText("Nothing here")).toBeTruthy();

    // No table rendered — the EmptyState branch skips the <table>.
    expect(container.querySelectorAll("tbody tr").length).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// 3. Sorting
// ---------------------------------------------------------------------------

describe("DataTable — sorting", () => {
  function getScores(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll('td[data-label="Score"]')).map(
      (td) => td.textContent?.trim() ?? "",
    );
  }

  it("sorts ascending on first header-button click, descending on second", async () => {
    const { container } = render(DataTable, { props: { ...baseProps } });

    // Locate the <button> inside the Score <th>.
    const headerButtons = Array.from(container.querySelectorAll("th button"));
    const scoreBtn = headerButtons.find((b) => b.textContent?.includes("Score"));
    expect(scoreBtn).not.toBeNull();

    // First click → ascending by score.
    await fireEvent.click(scoreBtn!);
    expect(getScores(container)).toEqual(["10", "20", "30"]);

    // Second click on the same column → descending.
    await fireEvent.click(scoreBtn!);
    expect(getScores(container)).toEqual(["30", "20", "10"]);
  });

  it("switches the active sort column when a different header is clicked", async () => {
    const { container } = render(DataTable, { props: { ...baseProps } });

    const headerButtons = Array.from(container.querySelectorAll("th button"));
    const nameBtn = headerButtons.find((b) => b.textContent?.includes("Name"));
    expect(nameBtn).not.toBeNull();

    await fireEvent.click(nameBtn!);

    const nameCells = Array.from(
      container.querySelectorAll('td[data-label="Name"]'),
    ).map((td) => td.textContent?.trim());
    // Alphabetical ascending: Alpha < Beta < Gamma.
    expect(nameCells).toEqual(["Alpha", "Beta", "Gamma"]);
  });
});

// ---------------------------------------------------------------------------
// 4. Search
// ---------------------------------------------------------------------------

describe("DataTable — search", () => {
  it("shows only matching rows after typing in the search field", async () => {
    const { container } = render(DataTable, {
      props: { ...baseProps, search: true, searchPlaceholder: "Filter…" },
    });

    const input = container.querySelector('input[type="search"]') as HTMLInputElement;
    expect(input).not.toBeNull();

    // Type "beta" — only the Beta row should remain.
    input.value = "Beta";
    await fireEvent.input(input);

    const nameCells = Array.from(container.querySelectorAll('td[data-label="Name"]'));
    expect(nameCells.length).toBe(1);
    expect(nameCells[0].textContent?.trim()).toBe("Beta");
  });

  it("shows emptyMessage when search matches no rows", async () => {
    const { container, getByText } = render(DataTable, {
      props: { ...baseProps, search: true, emptyMessage: "No results" },
    });

    const input = container.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = "zzz_no_match";
    await fireEvent.input(input);

    expect(getByText("No results")).toBeTruthy();
    expect(container.querySelectorAll("tbody tr").length).toBe(0);
  });

  it("restores all rows when the search field is cleared", async () => {
    const { container } = render(DataTable, {
      props: { ...baseProps, search: true },
    });

    const input = container.querySelector('input[type="search"]') as HTMLInputElement;

    // Filter down, then clear.
    input.value = "Alpha";
    await fireEvent.input(input);
    expect(container.querySelectorAll("tbody tr").length).toBe(1);

    input.value = "";
    await fireEvent.input(input);
    expect(container.querySelectorAll("tbody tr").length).toBe(rows.length);
  });
});
