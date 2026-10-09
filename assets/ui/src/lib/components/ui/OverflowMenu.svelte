<script lang="ts">
  // A "⋯" button that opens a short list of secondary actions — the place
  // for what a toolbar needs reachable but not in view (export, import).
  // A disclosure menu rather than a floating popover: the list renders in
  // flow under the button, so it needs no positioning layer, and Escape or
  // a click elsewhere closes it.
  import Icon from "./Icon.svelte";

  type Item = { label: string; onSelect: () => void; disabled?: boolean };
  type Props = { items: Item[]; ariaLabel: string };
  let { items, ariaLabel }: Props = $props();

  let open = $state(false);
  let root = $state<HTMLElement | null>(null);

  $effect(() => {
    if (!open) return;
    function onDocClick(e: MouseEvent) {
      if (root && !root.contains(e.target as Node)) open = false;
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") open = false;
    }
    document.addEventListener("click", onDocClick, true);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("click", onDocClick, true);
      document.removeEventListener("keydown", onKey);
    };
  });

  function select(item: Item) {
    open = false;
    item.onSelect();
  }
</script>

<div class="relative inline-block" bind:this={root}>
  <button
    type="button"
    class="inline-flex h-10 w-10 items-center justify-center rounded-md border border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] transition hover:text-[var(--ha-primary-text-color)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ha-primary-color)]"
    aria-label={ariaLabel}
    title={ariaLabel}
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    <Icon name="mdi:dots-horizontal" size={18} />
  </button>
  {#if open}
    <div
      role="menu"
      aria-label={ariaLabel}
      class="absolute right-0 z-30 mt-1 min-w-[10rem] rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] py-1 shadow-lg"
    >
      {#each items as item (item.label)}
        <button
          type="button"
          role="menuitem"
          class="block w-full px-3 py-2 text-left text-sm text-[var(--ha-primary-text-color)] hover:bg-[var(--ha-secondary-background-color)] disabled:opacity-50"
          disabled={item.disabled}
          onclick={() => select(item)}
        >
          {item.label}
        </button>
      {/each}
    </div>
  {/if}
</div>
