<script lang="ts">
  import { cn } from "$lib/utils";

  // Shared loading spinner. Decorative by design — the surrounding context
  // (e.g. LoadingState) owns the status/live region and the accessible label,
  // so the spinner itself is aria-hidden to avoid duplicate status roles.
  // Tailwind-only colours (with a dark: variant) so it renders correctly in
  // both themes without relying on CSS-var tokens.
  type Props = {
    size?: number;
    class?: string;
  };

  let { size = 18, class: className }: Props = $props();
</script>

<span
  class={cn(
    "inline-block animate-spin rounded-full border-2 border-current border-t-transparent text-slate-400 dark:text-slate-500",
    className,
  )}
  style="width:{size}px;height:{size}px"
  aria-hidden="true"
></span>
