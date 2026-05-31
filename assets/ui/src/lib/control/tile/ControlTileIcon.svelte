<!--
  Mirrors HA frontend's ha-tile-icon
  (frontend/src/components/tile/ha-tile-icon.ts, Apache-2.0). 40 px
  circle; background is the tile colour at 12 % opacity when active,
  divider-color when neutral. Glyph (an icon, emoji, or letter) is
  passed via the default snippet.
-->
<script lang="ts">
  import type { Snippet } from "svelte";

  type Props = {
    /** When true the icon background fills with the tile colour. */
    active?: boolean;
    /** Adds an `onclick` handler — turns the icon into a tap target. */
    onClick?: () => void;
    label?: string;
    children?: Snippet;
  };

  let { active = false, onClick, label, children }: Props = $props();
</script>

{#if onClick}
  <button
    type="button"
    class="ha-tile-icon"
    class:active
    aria-label={label}
    onclick={() => onClick?.()}
  >
    {@render children?.()}
  </button>
{:else}
  <div class="ha-tile-icon" class:active aria-label={label} role="img">
    {@render children?.()}
  </div>
{/if}

<style>
  .ha-tile-icon {
    display: inline-flex;
    height: 2.5rem;
    width: 2.5rem;
    align-items: center;
    justify-content: center;
    border-radius: 9999px;
    background-color: color-mix(in srgb, var(--ha-secondary-text-color) 12%, transparent);
    color: var(--ha-secondary-text-color);
    transition:
      background-color 180ms ease-in-out,
      color 180ms ease-in-out;
  }
  .ha-tile-icon.active {
    background-color: color-mix(in srgb, var(--tile-color) 20%, transparent);
    color: var(--tile-color);
  }
  button.ha-tile-icon {
    border: none;
    cursor: pointer;
    padding: 0;
  }
  button.ha-tile-icon:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px var(--tile-color);
  }
</style>
