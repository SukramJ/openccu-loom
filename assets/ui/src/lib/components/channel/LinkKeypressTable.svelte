<script lang="ts">
  import type { UISchemaParameter } from "$lib/api/types";
  import ParameterGrid from "./ParameterGrid.svelte";
  import { keypressRows } from "$lib/links/keypress-rows";
  import type { ParamValues } from "$lib/channel/validate";
  import { t } from "$lib/i18n";

  // LINK parameters as a short/long comparison: each row holds the
  // setting for a short press next to the same setting for a long press,
  // so the two can be read and edited side by side instead of on two
  // tabs. Parameters outside either keypress come first, untabled.
  // Each cell is its own ParameterGrid, which keeps time pairs, the
  // brightness helper and the dirty / read-back badges exactly as the
  // stacked layout renders them.
  type Props = {
    parameters: UISchemaParameter[];
    values: ParamValues;
    dirty: Set<string>;
    errors: Record<string, string>;
    readBack?: Map<string, string>;
    locale: string;
    locked?: Set<string>;
    brightnessSource?: { value: number; unit: string | null } | null;
    onParamChange: (name: string, value: unknown) => void;
    onAction?: (name: string) => void;
    /** False hides the raw CCU names — the profile view does. */
    showRawName?: boolean;
  };

  let {
    parameters,
    values,
    dirty,
    errors,
    readBack,
    locale,
    locked,
    brightnessSource = null,
    onParamChange,
    onAction,
    showRawName = true,
  }: Props = $props();

  const layout = $derived(keypressRows(parameters));
</script>

{#snippet cell(params: UISchemaParameter[], caption: string)}
  <div class="min-w-0">
    <p
      class="mb-1 text-[11px] font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)] @3xl:hidden"
    >
      {caption}
    </p>
    {#if params.length > 0}
      <ParameterGrid
        parameters={params}
        {values}
        {dirty}
        {errors}
        {readBack}
        {locale}
        {locked}
        {brightnessSource}
        {onParamChange}
        {onAction}
        {showRawName}
      />
    {:else}
      <p class="py-2 text-sm text-[var(--ha-disabled-text-color)]">—</p>
    {/if}
  </div>
{/snippet}

{#if layout.common.length > 0}
  <section class="mb-4">
    <ParameterGrid
      parameters={layout.common}
      {values}
      {dirty}
      {errors}
      {readBack}
      {locale}
      {locked}
      {brightnessSource}
      {onParamChange}
      {onAction}
      {showRawName}
    />
  </section>
{/if}

{#if layout.rows.length > 0}
  <div class="@container" data-testid="keypress-table">
    <div
      class="hidden gap-4 border-b border-[var(--ha-divider-color)] pb-1 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)] @3xl:grid @3xl:grid-cols-2"
    >
      <span>{t("channel.tab.short")}</span>
      <span>{t("channel.tab.long")}</span>
    </div>
    <div class="divide-y divide-[var(--ha-divider-color)]">
      {#each layout.rows as row (row.stem)}
        <div class="grid grid-cols-1 gap-x-4 gap-y-2 py-2 @3xl:grid-cols-2" data-row={row.stem}>
          {@render cell(row.short, t("channel.tab.short"))}
          {@render cell(row.long, t("channel.tab.long"))}
        </div>
      {/each}
    </div>
  </div>
{/if}
