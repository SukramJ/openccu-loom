<script lang="ts">
  import type { PreviewEntry } from "$lib/channel/write-preview";
  import { previewBody } from "$lib/channel/write-preview";
  import { t } from "$lib/i18n";
  import Button from "$lib/components/ui/Button.svelte";

  // WritePreviewDialog — shown before a MASTER or LINK write when the
  // operator has the write-preview preference on (the default).
  //
  // It is its own component rather than a confirmStore.ask() call because
  // the shared confirm dialog takes a string body, and what makes this
  // preview worth reading is structure: a per-parameter from/to table, the
  // request line the write goes to, and the exact JSON body. Follows
  // ConfirmDialog's overlay, focus-trap and Escape behaviour so the two
  // read as one dialog language.
  let {
    open,
    entries,
    request,
    onCancel,
    onConfirm,
  }: {
    open: boolean;
    entries: PreviewEntry[];
    // The request line, e.g. `PUT /api/v1/devices/ABC:1/paramsets/MASTER`.
    request: string;
    onCancel: () => void;
    onConfirm: () => void;
  } = $props();

  let dialogEl = $state<HTMLDivElement | null>(null);
  let previouslyFocused: HTMLElement | null = null;

  const body = $derived(JSON.stringify(previewBody(entries), null, 2));

  function focusableButtons(): HTMLElement[] {
    if (!dialogEl) return [];
    return Array.from(dialogEl.querySelectorAll<HTMLElement>("button"));
  }

  $effect(() => {
    if (open) {
      previouslyFocused = document.activeElement as HTMLElement | null;
      queueMicrotask(() => focusableButtons()[0]?.focus());
    } else if (previouslyFocused) {
      previouslyFocused.focus();
      previouslyFocused = null;
    }
  });

  // Escape cancels; Tab is trapped. Enter is deliberately not bound: focus
  // starts on Cancel, and a global Enter would write the paramset the
  // operator opened this dialog to inspect.
  function onKey(e: KeyboardEvent) {
    if (!open) return;
    if (e.key === "Escape") {
      e.preventDefault();
      onCancel();
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

{#if open}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={t("channel.preview.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) onCancel();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") onCancel();
    }}
  >
    <div
      bind:this={dialogEl}
      class="flex max-h-[85vh] w-full max-w-2xl flex-col p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-3 text-lg font-semibold">{t("channel.preview.title")}</h2>

      <div class="min-h-0 flex-1 overflow-y-auto">
        {#if entries.length === 0}
          <p class="text-sm" style="color: var(--ha-secondary-text-color);">
            {t("channel.preview.nothing_to_write")}
          </p>
        {:else}
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead
                class="border-b border-slate-200 text-left text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)] dark:border-slate-800"
              >
                <tr>
                  <th class="px-2 py-1.5" scope="col">{t("channel.preview.col.parameter")}</th>
                  <th class="px-2 py-1.5" scope="col">{t("channel.preview.col.from")}</th>
                  <th class="px-2 py-1.5" scope="col">{t("channel.preview.col.to")}</th>
                </tr>
              </thead>
              <tbody>
                {#each entries as entry (entry.name)}
                  <tr class="border-b border-slate-100 last:border-0 dark:border-[color-mix(in_srgb,var(--color-slate-800)_60%,transparent)]">
                    <td class="px-2 py-1.5">
                      <span class="font-medium">{entry.label}</span>
                      <span class="block font-mono text-xs text-[var(--ha-secondary-text-color)]">
                        {entry.name}
                      </span>
                    </td>
                    <td class="px-2 py-1.5 text-[var(--ha-secondary-text-color)]">{entry.from}</td>
                    <td class="px-2 py-1.5 font-medium">{entry.to}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>

          <h3 class="mt-4 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("channel.preview.request")}
          </h3>
          <pre class="mt-1 overflow-x-auto rounded-md bg-slate-100 p-2 font-mono text-xs dark:bg-[color-mix(in_srgb,var(--color-slate-800)_50%,transparent)]">{request}</pre>

          <h3 class="mt-3 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
            {t("channel.preview.body")}
          </h3>
          <pre class="mt-1 overflow-x-auto rounded-md bg-slate-100 p-2 font-mono text-xs dark:bg-[color-mix(in_srgb,var(--color-slate-800)_50%,transparent)]">{body}</pre>
        {/if}
      </div>

      <div class="mt-4 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button type="button" variant="outline" size="md" class="w-full sm:w-auto" onclick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="md"
          class="w-full sm:w-auto"
          disabled={entries.length === 0}
          onclick={onConfirm}
        >
          {t("channel.preview.write")}
        </Button>
      </div>
    </div>
  </div>
{/if}
