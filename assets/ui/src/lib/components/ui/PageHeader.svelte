<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  /**
   * Shared page header: a title (+ optional subtitle) on the left and an
   * optional actions cluster on the right.
   *
   * The layout is deliberately container-responsive without a breakpoint: the
   * title block does NOT shrink (no `flex-1`/`min-w-0`), so a crowded row wraps
   * the actions onto their own line via `flex-wrap` instead of squeezing the
   * title into a sliver. Because `flex-wrap` reacts to the real (rendered)
   * width, it works correctly even when the viewport is wide but the content
   * area is narrow — e.g. inside the Home Assistant Ingress iframe.
   *
   * `above`, `titleContent` and `children` exist so a rich header (the device
   * page: breadcrumb, inline rename, a metadata line, room/function pickers)
   * still renders through this component rather than hand-rolling its own
   * `<h1>`. Six views used to do exactly that, and every one of them had
   * drifted to `font-semibold` against this component's `font-bold`, so the
   * title changed weight as the operator navigated.
   */
  interface Props {
    title: string;
    /** Optional secondary line under the title (e.g. a count or description). */
    subtitle?: string;
    /** Optional right-hand actions (buttons, filters, …). */
    actions?: Snippet;
    /** Rendered above the title — a breadcrumb, typically. */
    above?: Snippet;
    /** Replaces the <h1>, for a header that edits its own title in place. */
    titleContent?: Snippet;
    /** Rendered under the title block (metadata lines, status indicators). */
    children?: Snippet;
    /** Extra classes for the <header> element. */
    class?: string;
  }

  let {
    title,
    subtitle,
    actions,
    above,
    titleContent,
    children,
    class: cls = "",
  }: Props = $props();
</script>

<header class={cn("mb-4 flex flex-wrap items-start justify-between gap-3", cls)}>
  <div>
    {#if above}
      {@render above()}
    {/if}
    {#if titleContent}
      {@render titleContent()}
    {:else}
      <h1 class="text-balance text-2xl font-bold tracking-tight">{title}</h1>
    {/if}
    {#if subtitle}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{subtitle}</p>
    {/if}
    {#if children}
      {@render children()}
    {/if}
  </div>
  {#if actions}
    <div class="flex flex-wrap items-center gap-2">
      {@render actions()}
    </div>
  {/if}
</header>
