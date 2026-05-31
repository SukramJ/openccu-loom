<script lang="ts">
  import { t } from "$lib/i18n";

  // Lightweight modal listing the keyboard shortcuts the SPA honours.
  // Triggered globally via "?" or from the topbar button. Shortcuts
  // are kept here as the single source of truth — every panel that
  // implements one should also list it.

  type Props = {
    open: boolean;
    onClose: () => void;
  };

  let { open, onClose }: Props = $props();

  const groups = $derived([
    {
      title: t("shortcut.group.general"),
      items: [
        { keys: ["?"], desc: t("shortcut.help_open") },
        { keys: ["Esc"], desc: t("shortcut.close_dialog") },
      ],
    },
    {
      title: t("shortcut.group.editor"),
      items: [
        { keys: ["⌘/Ctrl", "Z"], desc: t("shortcut.undo") },
        { keys: ["⌘/Ctrl", "Y"], desc: t("shortcut.redo") },
        { keys: ["⌘/Ctrl", "⇧", "Z"], desc: t("shortcut.redo") },
        { keys: ["⌘/Ctrl", "S"], desc: t("common.save") },
      ],
    },
  ]);

  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape" && open) {
      e.preventDefault();
      onClose();
    }
  }
</script>

<svelte:window onkeydown={onKey} />

{#if open}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("shortcut.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") onClose();
    }}
  >
    <div
      class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900"
    >
      <header class="mb-3 flex items-center justify-between">
        <h2 class="text-lg font-semibold">{t("shortcut.title")}</h2>
        <button
          type="button"
          class="rounded-md px-2 py-1 text-sm text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800"
          onclick={onClose}
          aria-label={t("common.close")}
        >
          ✕
        </button>
      </header>
      <div class="space-y-4">
        {#each groups as g (g.title)}
          <section>
            <h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
              {g.title}
            </h3>
            <ul class="space-y-1.5">
              {#each g.items as item, i (`${g.title}-${i}`)}
                <li class="flex items-center justify-between gap-3 text-sm">
                  <span class="text-slate-700 dark:text-slate-200">{item.desc}</span>
                  <span class="flex items-center gap-1">
                    {#each item.keys as k (k)}
                      <kbd class="rounded border border-slate-300 bg-slate-50 px-1.5 py-0.5 font-mono text-xs dark:border-slate-700 dark:bg-slate-800">{k}</kbd>
                    {/each}
                  </span>
                </li>
              {/each}
            </ul>
          </section>
        {/each}
      </div>
    </div>
  </div>
{/if}
