<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { CentralRow } from "$lib/api/client";
  import type { BackupEntry } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let backups = $state<BackupEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let triggering = $state(false);
  let restoring = $state<string | null>(null);

  // Centrals feed the trigger-target picker. With a single registered
  // central the picker is hidden and every trigger uses the
  // backward-compatible unscoped default (first/only central); with
  // several, the operator must pick one explicitly — see B2 (ADR 0002).
  let centrals = $state<CentralRow[]>([]);
  let triggerCentral = $state("");

  async function loadCentrals() {
    try {
      centrals = await api.listCentralsV2();
      if (!triggerCentral && centrals.length > 0) {
        triggerCentral = centrals[0].name;
      }
    } catch {
      // Non-fatal: the trigger button still works unscoped, and the
      // backup list itself surfaces its own load error below.
      centrals = [];
    }
  }

  const centralOptions = $derived(
    centrals.map((c) => ({ value: c.name, label: c.name })),
  );

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

  let uploading = $state(false);
  let fileInput = $state<HTMLInputElement | null>(null);

  // Importing an archive taken elsewhere. The daemon inspects it before
  // storing, so a wrong file is refused here rather than at restore time
  // when the CCU is already being wiped.
  async function onFilePicked(ev: Event) {
    const input = ev.target as HTMLInputElement;
    const file = input.files?.[0];
    // Clear immediately so picking the same file twice still fires.
    input.value = "";
    if (!file) return;
    uploading = true;
    try {
      const entry = await api.uploadBackup(file);
      toastStore.success(
        entry.firmware_version
          ? t("backup.uploaded_with_version", { id: entry.id, version: entry.firmware_version })
          : t("backup.uploaded", { id: entry.id }),
      );
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      uploading = false;
    }
  }

  async function trigger() {
    triggering = true;
    try {
      const { id } = await api.triggerBackup(
        centrals.length > 1 ? triggerCentral : undefined,
      );
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

  onMount(() => {
    void load();
    void loadCentrals();
  });

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

  // Reuse existing backup.col.* i18n keys.
  const columns: DataColumn<BackupEntry>[] = $derived([
    {
      key: "created",
      label: t("backup.col.created"),
      sortable: true,
      title: true,
      get: (e) => e.created_at,
    },
    {
      key: "central",
      label: t("backup.col.central"),
      sortable: true,
      get: (e) => e.central,
    },
    {
      key: "size",
      label: t("backup.col.size"),
      sortable: true,
      align: "right",
      get: (e) => e.bytes,
    },
    {
      key: "id",
      label: t("backup.col.id"),
      sortable: true,
      get: (e) => e.id,
    },
    {
      key: "action",
      label: t("backup.col.action"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader title={t("backup.title")} subtitle={t("backup.subtitle")}>
    {#snippet actions()}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      {#if centrals.length > 1}
        <label class="flex flex-col gap-1 text-xs">
          <span class="text-[var(--ha-secondary-text-color)]">{t("backup.trigger_central")}</span>
          <Select options={centralOptions} bind:value={triggerCentral} class="w-40" />
        </label>
      {/if}
      <input
        bind:this={fileInput}
        type="file"
        accept=".sbk"
        class="hidden"
        onchange={(ev) => void onFilePicked(ev)}
      />
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => fileInput?.click()}
        disabled={uploading}
        title={t("backup.upload.help")}
      >
        {uploading ? t("backup.uploading") : t("backup.upload")}
      </Button>
      <Button type="button" size="sm" onclick={() => void trigger()} disabled={triggering}>
        {triggering ? t("backup.triggering") : t("backup.trigger")}
      </Button>
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={backups}
        {columns}
        rowKey={(e) => e.id}
        search
        searchPlaceholder={t("common.search")}
        persistKey="backups"
        initialSort={{ key: "created", asc: false }}
        emptyMessage={t("backup.empty")}
        emptyIcon="mdi:download"
      >
        {#snippet cell(entry, col)}
          {#if col.key === "created"}
            <span class="font-medium">{formatDate(entry.created_at)}</span>
          {:else if col.key === "central"}
            <Badge variant="muted">{entry.central}</Badge>
          {:else if col.key === "size"}
            <span class="font-mono text-xs">{formatBytes(entry.bytes)}</span>
          {:else if col.key === "id"}
            <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{entry.id}</span>
          {:else if col.key === "action"}
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
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>
