<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";
  import { cn } from "$lib/utils";

  type Props = HTMLInputAttributes & {
    value?: string | number | null;
  };

  let { class: className, value = $bindable(), ...rest }: Props = $props();
</script>

<input
  bind:value
  class={cn(
    // `text-base sm:text-sm`: 16px on phones suppresses iOS Safari's
    // focus auto-zoom; 14px keeps desktop density. h-10 = 40px touch row.
    "h-10 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-1 text-base text-[var(--ha-primary-text-color)] shadow-sm transition sm:text-sm",
    "placeholder:text-[var(--ha-secondary-text-color)]",
    "focus-visible:border-[var(--ha-primary-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]",
    "disabled:cursor-not-allowed disabled:opacity-50",
    className,
  )}
  {...rest}
/>
