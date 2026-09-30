<script lang="ts">
  import type { Snippet } from "svelte";
  import { cn } from "$lib/utils";

  // DialogFrame — the overlay, focus handling and Escape behaviour of the
  // shared ConfirmDialog, for dialogs whose body is structured content (a
  // table of outcomes, a list of targets) that confirmStore.ask()'s string
  // body cannot carry. WritePreviewDialog follows the same language; this is
  // that language extracted so the device-admin dialogs do not each
  // hand-roll it.
  //
  // Enter is deliberately not bound: these dialogs end in a write, and a
  // global Enter would trigger it from wherever focus happens to sit.
  let {
    open,
    title,
    onClose,
    closeDisabled = false,
    size = "lg",
    children,
    footer,
  }: {
    open: boolean;
    title: string;
    onClose: () => void;
    // True while a request is in flight: Escape and a backdrop click do
    // nothing, so the operator cannot lose the outcome of a running write.
    closeDisabled?: boolean;
    size?: "md" | "lg" | "xl";
    children?: Snippet;
    footer?: Snippet;
  } = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);
  let previouslyFocused: HTMLElement | null = null;

  const FOCUSABLE =
    'button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])';

  function focusables(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE));
  }

  $effect(() => {
    if (open) {
      previouslyFocused = document.activeElement as HTMLElement | null;
      queueMicrotask(() => focusables()[0]?.focus());
    } else if (previouslyFocused) {
      previouslyFocused.focus();
      previouslyFocused = null;
    }
  });

  function requestClose() {
    if (!closeDisabled) onClose();
  }

  function onKey(e: KeyboardEvent) {
    if (!open) return;
    if (e.key === "Escape") {
      e.preventDefault();
      requestClose();
    } else if (e.key === "Tab") {
      const els = focusables();
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      const active = document.activeElement;
      const atEdge = e.shiftKey ? active === first : active === last;
      const outside = !els.includes(active as HTMLElement);
      if (atEdge || outside) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      }
    }
  }

  const widths = { md: "max-w-lg", lg: "max-w-2xl", xl: "max-w-4xl" } as const;
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={title}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) requestClose();
    }}
    onkeydown={() => {
      // Keyboard handling lives on the window listener above.
    }}
  >
    <div
      bind:this={dialogEl}
      class={cn("flex max-h-[85vh] w-full flex-col p-5", widths[size])}
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-3 text-lg font-semibold">{title}</h2>
      <div class="min-h-0 flex-1 overflow-y-auto">
        {@render children?.()}
      </div>
      {#if footer}
        <div class="mt-4 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          {@render footer()}
        </div>
      {/if}
    </div>
  </div>
{/if}
