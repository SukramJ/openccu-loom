<!--
  Mirrors HA frontend's ha-control-button
  (frontend/src/components/ha-control-button.ts, Apache-2.0). 40 px
  square minimum, pill border-radius, 20 %-opacity background of a
  state colour when active, ghost outline when inactive.
  Reimplemented in Svelte 5 + Tailwind 4.
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  type Props = {
    active?: boolean;
    disabled?: boolean;
    /** CSS colour expression used when `active=true`. */
    color?: string;
    label?: string;
    onClick: () => void;
    children?: Snippet;
  };

  let {
    active = false,
    disabled = false,
    color = "var(--ha-primary-color)",
    label,
    onClick,
    children,
  }: Props = $props();
</script>

<button
  type="button"
  class={cn(
    "relative inline-flex h-10 min-w-10 items-center justify-center gap-1 rounded-md px-2 text-sm font-medium transition-colors",
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1",
    disabled && "cursor-not-allowed opacity-50",
  )}
  style={active
    ? `background-color: color-mix(in srgb, ${color} 20%, transparent); color: ${color};`
    : "background-color: color-mix(in srgb, var(--ha-secondary-text-color) 12%, transparent); color: var(--ha-primary-text-color);"}
  aria-label={label}
  aria-pressed={active}
  {disabled}
  onclick={onClick}
>
  {@render children?.()}
</button>
