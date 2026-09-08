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

import DataTable from "./DataTable.svelte";
import DataTableExpandHost from "./DataTableExpandHost.svelte";
import type { DataColumn } from "./data-table";

type Row = { id: string; name: string; channels: number };

const rows: Row[] = [
  { id: "1", name: "Alpha", channels: 2 },
  { id: "2", name: "Beta", channels: 0 },
];

const columns: DataColumn<Row>[] = [
  { key: "name", label: "Name", get: (r) => r.name },
  { key: "channels", label: "Channels", get: (r) => r.channels },
];

function hostProps(over: Record<string, unknown> = {}) {
  return { rows, columns, ...over } as unknown as ComponentProps<
    typeof DataTableExpandHost
  >;
}

function plainProps() {
  return {
    rows,
    columns,
    rowKey: (r: Row) => r.id,
    emptyMessage: "Nothing here",
  } as unknown as ComponentProps<typeof DataTable>;
}

afterEach(() => cleanup());

describe("DataTable — row expansion", () => {
  // Without an `expand` snippet the chevron column must not appear at all:
  // an empty leading column on every existing table would shift its layout.
  it("adds no chevron column when no expand snippet is given", () => {
    const { container } = render(DataTable, { props: plainProps() });
    const headerCells = container.querySelectorAll("thead th");
    expect(headerCells.length).toBe(2);
  });

  it("renders a chevron column and no expansion until one is opened", () => {
    const { container } = render(DataTableExpandHost, { props: hostProps() });
    expect(container.querySelectorAll("thead th").length).toBe(3);
    expect(container.querySelectorAll("tbody tr").length).toBe(2);
    expect(container.textContent).not.toContain("expanded-content-1");
  });

  it("reveals the snippet's content for the row that was expanded", async () => {
    const { container, getAllByRole } = render(DataTableExpandHost, {
      props: hostProps(),
    });
    const toggles = getAllByRole("button", { name: "datatable.expand" });
    await fireEvent.click(toggles[0]);
    expect(container.textContent).toContain("expanded-content-1");
    expect(container.textContent).not.toContain("expanded-content-2");
  });

  it("collapses again on a second click", async () => {
    const { container, getAllByRole, getByRole } = render(DataTableExpandHost, {
      props: hostProps(),
    });
    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    expect(container.textContent).toContain("expanded-content-1");
    await fireEvent.click(getByRole("button", { name: "datatable.collapse" }));
    expect(container.textContent).not.toContain("expanded-content-1");
  });

  it("keeps two rows open independently", async () => {
    const { container, getAllByRole } = render(DataTableExpandHost, {
      props: hostProps(),
    });
    let toggles = getAllByRole("button", { name: "datatable.expand" });
    await fireEvent.click(toggles[0]);
    toggles = getAllByRole("button", { name: "datatable.expand" });
    await fireEvent.click(toggles[0]);
    expect(container.textContent).toContain("expanded-content-1");
    expect(container.textContent).toContain("expanded-content-2");
  });

  // A row that cannot expand keeps its (empty) chevron cell, so the data
  // columns still line up across the table.
  it("gives a non-expandable row an empty chevron cell rather than none", () => {
    const { container } = render(DataTableExpandHost, {
      props: hostProps({ expandable: (r: Row) => r.channels > 0 }),
    });
    const bodyRows = container.querySelectorAll("tbody tr");
    expect(bodyRows[0].querySelectorAll("td").length).toBe(3);
    expect(bodyRows[1].querySelectorAll("td").length).toBe(3);
    expect(bodyRows[1].querySelectorAll("button").length).toBe(0);
  });

  it("spans the expansion across every column including the chevron", async () => {
    const { container, getAllByRole } = render(DataTableExpandHost, {
      props: hostProps(),
    });
    await fireEvent.click(getAllByRole("button", { name: "datatable.expand" })[0]);
    const expansionCell = container.querySelector("tbody tr td[colspan]");
    expect(expansionCell?.getAttribute("colspan")).toBe("3");
  });
});
