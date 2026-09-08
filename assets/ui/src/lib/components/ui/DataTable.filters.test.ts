// @vitest-environment happy-dom
import type { ComponentProps } from "svelte";
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${Object.values(vars).join(",")}` : key,
}));

vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn().mockReturnValue(null),
}));

import DataTable from "./DataTable.svelte";
import type { DataColumn } from "./data-table";

type Row = { id: string; name: string; iface: string; score: number };

const rows: Row[] = [
  { id: "1", name: "Alpha", iface: "HmIP-RF", score: 30 },
  { id: "2", name: "Beta", iface: "BidCos-RF", score: 10 },
  { id: "3", name: "Gamma", iface: "HmIP-RF", score: 20 },
];

const columns: DataColumn<Row>[] = [
  { key: "name", label: "Name", get: (r) => r.name },
  {
    key: "iface",
    label: "Interface",
    get: (r) => r.iface,
    filter: "select",
    filterOptions: [
      { value: "HmIP-RF", label: "HmIP-RF" },
      { value: "BidCos-RF", label: "BidCos-RF" },
    ],
  },
  // No `get`: the table cannot read it, so it must not offer a filter.
  { key: "actions", label: "Actions" },
];

function props(over: Record<string, unknown> = {}) {
  return {
    rows,
    columns,
    rowKey: (r: Row) => r.id,
    emptyMessage: "Nothing here",
    columnFilters: true,
    ...over,
  } as unknown as ComponentProps<typeof DataTable>;
}

function bodyRowText(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll("tbody tr")).map(
    (r) => r.textContent?.trim() ?? "",
  );
}

beforeEach(() => localStorage.clear());
afterEach(() => cleanup());

describe("DataTable — column filters", () => {
  it("renders no filter row unless columnFilters is on", () => {
    const { container } = render(DataTable, { props: props({ columnFilters: false }) });
    expect(container.querySelectorAll("thead tr").length).toBe(1);
  });

  // A column the table cannot read has nothing to match against, so offering
  // a control for it would be a filter that silently does nothing.
  it("offers a control only for columns it can read", () => {
    const { container } = render(DataTable, { props: props() });
    const filterRow = container.querySelectorAll("thead tr")[1];
    expect(filterRow.querySelectorAll("input, select").length).toBe(2);
  });

  it("narrows the rows by a text filter", async () => {
    const { container, getByLabelText } = render(DataTable, { props: props() });
    await fireEvent.input(getByLabelText("datatable.filter_by:Name"), {
      target: { value: "am" },
    });
    expect(bodyRowText(container)).toHaveLength(1);
    expect(bodyRowText(container)[0]).toContain("Gamma");
  });

  it("narrows the rows by a select filter, matching exactly", async () => {
    const { container, getByLabelText } = render(DataTable, { props: props() });
    await fireEvent.change(getByLabelText("datatable.filter_by:Interface"), {
      target: { value: "BidCos-RF" },
    });
    expect(bodyRowText(container)).toHaveLength(1);
    expect(bodyRowText(container)[0]).toContain("Beta");
  });

  it("combines two filters with AND", async () => {
    const { container, getByLabelText } = render(DataTable, { props: props() });
    await fireEvent.change(getByLabelText("datatable.filter_by:Interface"), {
      target: { value: "HmIP-RF" },
    });
    await fireEvent.input(getByLabelText("datatable.filter_by:Name"), {
      target: { value: "Alpha" },
    });
    expect(bodyRowText(container)).toHaveLength(1);
    expect(bodyRowText(container)[0]).toContain("Alpha");
  });

  it("restores every row when a filter is cleared", async () => {
    const { container, getByLabelText } = render(DataTable, { props: props() });
    const input = getByLabelText("datatable.filter_by:Name");
    await fireEvent.input(input, { target: { value: "Alpha" } });
    expect(bodyRowText(container)).toHaveLength(1);
    await fireEvent.input(input, { target: { value: "" } });
    expect(bodyRowText(container)).toHaveLength(3);
  });

  // A filter that vanishes on reload is worse than none: the table then shows
  // a subset with nothing on screen saying why.
  it("persists filters under persistKey and restores them", async () => {
    const { getByLabelText, unmount } = render(
      DataTable,
      props({ persistKey: "filters-test" }),
    );
    await fireEvent.input(getByLabelText("datatable.filter_by:Name"), {
      target: { value: "Beta" },
    });
    unmount();

    const second = render(DataTable, { props: props({ persistKey: "filters-test" }) });
    expect(bodyRowText(second.container)).toHaveLength(1);
    expect(bodyRowText(second.container)[0]).toContain("Beta");
  });

  // Sort and search predate filters; a stored value written before this
  // feature has no `filters` key and must not crash the restore.
  it("tolerates a persisted payload written before filters existed", () => {
    localStorage.setItem(
      "datatable:legacy",
      JSON.stringify({ sortKey: "name", sortAsc: true, query: "" }),
    );
    const { container } = render(DataTable, { props: props({ persistKey: "legacy" }) });
    expect(bodyRowText(container)).toHaveLength(3);
  });
});
