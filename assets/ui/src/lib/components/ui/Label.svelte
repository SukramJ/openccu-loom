<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLLabelAttributes } from "svelte/elements";
  import { cn } from "$lib/utils";

  type Props = HTMLLabelAttributes & { children?: Snippet };
  let { class: className, children, ...rest }: Props = $props();
</script>

<label
  class={cn(
    "block text-sm font-medium text-slate-700 dark:text-slate-300",
    className,
  )}
  {...rest}
>
  {@render children?.()}
</label>
