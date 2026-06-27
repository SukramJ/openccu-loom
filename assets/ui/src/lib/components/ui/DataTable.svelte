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
    emptyIcon = "mdi:format-list-bulleted",
    cell,
    rowClass,
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
    emptyIcon?: IconName;
    cell?: Snippet<[Row, DataColumn<Row>]>;
    rowClass?: (row: Row) => string;
  } = $props();

  type Persisted = { sortKey: string; sortAsc: boolean; query: string };

  // Compute the initial sort/search inside a function so the `initialSort`
  // prop is read once (non-reactively) rather than captured in a $state
  // initializer; persisted state (when persistKey is set) wins.
  function initialState(): Persisted {
    let base: Persisted = {
      sortKey: initialSort?.key ?? "",
      sortAsc: initialSort?.asc ?? true,
      query: "",
    };
    if (persistKey) {
      try {
        const raw = localStorage.getItem("datatable:" + persistKey);
        if (raw) base = { ...base, ...(JSON.parse(raw) as Persisted) };
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

  $effect(() => {
    if (!persistKey) return;
    const snapshot: Persisted = { sortKey, sortAsc, query };
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

  function alignClass(align?: string): string {
    if (align === "right") return "text-right";
    if (align === "center") return "text-center";
    return "text-left";
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

  const processed = $derived.by(() => {
    let list = rows;
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
  <EmptyState message={emptyMessage} icon={emptyIcon} />
{:else}
  <div class="overflow-x-auto">
    <table class="table-reflow w-full text-sm">
      <thead
        class="border-b border-slate-200 text-left text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)] dark:border-slate-800"
      >
        <tr>
          {#each columns as col (col.key)}
            <th
              class="px-3 py-2 {alignClass(col.align)}"
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
      </thead>
      <tbody>
        {#each processed as row (rowKey(row))}
          <tr
            class="border-b border-slate-100 last:border-0 hover:bg-slate-50 dark:border-slate-800/60 dark:hover:bg-slate-800/40 {rowClass?.(
              row,
            ) ?? ''}"
          >
            {#each columns as col (col.key)}
              <td
                class="px-3 py-2 {alignClass(col.align)} {col.title
                  ? 'reflow-title'
                  : ''} {col.cellClass ?? ''}"
                data-label={col.label}
              >
                {#if cell}{@render cell(row, col)}{:else}{col.get?.(row) ?? "—"}{/if}
              </td>
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
