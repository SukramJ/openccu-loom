<!--
  Mirrors HA frontend's hui-tile-card
  (frontend/src/panels/lovelace/cards/hui-tile-card.ts +
  frontend/src/panels/lovelace/cards/tile/tile-card-style.ts,
  Apache-2.0). State-coloured border accent driven by `--tile-color`;
  hero row (icon + info) on top, feature stack below.
-->
<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    /**
     * State-coloured CSS expression. Set by the resolver via
     * lib/control/state-color.ts. Drives the focused-state ring + the
     * tinted background of children that opt into `var(--tile-color)`.
     */
    tileColor: string;
    /** Adds the focus-ring shadow even without keyboard focus — used
     *  while the tile's primary interaction is engaged (slider drag). */
    focused?: boolean;
    icon?: Snippet;
    info?: Snippet;
    features?: Snippet;
  };

  let { tileColor, focused = false, icon, info, features }: Props = $props();
</script>

<div
  class="ha-tile rounded-xl border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-3 shadow-[var(--ha-elevation-card)] transition-shadow"
  style="--tile-color: {tileColor};"
  class:focused
>
  <div class="flex items-center gap-3">
    <div class="shrink-0">
      {@render icon?.()}
    </div>
    <div class="min-w-0 flex-1">
      {@render info?.()}
    </div>
  </div>
  {#if features}
    <div class="mt-3 space-y-3">
      {@render features?.()}
    </div>
  {/if}
</div>

<style>
  .ha-tile.focused {
    box-shadow:
      var(--ha-elevation-card),
      0 0 0 1px var(--tile-color);
    border-color: var(--tile-color);
  }
</style>
