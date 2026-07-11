<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";
  import { cn } from "$lib/utils";

  type Variant = "default" | "outline" | "ghost" | "destructive" | "outline-destructive";
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

  // Disabled styling for the filled variants uses explicit muted slate
  // colours rather than the base `opacity-50`: at 50% opacity a saturated
  // teal / red fill still reads as "active" on a dark card, so a disabled
  // primary button was indistinguishable from an enabled one. The outline
  // and ghost variants keep the opacity dim (their fill is already neutral)
  // but add a dark-mode text damping so the label greys out on dark too.
  const disabledFill =
    "disabled:bg-slate-200 disabled:text-slate-400 disabled:shadow-none dark:disabled:bg-slate-800 dark:disabled:text-slate-500";
  const disabledMuted =
    "disabled:opacity-50 disabled:text-slate-400 dark:disabled:text-slate-500";
  const variants: Record<Variant, string> = {
    default:
      `bg-brand-500 text-white hover:bg-brand-700 shadow-sm ${disabledFill}`,
    outline:
      `border border-slate-300 bg-white text-slate-900 hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100 dark:hover:bg-slate-800 ${disabledMuted}`,
    ghost:
      `text-slate-700 hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-800 ${disabledMuted}`,
    destructive:
      `bg-red-600 text-white hover:bg-red-700 shadow-sm ${disabledFill}`,
    // Outline-weight danger: same footprint as `outline`, but a red
    // border/text signals "destructive" without the loud filled-red
    // block competing with everyday header actions for attention.
    // Hover fills solid red so the destructive intent is still explicit
    // at the point of commitment.
    "outline-destructive":
      `border border-red-300 bg-white text-red-600 hover:border-red-600 hover:bg-red-600 hover:text-white dark:border-red-900/60 dark:bg-slate-900 dark:text-red-400 dark:hover:border-red-600 dark:hover:bg-red-600 dark:hover:text-white ${disabledMuted}`,
  };
  // Touch-first heights: even the `sm` variant clears ~36px, `md` is a
  // comfortable 40px, and the icon button is a 40px square so single-tap
  // targets stay reliable on a phone. Desktop density is unaffected.
  const sizes: Record<Size, string> = {
    sm: "h-9 px-3 text-sm",
    md: "h-10 px-4 text-sm",
    lg: "h-11 px-5 text-base",
    icon: "h-10 w-10",
  };
</script>

<button
  class={cn(
    "inline-flex items-center justify-center gap-2 rounded-md font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 disabled:cursor-not-allowed",
    variants[variant],
    sizes[size],
    className,
  )}
  {...rest}
>
  {@render children?.()}
</button>
