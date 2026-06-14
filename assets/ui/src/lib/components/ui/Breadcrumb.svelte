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
    class="flex flex-wrap items-center gap-1 text-sm {className}"
    aria-label={t("ui.breadcrumb")}
    style="color: var(--ha-secondary-text-color);"
  >
    {#each items as crumb, i (i)}
      {#if i > 0}
        <Icon name="mdi:chevron-right" size={14} class="opacity-60" />
      {/if}
      {#if crumb.href && i < items.length - 1}
        <a
          href={crumb.href}
          class="hover:text-brand-700 hover:underline"
          style="color: var(--ha-secondary-text-color);"
        >
          {crumb.label}
        </a>
      {:else}
        <span
          class="font-medium"
          aria-current={i === items.length - 1 ? "page" : undefined}
          style="color: var(--ha-primary-text-color);"
        >
          {crumb.label}
        </span>
      {/if}
    {/each}
  </nav>
{/if}
