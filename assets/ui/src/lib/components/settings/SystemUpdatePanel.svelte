<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SystemUpdateEntry } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // CCU system (firmware) update panel. Reads GET /system/update per
  // central and — for admins — triggers POST /system/update/install,
  // which kicks off the CCU's own firmware update (the CCU reboots).
  // While an install is in flight the panel polls so the in-progress
  // state and the post-reboot version land without a manual refresh.

  let entries = $state<SystemUpdateEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // The central whose install is currently being triggered (button busy).
  let busy = $state<string | null>(null);

  const isAdmin = $derived(authStore.identity?.role === "admin");

  async function load() {
    error = null;
    try {
      entries = await api.getSystemUpdate();
    } catch (err) {
      // Keep the last known entries during a poll (the CCU may be mid-
      // reboot) — only surface the error, don't blank the list.
      error = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  function stopPoll() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }
  // Poll every 5s while any central reports an install in progress.
  function ensurePoll() {
    if (pollTimer) return;
    const tick = async () => {
      await load();
      if (entries.some((e) => e.in_progress)) {
        pollTimer = setTimeout(tick, 5000);
      } else {
        pollTimer = null;
      }
    };
    pollTimer = setTimeout(tick, 5000);
  }

  async function install(e: SystemUpdateEntry) {
    const central = e.central ?? "";
    const ok = await confirmStore.ask({
      title: t("ccu_update.confirm_title"),
      body: t("ccu_update.confirm_body", { central }),
      confirmLabel: t("ccu_update.install"),
      destructive: true,
    });
    if (!ok) return;
    busy = central;
    try {
      await api.installSystemUpdate(e.central);
      toastStore.success(t("ccu_update.triggered", { central }));
      await load();
      ensurePoll();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      busy = null;
    }
  }

  onMount(async () => {
    await load();
    // An install may already be running (triggered elsewhere) — keep the
    // status live until it settles.
    if (entries.some((e) => e.in_progress)) ensurePoll();
  });
  onDestroy(stopPoll);
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center justify-between gap-2">
    <h3 class="text-sm font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {t("ccu_update.title")}
    </h3>
    <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
      {t("common.reload")}
    </Button>
  </div>
  <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("ccu_update.subtitle")}</p>

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if error && entries.length === 0}
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {error}</p>
  {:else if entries.length === 0}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("ccu_update.empty")}</p>
  {:else}
    <div class="space-y-3">
      {#each entries as e (e.central)}
        <div class="rounded-md bg-[var(--ha-secondary-background-color)] p-3">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">{e.central}</div>
              <div class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">
                {#if !e.observed}
                  {t("ccu_update.not_observed")}
                {:else}
                  <span class="font-mono">{e.current_firmware || "—"}</span>
                  {#if e.update_available && e.available_firmware}
                    <span aria-hidden="true"> → </span>
                    <span class="font-mono text-[var(--ha-primary-text-color)]">{e.available_firmware}</span>
                  {/if}
                {/if}
              </div>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              {#if e.in_progress}
                <Badge variant="warning">{t("ccu_update.in_progress")}</Badge>
              {:else if e.update_available}
                <Badge variant="default">{t("ccu_update.available")}</Badge>
              {:else if e.observed}
                <Badge variant="muted">{t("firmware.up_to_date")}</Badge>
              {/if}
              {#if isAdmin}
                <Button
                  type="button"
                  variant="default"
                  size="sm"
                  disabled={!e.update_available || e.in_progress || busy === e.central}
                  onclick={() => void install(e)}
                >
                  {busy === e.central ? t("ccu_update.installing") : t("ccu_update.install")}
                </Button>
              {/if}
            </div>
          </div>
        </div>
      {/each}
    </div>
    {#if !isAdmin}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("ccu_update.admin_only")}</p>
    {/if}
  {/if}
</div>
