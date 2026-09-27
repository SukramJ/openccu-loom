<script lang="ts">
  import type { TaxonomyEnum } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { flatten, labelOf } from "$lib/taxonomy/tree";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Select from "$lib/components/ui/Select.svelte";

  // Assigns an address to nodes of one nested enum. A node is shown with
  // its whole path ("Erdgeschoss › Küche"), so two rooms of one name are
  // told apart, and it is written by path, never by name.
  type Props = {
    taxonomy: TaxonomyEnum | undefined;
    /** Paths of the nodes the address is assigned to. */
    selected: string[];
    onChange: (paths: string[]) => void;
    disabled?: boolean;
    ariaLabel?: string;
  };

  let { taxonomy, selected, onChange, disabled = false, ariaLabel }: Props = $props();

  const rows = $derived(flatten(taxonomy?.nodes));
  const options = $derived(
    rows
      .filter((r) => !selected.includes(r.path))
      .map((r) => ({ value: r.path, label: r.label })),
  );
  // The Select is a picker, not a stored value: it snaps back after each add.
  let pick = $state("");

  function add(path: string) {
    if (!path || selected.includes(path)) return;
    onChange([...selected, path]);
    pick = "";
  }
</script>

<div class="flex flex-wrap items-center gap-1.5">
  {#each selected as path (path)}
    {@const label = labelOf(taxonomy, path) ?? path}
    <span
      class="inline-flex items-center gap-1 rounded-full bg-[var(--ha-secondary-background-color)] px-2 py-0.5 text-xs text-[var(--ha-primary-text-color)]"
    >
      {label}
      {#if !disabled}
        <button
          type="button"
          class="rounded-full text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]"
          aria-label={t("taxonomy.picker.remove", { name: label })}
          onclick={() => onChange(selected.filter((p) => p !== path))}
        >
          <Icon name="mdi:close" size={12} aria-label="" />
        </button>
      {/if}
    </span>
  {/each}
  {#if !disabled && options.length > 0}
    <Select
      class="h-8 w-56"
      {options}
      bind:value={pick}
      placeholder={t("taxonomy.picker.add")}
      ariaLabel={ariaLabel ?? t("taxonomy.picker.add")}
      onValueChange={add}
    />
  {/if}
</div>
