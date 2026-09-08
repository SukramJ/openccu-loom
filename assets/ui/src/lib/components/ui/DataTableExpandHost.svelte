<script lang="ts" generics="Row extends { id: string }">
  import type { DataColumn } from "./data-table";
  import DataTable from "./DataTable.svelte";

  // Test host for DataTable's `expand` snippet. A snippet cannot be passed
  // through testing-library's plain props object, so the cases render this
  // wrapper instead — the same reason ChannelPanel's link tests use a host.
  let {
    rows,
    columns,
    expandable,
  }: {
    rows: Row[];
    columns: DataColumn<Row>[];
    expandable?: (row: Row) => boolean;
  } = $props();
</script>

{#snippet expansion(row: Row)}
  <div>expanded-content-{row.id}</div>
{/snippet}

<DataTable
  {rows}
  {columns}
  rowKey={(r) => r.id}
  emptyMessage="Nothing here"
  expand={expansion}
  {expandable}
/>
