// Shared column descriptor for the DataTable design-system component.
// Kept in a plain .ts module so views can import the type (Svelte
// components cannot export generic types for use in markup).

export type DataTableAlign = "left" | "right" | "center";

export type DataColumn<Row> = {
  // Stable key — used for sort state, the per-cell `data-label`, and to
  // branch in the `cell` snippet.
  key: string;
  // Localized header label.
  label: string;
  // Cell + header alignment (default "left").
  align?: DataTableAlign;
  // When true the header is clickable to sort by this column.
  sortable?: boolean;
  // Scalar accessor used for sorting, the default text rendering, and the
  // search match. Return null/undefined for "no value".
  get?: (row: Row) => string | number | null | undefined;
  // Marks the primary column: on the mobile reflow it shows without its
  // label prefix and as the row's heading. At most one column should set it.
  title?: boolean;
  // Extra classes applied to this column's <td> cells.
  cellClass?: string;
  // Marks a column whose values are numbers. Cells and the header get
  // `tabular-nums` and right alignment (unless `align` overrides it), so a
  // column of channel numbers or counts lines up on its digits instead of
  // ragging against proportional glyph widths.
  numeric?: boolean;
  // Per-column filter control rendered in the filter row under the header,
  // when the table has `columnFilters` on. "text" is a substring match through
  // `get()`; "select" offers `filterOptions` and matches exactly; false (the
  // default for a column with no `get`) leaves the cell empty, because a
  // column the table cannot read cannot be filtered.
  filter?: "text" | "select" | false;
  // Options for a "select" filter. `value` is compared against `get()`'s
  // string form; `label` is what the operator picks.
  filterOptions?: { value: string; label: string }[];
  // Extra classes applied to this column's <th> header cell. Use together
  // with cellClass (e.g. the shared `hide-narrow` utility) to collapse a
  // whole column away on narrow viewports without leaving a dangling header.
  headClass?: string;
};

export type DataTableSort = { key: string; asc: boolean };
