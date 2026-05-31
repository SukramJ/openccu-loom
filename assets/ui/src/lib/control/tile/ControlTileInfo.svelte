<!--
  Mirrors HA frontend's ha-tile-info
  (frontend/src/components/tile/ha-tile-info.ts, Apache-2.0). Two
  stacked text rows: primary (e.g. entity name) + optional secondary
  (state display).
-->
<script lang="ts">
  type Props = {
    primary: string;
    secondary?: string;
  };

  let { primary, secondary }: Props = $props();
</script>

<div class="flex min-w-0 flex-col">
  <div class="truncate text-sm font-medium text-[var(--ha-primary-text-color)]">
    {primary}
  </div>
  {#if secondary}
    <div class="truncate text-xs text-[var(--ha-secondary-text-color)]">
      {secondary}
    </div>
  {/if}
</div>
