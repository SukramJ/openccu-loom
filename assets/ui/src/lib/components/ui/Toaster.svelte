<script lang="ts">
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // Fixed-corner toast viewport. Stacks newest at the bottom.
  // Each toast is dismissable; `severity` drives colour.

  function colourFor(severity: "info" | "success" | "warn" | "error"): string {
    switch (severity) {
      case "success":
        return "border-emerald-300 bg-emerald-50 text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-100";
      case "warn":
        return "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-100";
      case "error":
        return "border-red-300 bg-red-50 text-red-900 dark:border-red-800 dark:bg-red-950 dark:text-red-100";
      default:
        return "border-slate-300 bg-white text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100";
    }
  }
</script>

<div
  class="pin-b-safe pin-r-safe pointer-events-none fixed z-50 flex w-[calc(100%-2rem)] max-w-sm flex-col gap-2"
  role="region"
  aria-live="polite"
>
  {#each toastStore.items as toast (toast.id)}
    <div
      role="alert"
      class="pointer-events-auto rounded-md border px-3 py-2 text-sm shadow-lg {colourFor(toast.severity)}"
    >
      <div class="flex items-start gap-2">
        <div class="min-w-0 flex-1">
          <p class="font-medium">{toast.message}</p>
          {#if toast.detail}
            <p class="mt-1 text-xs opacity-80">{toast.detail}</p>
          {/if}
        </div>
        <button
          type="button"
          class="-mt-1 -mr-1 flex h-9 w-9 items-center justify-center rounded text-lg leading-none opacity-70 hover:opacity-100"
          onclick={() => toastStore.dismiss(toast.id)}
          aria-label={t("ui.dismiss")}
        >
          ×
        </button>
      </div>
    </div>
  {/each}
</div>
