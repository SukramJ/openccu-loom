<script lang="ts" generics="Row">
  import type { Snippet } from "svelte";
  import type { IconName } from "$lib/icons";
  import type { DataColumn, DataTableSort } from "./data-table";
  import { makeTextMatcher } from "$lib/utils";
  import { t } from "$lib/i18n";
  import Input from "./Input.svelte";
  import EmptyState from "./EmptyState.svelte";

  // DataTable — the shared, sortable, optionally searchable table used across
  // the SPA so every tabular view shares one interaction + visual language.
  // Sorting is by clicking the column header (asc ↔ desc, keyboard-operable);
  // the responsive `table-reflow` layout collapses to cards on phones. Cell
  // content is supplied by the `cell` snippet (parent), falling back to the
  // column's `get()` text when no snippet is given.
  let {
    rows,
    columns,
    rowKey,
    search = false,
    searchPlaceholder = "",
    initialSort = null,
    persistKey = "",
    emptyMessage,
    emptyDescription = "",
    emptyIcon = "mdi:format-list-bulleted",
    cell,
    rowClass,
    onRowClick,
    selectedKey = null,
    columnFilters = false,
    expand,
    expandable,
    onExpand,
  }: {
    rows: Row[];
    columns: DataColumn<Row>[];
    rowKey: (row: Row) => string;
    search?: boolean;
    searchPlaceholder?: string;
    initialSort?: DataTableSort | null;
    // When set, sort + search persist under this localStorage key.
    persistKey?: string;
    emptyMessage: string;
    // Optional muted second line under the empty message (see EmptyState).
    emptyDescription?: string;
    emptyIcon?: IconName;
    cell?: Snippet<[Row, DataColumn<Row>]>;
    rowClass?: (row: Row) => string;
    // When set, every row becomes activatable: clicking it (or pressing
    // Enter/Space on it) calls this. The row keeps its cells' own interactive
    // elements working — a click that originated inside a button, link or
    // form control is left to that control and never reaches the row.
    onRowClick?: (row: Row) => void;
    // `rowKey` of the row that is currently selected, or null for none. Marks
    // it `aria-selected` and tints it, which is what turns a table into a
    // selector for an editor rendered beside or below it.
    selectedKey?: string | null;
    // Renders a filter row under the header: one control per column that
    // declares a `filter` (or has a `get` and does not opt out). Filters
    // combine with AND, and with the search box when both are on.
    columnFilters?: boolean;
    // Renders under a row when the operator expands it, spanning every
    // column. Without this snippet no chevron column appears at all.
    expand?: Snippet<[Row]>;
    // Which rows can expand. Defaults to all of them when `expand` is given;
    // a row that answers false keeps its chevron cell empty so the columns
    // still line up.
    expandable?: (row: Row) => boolean;
    // Called when a row is opened (not when it closes). This is what makes a
    // lazily loaded expansion possible: the snippet renders only while the
    // row is open, so it has no moment of its own to start a fetch from.
    onExpand?: (row: Row) => void;
  } = $props();

  type Persisted = {
    sortKey: string;
    sortAsc: boolean;
    query: string;
    // Column key → the operator's filter text. Persisted beside sort and
    // search: a filter that vanishes on reload is worse than none, because
    // the table then shows a subset with nothing on screen saying why.
    filters: Record<string, string>;
  };

  // Compute the initial sort/search inside a function so the `initialSort`
  // prop is read once (non-reactively) rather than captured in a $state
  // initializer; persisted state (when persistKey is set) wins.
  function initialState(): Persisted {
    let base: Persisted = {
      sortKey: initialSort?.key ?? "",
      sortAsc: initialSort?.asc ?? true,
      query: "",
      filters: {},
    };
    if (persistKey) {
      try {
        const raw = localStorage.getItem("datatable:" + persistKey);
        if (raw) {
          const parsed = JSON.parse(raw) as Partial<Persisted>;
          base = { ...base, ...parsed, filters: parsed.filters ?? {} };
        }
      } catch {
        // storage unavailable — fall back to the initial sort.
      }
    }
    return base;
  }

  const init0 = initialState();
  let sortKey = $state(init0.sortKey);
  let sortAsc = $state(init0.sortAsc);
  let query = $state(init0.query);
  let filters = $state<Record<string, string>>(init0.filters);
  let expandedKeys = $state<Set<string>>(new Set());

  $effect(() => {
    if (!persistKey) return;
    const snapshot: Persisted = { sortKey, sortAsc, query, filters };
    try {
      localStorage.setItem("datatable:" + persistKey, JSON.stringify(snapshot));
    } catch {
      // storage unavailable — sort/search simply do not persist.
    }
  });

  function toggleSort(col: DataColumn<Row>) {
    if (!col.sortable) return;
    if (sortKey === col.key) {
      sortAsc = !sortAsc;
    } else {
      sortKey = col.key;
      sortAsc = true;
    }
  }

  function alignClass(col: DataColumn<Row>): string {
    const align = col.align ?? (col.numeric ? "right" : "left");
    if (align === "right") return "text-right";
    if (align === "center") return "text-center";
    return "text-left";
  }

  // A numeric column renders in tabular figures so digits line up in a column.
  function numericClass(col: DataColumn<Row>): string {
    return col.numeric ? "tabular-nums" : "";
  }

  // A click that started inside an interactive element belongs to that
  // element: a rename button or a link in a cell must not also select the row.
  function rowClickHandler(row: Row) {
    return (event: MouseEvent) => {
      if (!onRowClick) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("button, a, input, select, textarea, label")) return;
      onRowClick(row);
    };
  }

  function rowKeyHandler(row: Row) {
    return (event: KeyboardEvent) => {
      if (!onRowClick) return;
      if (event.key !== "Enter" && event.key !== " ") return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("button, a, input, select, textarea")) return;
      event.preventDefault();
      onRowClick(row);
    };
  }

  function compare(a: unknown, b: unknown): number {
    const an = a === null || a === undefined;
    const bn = b === null || b === undefined;
    if (an && bn) return 0;
    if (an) return 1; // nulls last
    if (bn) return -1;
    if (typeof a === "number" && typeof b === "number") return a - b;
    return String(a).localeCompare(String(b), undefined, {
      sensitivity: "base",
      numeric: true,
    });
  }

  // Which columns actually get a filter control. A column the table cannot
  // read (`get` absent) is not filterable however it is declared, because the
  // filter has nothing to match against.
  function filterKindOf(col: DataColumn<Row>): "text" | "select" | false {
    if (!columnFilters) return false;
    if (col.filter === false) return false;
    if (!col.get) return false;
    return col.filter ?? "text";
  }

  const anyFilterable = $derived(columnFilters && columns.some((c) => filterKindOf(c)));

  function setFilter(key: string, value: string) {
    // Drop an emptied filter rather than storing "" — it keeps the persisted
    // object small and makes "is anything filtered" a simple key count.
    const next = { ...filters };
    if (value.trim()) next[key] = value;
    else delete next[key];
    filters = next;
  }

  const canExpand = $derived(!!expand);

  function rowExpandable(row: Row): boolean {
    if (!expand) return false;
    return expandable ? expandable(row) : true;
  }

  function toggleExpand(row: Row) {
    const key = rowKey(row);
    const next = new Set(expandedKeys);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
      onExpand?.(row);
    }
    expandedKeys = next;
  }

  const processed = $derived.by(() => {
    let list = rows;
    for (const [key, value] of Object.entries(filters)) {
      const col = columns.find((c) => c.key === key);
      const get = col?.get;
      if (!get) continue;
      const kind = filterKindOf(col);
      if (kind === "select") {
        list = list.filter((r) => {
          const v = get(r);
          return v != null && String(v) === value;
        });
      } else if (kind === "text") {
        const match = makeTextMatcher(value);
        list = list.filter((r) => {
          const v = get(r);
          return v != null && match(String(v));
        });
      }
    }
    if (search && query.trim()) {
      const match = makeTextMatcher(query);
      list = list.filter((r) =>
        columns.some((c) => {
          const v = c.get?.(r);
          return v != null && match(String(v));
        }),
      );
    }
    const col = sortKey ? columns.find((c) => c.key === sortKey) : undefined;
    if (col?.get) {
      const get = col.get;
      list = [...list].sort((a, b) => {
        const cmp = compare(get(a), get(b));
        return sortAsc ? cmp : -cmp;
      });
    }
    return list;
  });

  function ariaSort(col: DataColumn<Row>): "ascending" | "descending" | "none" {
    if (!col.sortable || sortKey !== col.key) return "none";
    return sortAsc ? "ascending" : "descending";
  }
