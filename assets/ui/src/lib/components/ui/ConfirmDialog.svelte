<script lang="ts">
  import { t } from "$lib/i18n";
  import Button from "./Button.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  // Renderer for the global confirm-dialog singleton. Mount once
  // (typically inside App.svelte) and call `confirmStore.ask({...})`
  // from anywhere to get a Promise<boolean>.

  // Reactive read of the singleton — updates whenever ask() / resolve()
  // is called.
  const pending = $derived(confirmStore.pending);

  function onKey(e: KeyboardEvent) {
    if (!pending) return;
    if (e.key === "Escape") {
      e.preventDefault();
      confirmStore.resolve(false);
    } else if (e.key === "Enter") {
      e.preventDefault();
      confirmStore.resolve(true);
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
      class="w-full max-w-md p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-2 text-lg font-semibold">{opts.title}</h2>
      {#if opts.body}
        <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
          {opts.body}
        </p>
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
