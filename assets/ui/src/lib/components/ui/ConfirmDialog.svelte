<script lang="ts">
  import { t } from "$lib/i18n";
  import Button from "./Button.svelte";
  import Switch from "./Switch.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  // Renderer for the global confirm-dialog singleton. Mount once
  // (typically inside App.svelte) and call `confirmStore.ask({...})`
  // from anywhere to get a Promise<boolean>.

  // Reactive read of the singleton — updates whenever ask() / resolve()
  // is called.
  const pending = $derived(confirmStore.pending);

  // Focus-trap bookkeeping. `dialogEl` roots the search for the two
  // focusable buttons; `previouslyFocused` is the element that had focus
  // before the dialog opened, so it can be restored once it closes.
  let dialogEl = $state<HTMLDivElement | null>(null);
  let previouslyFocused: HTMLElement | null = null;

  function focusableButtons(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(dialogEl.querySelectorAll<HTMLElement>("button"));
  }

  $effect(() => {
    if (pending) {
      previouslyFocused = document.activeElement as HTMLElement | null;
      // The dialog's DOM is inserted by the {#if} block that guards this
      // effect's dependency, so it exists once this runs; queue past the
      // current microtask to let Svelte finish committing it either way.
      queueMicrotask(() => focusableButtons()[0]?.focus());
    } else if (previouslyFocused) {
      previouslyFocused.focus();
      previouslyFocused = null;
    }
  });

  // Escape and Tab are handled globally because they belong to the dialog as
  // a whole. Enter deliberately is not: focus starts on Cancel, so a global
  // Enter shortcut would confirm the destructive action the operator was
  // about to decline. Enter on a focused <button> activates that button
  // natively, which is the behaviour every call site wants.
  function onKey(e: KeyboardEvent) {
    if (!pending) return;
    if (e.key === "Escape") {
      e.preventDefault();
      confirmStore.resolve(false);
    } else if (e.key === "Tab") {
      const els = focusableButtons();
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
</script>

<svelte:window onkeydown={onKey} />

{#if pending}
  {@const opts = pending.options}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={opts.title}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) confirmStore.resolve(false);
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") confirmStore.resolve(false);
    }}
  >
    <div
      bind:this={dialogEl}
      class="w-full max-w-md p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-2 text-lg font-semibold">{opts.title}</h2>
      {#if opts.body}
        <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
          {opts.body}
        </p>
      {/if}
      {#if opts.checkbox}
        <label
          class="mb-4 flex cursor-pointer items-center gap-2 text-sm"
          style="color: var(--ha-primary-text-color);"
        >
          <Switch
            checked={confirmStore.checkboxChecked}
            onCheckedChange={(v) => confirmStore.setCheckboxChecked(v)}
          />
          <span>{opts.checkbox.label}</span>
        </label>
      {/if}
      <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button
          type="button"
          variant="outline"
          size="md"
          class="w-full sm:w-auto"
          onclick={() => confirmStore.resolve(false)}
        >
          {opts.cancelLabel ?? t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant={opts.destructive ? "destructive" : "default"}
          size="md"
          class="w-full sm:w-auto"
          onclick={() => confirmStore.resolve(true)}
        >
          {opts.confirmLabel ?? t("common.ok")}
        </Button>
      </div>
    </div>
  </div>
{/if}
