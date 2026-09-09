// @vitest-environment happy-dom
import type { ComponentProps } from "svelte";
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";

vi.mock("$lib/i18n", () => ({
  t: (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${Object.values(vars).join(",")}` : key,
}));

vi.mock("$lib/components/ui/Icon.svelte", () => ({
  default: vi.fn().mockReturnValue(null),
}));

import DataTableGroupHost from "./DataTableGroupHost.svelte";
import type { DataColumn } from "./data-table";

type Row = { id: string; device: string; name: string };

// Row 1 is device "Beta" so, unsorted, the "Beta" section is encountered
// first — this is what lets the sort-order case assert a flip.
const rows: Row[] = [
  { id: "1", device: "Beta", name: "B1" },
  { id: "2", device: "Alpha", name: "A1" },
  { id: "3", device: "Beta", name: "B2" },
  { id: "4", device: "Alpha", name: "A2" },
];

const columns: DataColumn<Row>[] = [
  { key: "device", label: "Device", get: (r) => r.device, sortable: true },
  { key: "name", label: "Name", get: (r) => r.name },
];

function hostProps(over: Record<string, unknown> = {}) {
  return {
    rows,
    columns,
    rowKey: (r: Row) => r.id,
    groupBy: (r: Row) => r.device,
    ...over,
  } as unknown as ComponentProps<typeof DataTableGroupHost>;
}

afterEach(() => cleanup());

describe("DataTable — groupBy sections", () => {
  it("renders one tbody per distinct group key with a full-width header row", () => {
    const { container } = render(DataTableGroupHost, { props: hostProps() });
    const bodies = container.querySelectorAll("tbody");
    expect(bodies.length).toBe(2);
    for (const body of bodies) {
      const header = body.querySelector("tr th[scope='colgroup']");
      expect(header).not.toBeNull();
      // Two data columns, no expand snippet given, so the header spans 2.
      expect(header?.getAttribute("colspan")).toBe("2");
    }
  });

  it("puts every row under its own group key with none lost or duplicated", () => {
    const { container } = render(DataTableGroupHost, { props: hostProps() });
    const bodies = container.querySelectorAll("tbody");
    const betaBody = Array.from(bodies).find((b) => b.textContent?.includes("Beta"));
    const alphaBody = Array.from(bodies).find((b) => b.textContent?.includes("Alpha"));
    expect(betaBody?.querySelectorAll("tr").length).toBe(3); // header + 2 rows
    expect(alphaBody?.querySelectorAll("tr").length).toBe(3); // header + 2 rows
    expect(container.querySelectorAll("tbody tr td")).toHaveLength(8); // 4 rows * 2 cols
    expect(betaBody?.textContent).toContain("B1");
    expect(betaBody?.textContent).toContain("B2");
    expect(alphaBody?.textContent).toContain("A1");
    expect(alphaBody?.textContent).toContain("A2");
  });

  it("orders sections by sort, applied before grouping", async () => {
    const { container, getByRole } = render(DataTableGroupHost, { props: hostProps() });
    const headersBefore = Array.from(
      container.querySelectorAll("tbody tr th[scope='colgroup']"),
    ).map((h) => h.textContent?.trim());
    expect(headersBefore).toEqual(["Beta", "Alpha"]);

    await fireEvent.click(getByRole("button", { name: "Device" }));

    const headersAfter = Array.from(
      container.querySelectorAll("tbody tr th[scope='colgroup']"),
    ).map((h) => h.textContent?.trim());
    expect(headersAfter).toEqual(["Alpha", "Beta"]);
  });

  it("renders the groupHeader snippet when given", () => {
    const { container } = render(DataTableGroupHost, {
      props: hostProps({ withHeader: true }),
    });
    expect(container.textContent).toContain("section-Alpha-2");
    expect(container.textContent).toContain("section-Beta-2");
  });

  it("falls back to the raw key as text when no groupHeader is given", () => {
    const { container } = render(DataTableGroupHost, { props: hostProps() });
    const headerTexts = Array.from(
      container.querySelectorAll("tbody tr th[scope='colgroup']"),
    ).map((h) => h.textContent?.trim());
    expect(headerTexts).toEqual(["Beta", "Alpha"]);
    expect(container.textContent).not.toContain("section-");
  });
});
