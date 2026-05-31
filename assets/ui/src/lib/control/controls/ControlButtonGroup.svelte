<!--
  Mirrors HA frontend's ha-control-button-group
  (frontend/src/components/ha-control-button-group.ts, Apache-2.0).
  12 px gap between children, 40 px container height, children
  expand via flex:1. Vertical mode flips the axis.
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  type Props = {
    vertical?: boolean;
    children?: Snippet;
  };

  let { vertical = false, children }: Props = $props();
</script>

<div
  class={cn(
    "flex w-full",
    vertical ? "flex-col gap-3" : "flex-row gap-3 h-10",
    "[&>*]:flex-1",
  )}
>
  {@render children?.()}
</div>