</script>

{#if search}
  <div class="mb-3 max-w-md">
    <Input
      type="search"
      placeholder={searchPlaceholder || t("common.search")}
      bind:value={query}
    />
  </div>
{/if}

{#if processed.length === 0}
  <EmptyState message={emptyMessage} description={emptyDescription} icon={emptyIcon} />
{:else}
  <div class="overflow-x-auto">
    <table class="table-reflow w-full text-sm">
      <thead
        class="border-b border-slate-200 text-left text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)] dark:border-slate-800"
      >
        <tr>
          {#if canExpand}
            <th class="w-8 px-2 py-2" scope="col">
              <span class="sr-only">{t("datatable.details")}</span>
            </th>
          {/if}
          {#each columns as col (col.key)}
            <th
              class="px-3 py-2 {alignClass(col)} {col.headClass ?? ''}"
              aria-sort={ariaSort(col)}
              scope="col"
            >
              {#if col.sortable}
                <button
                  type="button"
                  class="inline-flex items-center gap-1 font-semibold uppercase tracking-wide hover:text-[var(--ha-primary-text-color)]"
                  onclick={() => toggleSort(col)}
                >
                  <span>{col.label}</span>
                  <span class="w-3 text-[10px]" aria-hidden="true">
                    {sortKey === col.key ? (sortAsc ? "▲" : "▼") : ""}
                  </span>
                </button>
              {:else}
                {col.label}
              {/if}
            </th>
          {/each}
        </tr>
        {#if anyFilterable}
          <!-- Filter row. One control per readable column; the filters
               combine with AND and persist beside sort and search, so a
               reload never shows a silently narrowed table. -->
          <tr class="border-b border-slate-200 dark:border-slate-800">
            {#if canExpand}<th class="px-2 py-1"></th>{/if}
            {#each columns as col (col.key)}
              {@const kind = filterKindOf(col)}
              <th class="px-2 py-1 font-normal">
                {#if kind === "select"}
                  <select
                    class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-1.5 py-1 text-xs font-normal text-[var(--ha-primary-text-color)]"
                    aria-label={t("datatable.filter_by", { column: col.label })}
                    value={filters[col.key] ?? ""}
                    onchange={(e) => setFilter(col.key, (e.target as HTMLSelectElement).value)}
                  >
                    <option value="">{t("datatable.filter_all")}</option>
                    {#each col.filterOptions ?? [] as opt (opt.value)}
                      <option value={opt.value}>{opt.label}</option>
                    {/each}
                  </select>
                {:else if kind === "text"}
                  <input
                    type="search"
                    class="w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-1.5 py-1 text-xs font-normal text-[var(--ha-primary-text-color)]"
                    aria-label={t("datatable.filter_by", { column: col.label })}
                    value={filters[col.key] ?? ""}
                    oninput={(e) => setFilter(col.key, (e.target as HTMLInputElement).value)}
                  />
                {/if}
              </th>
            {/each}
          </tr>
        {/if}
      </thead>
      <tbody>
        {#each processed as row (rowKey(row))}
          <tr
            class="border-b border-slate-100 last:border-0 hover:bg-slate-50 dark:border-[color-mix(in_srgb,var(--color-slate-800)_60%,transparent)] dark:hover:bg-[color-mix(in_srgb,var(--color-slate-800)_40%,transparent)] {onRowClick
              ? 'cursor-pointer'
              : ''} {selectedKey !== null && rowKey(row) === selectedKey
              ? 'bg-slate-100 dark:bg-[color-mix(in_srgb,var(--color-slate-800)_65%,transparent)]'
              : ''} {rowClass?.(row) ?? ''}"
            aria-selected={selectedKey === null
              ? undefined
              : rowKey(row) === selectedKey}
            tabindex={onRowClick ? 0 : undefined}
            onclick={onRowClick ? rowClickHandler(row) : undefined}
            onkeydown={onRowClick ? rowKeyHandler(row) : undefined}
          >
            {#if canExpand}
              <td class="w-8 px-2 py-2">
                {#if rowExpandable(row)}
                  {@const isOpen = expandedKeys.has(rowKey(row))}
                  <button
                    type="button"
                    class="inline-flex h-5 w-5 items-center justify-center rounded text-[var(--ha-secondary-text-color)] transition hover:bg-black/5 dark:hover:bg-white/5"
                    aria-expanded={isOpen}
                    aria-label={isOpen ? t("datatable.collapse") : t("datatable.expand")}
                    onclick={() => toggleExpand(row)}
                  >
                    <span aria-hidden="true" class="text-[10px]">{isOpen ? "▼" : "▶"}</span>
                  </button>
                {/if}
              </td>
            {/if}
            {#each columns as col (col.key)}
              <td
                class="px-3 py-2 {alignClass(col)} {numericClass(col)} {col.title
                  ? 'reflow-title'
                  : ''} {col.cellClass ?? ''}"
                data-label={col.label}
              >
                {#if cell}{@render cell(row, col)}{:else}{col.get?.(row) ?? "—"}{/if}
              </td>
            {/each}
          </tr>
          {#if canExpand && expandedKeys.has(rowKey(row))}
            <!-- The expansion spans every column, chevron cell included, so
                 nested content is not squeezed into one column's width. -->
            <tr class="border-b border-slate-100 dark:border-[color-mix(in_srgb,var(--color-slate-800)_60%,transparent)]">
              <td colspan={columns.length + 1} class="px-3 py-3">
                {@render expand?.(row)}
              </td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
{/if}
