<script lang="ts">
  import type { Snippet } from "svelte";

  /**
   * Shared list-view page header: a title (+ optional subtitle) on the left and
   * an optional actions cluster on the right.
   *
   * The layout is deliberately container-responsive without a breakpoint: the
   * title block does NOT shrink (no `flex-1`/`min-w-0`), so a crowded row wraps
   * the actions onto their own line via `flex-wrap` instead of squeezing the
   * title into a sliver. Because `flex-wrap` reacts to the real (rendered)
   * width, it works correctly even when the viewport is wide but the content
   * area is narrow — e.g. inside the Home Assistant Ingress iframe.
   */
  interface Props {
    title: string;
    /** Optional secondary line under the title (e.g. a count or description). */
    subtitle?: string;
    /** Optional right-hand actions (buttons, filters, …). */
    actions?: Snippet;
    /** Extra classes for the <header> element. */
    class?: string;
  }

  let { title, subtitle, actions, class: cls = "" }: Props = $props();
</script>

<header class={`mb-4 flex flex-wrap items-start justify-between gap-3 ${cls}`}>
  <div>
    <h1 class="text-balance text-2xl font-bold tracking-tight">{title}</h1>
    {#if subtitle}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{subtitle}</p>
    {/if}
  </div>
  {#if actions}
    <div class="flex flex-wrap items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</header>
