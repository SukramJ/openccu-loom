<script lang="ts" generics="Row">
  import type { DataColumn } from "./data-table";
  import DataTable from "./DataTable.svelte";

  // Test host for DataTable's `groupBy`/`groupHeader` capability. A snippet
  // cannot be passed through testing-library's plain props object, so the
  // cases render this wrapper instead — the same reason DataTableExpandHost
  // exists for `expand`.
  let {
    rows,
    columns,
    rowKey,
    groupBy,
    withHeader = false,
  }: {
    rows: Row[];
    columns: DataColumn<Row>[];
    rowKey: (row: Row) => string;
    groupBy: (row: Row) => string;
    withHeader?: boolean;
  } = $props();
</script>

{#snippet header(key: string, groupRows: Row[])}
  <span>section-{key}-{groupRows.length}</span>
{/snippet}

<DataTable
  {rows}
  {columns}
  {rowKey}
  emptyMessage="Nothing here"
  {groupBy}
  groupHeader={withHeader ? header : undefined}
/>
