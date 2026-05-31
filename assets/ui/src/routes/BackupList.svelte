<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { BackupEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";

  type Props = { locale: string };
  let { locale }: Props = $props();

  let backups = $state<BackupEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let triggering = $state(false);
  let restoring = $state<string | null>(null);
  let confirmRestore = $state<BackupEntry | null>(null);
  let banner = $state<string | null>(null);

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
    banner = null;
    try {
      const { id } = await api.triggerBackup();
      banner = t("backup.started", { id });
      await load();
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      triggering = false;
    }
  }

  async function restore(entry: BackupEntry) {
    restoring = entry.id;
    banner = null;
    confirmRestore = null;
    try {
      await api.restoreBackup(entry.id);
      banner = t("backup.restore_started", { id: entry.id });
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
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
      return new Date(iso).toLocaleString(locale === "de" ? "de-DE" : "en-US");
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

<section class="mx-auto max-w-6xl px-6 py-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("backup.title")}</h1>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("backup.subtitle")}</p>
    </div>
    <div class="flex items-center gap-2">
      {#if banner}
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</span>
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      <Button type="button" size="sm" onclick={() => void trigger()} disabled={triggering}>
        {triggering ? t("backup.triggering") : t("backup.trigger")}
      </Button>
    </div>
  </header>

  {#if loadError}
    <Card class="mb-4 p-3">
      <p class="text-sm text-red-600 dark:text-red-400">
        {t("common.error")} {loadError}
      </p>
    </Card>
  {/if}

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if sortedBackups.length === 0}
    <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("backup.empty")}
    </Card>
  {:else}
    <Card class="overflow-hidden p-0">
      <table class="w-full text-left text-sm">
        <thead
          class="border-b border-slate-200 bg-slate-50 text-xs uppercase tracking-wide text-slate-500 dark:border-slate-800 dark:bg-slate-900"
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
              <td class="px-4 py-2">{formatDate(entry.created_at)}</td>
              <td class="px-4 py-2">
                <Badge variant="muted">{entry.central}</Badge>
              </td>
              <td class="px-4 py-2 font-mono text-xs">{formatBytes(entry.bytes)}</td>
              <td class="px-4 py-2 font-mono text-xs text-[var(--ha-secondary-text-color)]">{entry.id}</td>
              <td class="px-4 py-2 text-right">
                <div class="flex items-center justify-end gap-2">
                  <a
                    class="text-brand-700 hover:text-brand-800"
                    href={api.backupDownloadUrl(entry.id)}
                    download={`${entry.id}.sbk`}
                  >
                    {t("backup.download")}
                  </a>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => (confirmRestore = entry)}
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

{#if confirmRestore}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("backup.confirm_restore_aria")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) confirmRestore = null;
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") confirmRestore = null;
    }}
  >
    <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900">
      <h2 class="text-lg font-semibold">{t("backup.confirm.title")}</h2>
      <p class="mt-2 text-sm text-slate-600 dark:text-slate-300">
        {t("backup.confirm.body")}
      </p>
      <dl class="mt-3 grid grid-cols-2 gap-1 text-xs text-[var(--ha-secondary-text-color)]">
        <dt>{t("backup.col.id")}</dt>
        <dd class="font-mono">{confirmRestore.id}</dd>
        <dt>{t("backup.col.created")}</dt>
        <dd>{formatDate(confirmRestore.created_at)}</dd>
        <dt>{t("backup.col.central")}</dt>
        <dd>{confirmRestore.central}</dd>
        <dt>{t("backup.col.size")}</dt>
        <dd>{formatBytes(confirmRestore.bytes)}</dd>
      </dl>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (confirmRestore = null)}>
          {t("common.cancel")}
        </Button>
        <Button type="button" size="sm" onclick={() => void restore(confirmRestore!)}>
          {t("common.restore")}
        </Button>
      </div>
    </div>
  </div>
{/if}
