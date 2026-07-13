<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLAttributes } from "svelte/elements";
  import { cn } from "$lib/utils";

  type Props = HTMLAttributes<HTMLDivElement> & { children?: Snippet };
  let { class: className, children, ...rest }: Props = $props();
</script>

<div
  class={cn(
    "rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] shadow-sm",
    className,
  )}
  {...rest}
>
  {@render children?.()}
</div>
