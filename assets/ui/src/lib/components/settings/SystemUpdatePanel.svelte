<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SystemUpdateEntry } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
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

  // Firmware-download form: an admin supplies an http(s) URL and the CCU
  // fetches the image onto the central so it can be staged for a later
  // install. Optional per-central target for multi-CCU deployments.
  let downloadUrl = $state("");
  let downloadCentral = $state("");
  let downloading = $state(false);

  const centralOptions = $derived(
    entries
      .map((e) => e.central ?? "")
      .filter((c) => c !== "")
      .map((c) => ({ value: c, label: c })),
  );

  // A firmware download targets exactly one CCU. For multi-CCU
  // deployments default the selector to the first central; single-CCU
  // leaves it empty so the backend resolves the sole central.
  $effect(() => {
    if (centralOptions.length > 1 && downloadCentral === "") {
      downloadCentral = centralOptions[0].value;
    }
  });

  async function downloadFirmware() {
    const url = downloadUrl.trim();
    if (!url || downloading) return;
    downloading = true;
    try {
      await api.downloadSystemFirmware(url, downloadCentral || undefined);
      toastStore.success(t("firmware_download.triggered"));
      downloadUrl = "";
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      downloading = false;
    }
  }

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

  // Off by default: a pre-update backup is the safer choice but makes the
  // request block for minutes, so it stays an explicit decision rather
  // than a surprise.
  let backupFirst = $state(false);

  async function install(e: SystemUpdateEntry) {
    const central = e.central ?? "";
    const ok = await confirmStore.ask({
      title: t("ccu_update.confirm_title"),
      body: backupFirst
        ? t("ccu_update.confirm_body_with_backup", { central })
        : t("ccu_update.confirm_body", { central }),
      confirmLabel: t("ccu_update.install"),
      destructive: true,
    });
    if (!ok) return;
    busy = central;
    try {
      await api.installSystemUpdate(e.central, backupFirst);
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
                {#if e.update_available && !e.in_progress}
                  <label class="flex items-center gap-1.5 text-xs" title={t("ccu_update.backup_first.help")}>
                    <input type="checkbox" bind:checked={backupFirst} />
                    <span>{t("ccu_update.backup_first")}</span>
                  </label>
                {/if}
                <Button
                  type="button"
                  variant="default"
                  size="sm"
                  disabled={!e.update_available || e.in_progress || busy === e.central}
                  onclick={() => void install(e)}
                >
                  {busy === e.central
                    ? backupFirst
                      ? t("ccu_update.backing_up")
                      : t("ccu_update.installing")
                    : t("ccu_update.install")}
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

  {#if isAdmin}
    <div class="space-y-2 border-t border-[var(--ha-divider-color)] pt-4">
      <h4 class="text-sm font-semibold">{t("firmware_download.title")}</h4>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("firmware_download.subtitle")}</p>
      <div class="flex flex-wrap items-center gap-2">
        <Input
          class="min-w-0 flex-1"
          type="url"
          bind:value={downloadUrl}
          placeholder={t("firmware_download.url_placeholder")}
          aria-label={t("firmware_download.url_label")}
          disabled={downloading}
        />
        {#if centralOptions.length > 1}
          <Select
            class="h-9 w-auto"
            bind:value={downloadCentral}
            options={centralOptions}
          />
        {/if}
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={downloading || downloadUrl.trim() === ""}
          onclick={() => void downloadFirmware()}
        >
          {downloading ? t("firmware_download.downloading") : t("firmware_download.download")}
        </Button>
      </div>
    </div>
  {/if}
</div>
