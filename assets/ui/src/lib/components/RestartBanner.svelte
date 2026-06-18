<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import {
    restartPending,
    restartCaps,
    loadRestartCaps,
  } from "$lib/stores/restartPending.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  onMount(() => {
    void loadRestartCaps();
  });

  async function handleRestart() {
    const ok = await confirmStore.ask({
      title: t("settings.restart_daemon"),
      body: t("settings.restart_confirm"),
      confirmLabel: t("restart.now"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.restartDaemon();
      toastStore.success(t("settings.restart_signalled"));
    } catch {
      // error is surfaced by the toast in the settings panel; swallow here
    }
  }
</script>

{#if restartPending.pending}
  <div
    class="flex w-full items-center gap-3 bg-amber-50 px-4 py-2.5 text-amber-900 dark:bg-amber-950 dark:text-amber-100"
    role="alert"
  >
    <Icon name="mdi:alert-triangle" size={18} class="shrink-0 text-amber-600 dark:text-amber-400" />
    <button
      type="button"
      class="flex-1 cursor-pointer text-left text-sm font-medium hover:underline"
      onclick={() => (location.hash = "#/settings")}
    >
      {t("restart.banner_text")}
    </button>
    {#if restartCaps.supervised}
      <Button
        variant="outline"
        size="sm"
        class="shrink-0 border-amber-400 bg-transparent text-amber-900 hover:bg-amber-100 dark:border-amber-600 dark:text-amber-100 dark:hover:bg-amber-900"
        onclick={handleRestart}
      >
        {t("restart.now")}
      </Button>
    {/if}
  </div>
{/if}
