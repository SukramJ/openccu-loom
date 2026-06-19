<script lang="ts">
  import Icon from "./Icon.svelte";
  import { t } from "$lib/i18n";

  // Lightweight breadcrumb. Each entry is either a hash href or a
  // terminal label (the current page). Mirrors
  // homematicip-local-frontend/components/breadcrumb.ts but rendered
  // with our HA semantic tokens.

  export type Crumb = {
    label: string;
    href?: string;
  };

  type Props = {
    items: Crumb[];
    class?: string;
  };

  let { items, class: className = "" }: Props = $props();
</script>

{#if items.length > 0}
  <nav
    class="flex flex-wrap items-center gap-1 text-sm text-[var(--ha-secondary-text-color)] dark:text-slate-400 {className}"
    aria-label={t("ui.breadcrumb")}
  >
    {#each items as crumb, i (i)}
      {#if i > 0}
        <Icon name="mdi:chevron-right" size={14} class="opacity-60" />
      {/if}
      {#if crumb.href && i < items.length - 1}
        <a
          href={crumb.href}
          class="text-[var(--ha-secondary-text-color)] hover:text-brand-700 hover:underline dark:text-slate-400 dark:hover:text-brand-300"
        >
          {crumb.label}
        </a>
      {:else}
        <span
          class="font-medium text-[var(--ha-primary-text-color)] dark:text-slate-100"
          aria-current={i === items.length - 1 ? "page" : undefined}
        >
          {crumb.label}
        </span>
      {/if}
    {/each}
  </nav>
{/if}
