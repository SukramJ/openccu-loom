<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import { cn } from "$lib/utils";

  type Variant = "default" | "outline" | "ghost" | "destructive";
  type Size = "sm" | "md" | "lg" | "icon";

  type Props = HTMLButtonAttributes & {
    variant?: Variant;
    size?: Size;
    children?: Snippet;
  };

  let {
    variant = "default",
    size = "md",
    class: className,
    children,
    ...rest
  }: Props = $props();

  const variants: Record<Variant, string> = {
    default:
      "bg-brand-500 text-white hover:bg-brand-700 shadow-sm",
    outline:
      "border border-slate-300 bg-white text-slate-900 hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:hover:bg-slate-800",
    ghost:
      "text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800",
    destructive:
      "bg-red-600 text-white hover:bg-red-700 shadow-sm",
  };
  const sizes: Record<Size, string> = {
    sm: "h-8 px-3 text-sm",
    md: "h-9 px-4 text-sm",
    lg: "h-10 px-5 text-base",
    icon: "h-9 w-9",
  };
</script>

<button
  class={cn(
    "inline-flex items-center justify-center gap-2 rounded-md font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 disabled:cursor-not-allowed disabled:opacity-50",
    variants[variant],
    sizes[size],
    className,
  )}
  {...rest}
>
  {@render children?.()}
</button>
