<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { BackupEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let backups = $state<BackupEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let triggering = $state(false);
  let restoring = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      backups = await api.listBackups();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function trigger() {
    triggering = true;
    try {
      const { id } = await api.triggerBackup();
      toastStore.success(t("backup.started", { id }));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      triggering = false;
    }
  }

  async function restore(entry: BackupEntry) {
    const ok = await confirmStore.ask({
      title: t("backup.confirm.title"),
      body: t("backup.confirm.body"),
      confirmLabel: t("common.restore"),
      destructive: true,
    });
    if (!ok) return;
    restoring = entry.id;
    try {
      await api.restoreBackup(entry.id);
      toastStore.success(t("backup.restore_started", { id: entry.id }));
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      restoring = null;
    }
  }

  onMount(load);

  function formatBytes(n: number): string {
    if (n < 1024) return `${n} B`;
    const units = ["KiB", "MiB", "GiB"];
    let i = -1;
    let v = n;
    do {
      v /= 1024;
      i++;
    } while (v >= 1024 && i < units.length - 1);
    return `${v.toFixed(1)} ${units[i]}`;
  }

  function formatDate(iso: string): string {
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  const sortedBackups = $derived(
    [...backups].sort((a, b) =>
      b.created_at.localeCompare(a.created_at),
    ),
  );
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("backup.title")}</h1>
      <p class="text-sm text-slate-500 dark:text-slate-400">{t("backup.subtitle")}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      <Button type="button" size="sm" onclick={() => void trigger()} disabled={triggering}>
        {triggering ? t("backup.triggering") : t("backup.trigger")}
      </Button>
    </div>
  </header>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if sortedBackups.length === 0}
    <EmptyState message={t("backup.empty")} icon="mdi:download" />
  {:else}
    <Card class="overflow-hidden p-0">
      <table class="table-reflow w-full text-left text-sm">
        <thead
          class="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:bg-slate-900 dark:text-slate-400"
        >
          <tr>
            <th class="px-4 py-2">{t("backup.col.created")}</th>
            <th class="px-4 py-2">{t("backup.col.central")}</th>
            <th class="px-4 py-2">{t("backup.col.size")}</th>
            <th class="px-4 py-2">{t("backup.col.id")}</th>
            <th class="px-4 py-2 text-right">{t("backup.col.action")}</th>
          </tr>
        </thead>
        <tbody>
          {#each sortedBackups as entry (entry.id)}
            <tr class="border-b border-slate-100 last:border-0 dark:border-slate-800">
              <td class="reflow-title px-4 py-2">{formatDate(entry.created_at)}</td>
              <td class="px-4 py-2" data-label={t("backup.col.central")}>
                <Badge variant="muted">{entry.central}</Badge>
              </td>
              <td class="px-4 py-2 font-mono text-xs" data-label={t("backup.col.size")}>{formatBytes(entry.bytes)}</td>
              <td class="px-4 py-2 font-mono text-xs text-slate-500 dark:text-slate-400" data-label={t("backup.col.id")}>{entry.id}</td>
              <td class="reflow-actions px-4 py-2">
                <div class="flex items-center justify-end gap-2">
                  <a
                    class="text-brand-700 hover:text-brand-800 dark:text-brand-400 dark:hover:text-brand-300"
                    href={api.backupDownloadUrl(entry.id)}
                    download={`${entry.id}.sbk`}
                  >
                    {t("backup.download")}
                  </a>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => void restore(entry)}
                    disabled={restoring === entry.id}
                  >
                    {restoring === entry.id ? "…" : t("common.restore")}
                  </Button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </Card>
  {/if}
</section>
