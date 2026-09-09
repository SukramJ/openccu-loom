<script lang="ts">
  import type { TabItem } from "./tabs";
  import { cn } from "$lib/utils";
  import Badge from "./Badge.svelte";
  import Icon from "./Icon.svelte";

  /**
   * Tabs — the shared tab strip, in the SPA's two established weights.
   *
   * Seven hand-rolled strips had drifted apart before this: the alarm, security
   * and matter shells used `py-3` links spread with `flex-1`; the device page,
   * the message list and the keypress sections used `py-2` buttons aligned
   * left; and the active marker was `brand-600/400` in three of them,
   * `brand-500/700/300` in the device page and `brand-500/700` — with no dark
   * variant at all — in the keypress strip. The strip therefore changed height,
   * alignment and colour depending on which area the operator was in.
   *
   *  - `underline` — primary, branded navigation. One per page.
   *  - `segmented` — a quiet recessed track for a second level nested under an
   *    underline strip, so the two levels do not compete for attention. Its
   *    track keeps the plain slate ramp rather than
   *    `--ha-secondary-background-color`: under the loom skin that token sits a
   *    hair away from the card colour, which leaves the track invisible.
   *
   * An item with `href` renders as a link (route-driven tabs, deep-linkable);
   * one without renders as a button and reports through `onSelect`.
   */
  interface Props {
    items: TabItem[];
    /** `key` of the active tab. */
    active: string;
    variant?: "underline" | "segmented";
    /** Spread the tabs evenly across the full width (route-level strips). */
    fill?: boolean;
    ariaLabel?: string;
    onSelect?: (key: string) => void;
    class?: string;
  }

  let {
    items,
    active,
    variant = "underline",
    fill = false,
    ariaLabel,
    onSelect,
    class: cls = "",
  }: Props = $props();

  const listClass = $derived(
    variant === "underline"
      ? cn("flex gap-1 overflow-x-auto border-b border-[var(--ha-divider-color)]", cls)
      : cn(
          "inline-flex flex-wrap gap-0.5 rounded-lg bg-slate-100 p-0.5 dark:bg-slate-800",
          cls,
        ),
  );

  function itemClass(isActive: boolean): string {
    if (variant === "underline") {
      return cn(
        "-mb-px inline-flex items-center justify-center gap-2 whitespace-nowrap border-b-2 px-4 py-3 text-center text-sm font-medium transition",
        fill && "flex-1",
        isActive
          ? "border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400"
          : "border-transparent text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-primary-text-color)]",
      );
    }
    return cn(
      "rounded-md px-3 py-1.5 text-sm font-medium transition",
      isActive
        ? "bg-white text-slate-900 shadow-sm dark:bg-slate-700 dark:text-white"
        : "text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white",
    );
  }
</script>

<div class={listClass} role="tablist" aria-label={ariaLabel}>
  {#each items as item (item.key)}
    {@const isActive = item.key === active}
    {#if item.href}
      <a
        href={item.href}
        role="tab"
        aria-selected={isActive}
        class={itemClass(isActive)}
        onclick={() => onSelect?.(item.key)}
      >
        {#if item.icon}<Icon name={item.icon} size={16} />{/if}
        {item.label}
        {#if item.badge !== undefined}<Badge variant="muted">{item.badge}</Badge>{/if}
      </a>
    {:else}
      <button
        type="button"
        role="tab"
        aria-selected={isActive}
        class={itemClass(isActive)}
        onclick={() => onSelect?.(item.key)}
      >
        {#if item.icon}<Icon name={item.icon} size={16} />{/if}
        {item.label}
        {#if item.badge !== undefined}<Badge variant="muted">{item.badge}</Badge>{/if}
      </button>
    {/if}
  {/each}
</div>
