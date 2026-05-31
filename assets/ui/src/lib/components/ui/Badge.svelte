<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  type Variant = "default" | "success" | "warning" | "danger" | "muted";
  type Props = { variant?: Variant; class?: string; children?: Snippet };

  let {
    variant = "default",
    class: className,
    children,
  }: Props = $props();

  const variants: Record<Variant, string> = {
    default:
      "bg-brand-50 text-brand-900 dark:bg-brand-900/30 dark:text-brand-50",
    success:
      "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-200",
    warning:
      "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200",
    danger:
      "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-200",
    muted:
      "bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300",
  };
</script>

<span
  class={cn(
    "inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium",
    variants[variant],
    className,
  )}
>
  {@render children?.()}
</span>
