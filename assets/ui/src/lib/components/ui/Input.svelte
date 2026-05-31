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
    "h-9 w-full rounded-md border border-slate-300 bg-white px-3 py-1 text-sm shadow-sm transition",
    "placeholder:text-[var(--ha-secondary-text-color)]",
    "focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500",
    "disabled:cursor-not-allowed disabled:opacity-50",
    "dark:border-slate-700 dark:bg-slate-900",
    className,
  )}
  {...rest}
/>
