<script lang="ts">
  import { centralStore } from "$lib/stores/centrals.svelte";
  import FeatureGate from "$lib/components/ui/FeatureGate.svelte";
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { CentralRow } from "$lib/api/client";
  import type { BackupEntry, BackupStorageInfo } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import { t } from "$lib/i18n";
  import { centralFeatureReason, featureName, featureReason } from "$lib/features";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let backups = $state<BackupEntry[]>([]);
  // Where the daemon actually writes the archives. On a CCU add-on install
  // this is resolved at every start from the CCU's own backup target, so it
  // is neither the config value nor guessable from the client side — and it
  // is the first thing an operator asks after taking a backup.
  let storage = $state<BackupStorageInfo | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let triggering = $state(false);
  let restoring = $state<string | null>(null);
  let deleting = $state<string | null>(null);

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
        const able = centrals.find((c) => centralStore.offers(c.name, "system.backup.create"));
        triggerCentral = (able ?? centrals[0]).name;
      }
    } catch {
      // Non-fatal: the trigger button still works unscoped, and the
      // backup list itself surfaces its own load error below.
      centrals = [];
    }
  }

  // Only a central that can create a backup is offered as its target.
  const centralOptions = $derived(
    centrals
      .filter((c) => centralStore.offers(c.name, "system.backup.create"))
      .map((c) => ({ value: c.name, label: c.name })),
  );

  const RESTORE = "system.backup.restore";

  // How a restore action is offered. What the system cannot do at all is
  // hidden — no credential will ever make it work. What only the credential
  // lacks is shown disabled with the reason, because the operator can fix
  // that by granting the scope. A central that is merely booting reports
  // "not_ready" and keeps its buttons, so they do not vanish on a restart.
  type RestoreAction =
    | { kind: "enabled" }
    | { kind: "disabled"; reason: string }
    | { kind: "hidden" };

  function restoreActionFor(name: string): RestoreAction {
    const f = centralStore.featureOf(name, RESTORE);
    if (!f || f.available || f.reason === "not_ready") return { kind: "enabled" };
    if (f.reason === "missing_scope") {
      const c = centralStore.byName(name);
      return {
        kind: "disabled",
        reason: t("feature.unavailable", {
          feature: featureName(RESTORE),
          central: name,
          reason: c ? centralFeatureReason(c, RESTORE) : featureReason(f),
        }),
      };
    }
    return { kind: "hidden" };
  }

  // The same question over the fleet, for what has no central of its own:
  // an uploaded archive goes to whichever central the daemon resolves, and
  // the upload is only good for a restore. Any central that can restore
  // enables it; otherwise one that lacks only the scope explains why not.
  // While the fleet is unknown the answer is enabled, so the actions do not
  // blank during the first paint.
  const fleetRestore = $derived.by((): RestoreAction => {
    if (centralStore.items.length === 0) return { kind: "enabled" };
    const actions = centralStore.items.map((c) => restoreActionFor(c.name));
    return (
      actions.find((a) => a.kind === "enabled") ??
      actions.find((a) => a.kind === "disabled") ?? { kind: "hidden" }
    );
  });

  // An archive bound to a central restores there.
  function restoreAction(entry: BackupEntry): RestoreAction {
    return entry.central ? restoreActionFor(entry.central) : fleetRestore;
  }

  // An archive the box served encrypted (".sbk.age"). The daemon cannot
  // open it; only the openccu-lite system holding its key can restore it.
  function isEncrypted(entry: BackupEntry): boolean {
    return (entry.filename ?? "").toLowerCase().endsWith(".age");
  }

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
    try {
      storage = await api.backupStorageInfo();
    } catch {
      // Non-fatal: the list is the page, the location is context. A daemon
      // too old to serve this route simply shows no location row.
      storage = null;
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

  // Deleting an archive. The confirmation names the archive rather than
  // asking about "this backup": the list can hold several per CCU, and the
  // one thing an operator must be sure of before an unrecoverable delete is
  // which one is about to go.
  async function remove(entry: BackupEntry) {
    const name = entry.filename || `${entry.id}.sbk`;
    const ok = await confirmStore.ask({
      title: t("backup.delete_confirm.title"),
      body: t("backup.delete_confirm.body", { name }),
      confirmLabel: t("backup.delete"),
      destructive: true,
    });
    if (!ok) return;
    deleting = entry.id;
    try {
      await api.deleteBackup(entry.id);
      toastStore.success(t("backup.deleted", { id: entry.id }));
      await load();
    } catch (err) {
      toastStore.error(
        t("backup.delete_failed", {
          id: entry.id,
          error:
            err instanceof ApiError
              ? `${err.status}: ${err.message}`
              : err instanceof Error
                ? err.message
                : String(err),
        }),
      );
    } finally {
      deleting = null;
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

<PageShell>
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
        accept=".sbk,.age"
        class="hidden"
        onchange={(ev) => void onFilePicked(ev)}
      />
      <!-- An uploaded archive is only good for a restore, so the upload
           follows the fleet's restore rule. -->
      {#if fleetRestore.kind === "enabled"}
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
      {:else if fleetRestore.kind === "disabled"}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled
          title={fleetRestore.reason}
          aria-describedby="backup-upload-reason"
        >
          {t("backup.upload")}
        </Button>
        <span id="backup-upload-reason" class="sr-only">{fleetRestore.reason}</span>
      {/if}
      {#if centralStore.offers(triggerCentral || undefined, "system.backup.create")}
        <Button type="button" size="sm" onclick={() => void trigger()} disabled={triggering}>
          {triggering ? t("backup.triggering") : t("backup.trigger")}
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  <FeatureGate feature="system.backup.create">
    {#if loadError}
      <ErrorState message={loadError} onRetry={load} class="mb-4" />
    {/if}

    {#if storage}
      <div
        class="mb-4 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs text-[var(--ha-secondary-text-color)]"
        data-testid="backup-storage"
      >
        <span class="font-medium">{t("backup.storage.label")}:</span>
        {#if storage.available}
          <span class="font-mono break-all text-[var(--ha-primary-text-color)]">
            {storage.dir || t("backup.storage.unknown")}
          </span>
          <span aria-hidden="true">·</span>
          <span>
            {t("backup.storage.summary", {
              count: String(storage.count),
              bytes: formatBytes(storage.bytes),
            })}
          </span>
        {:else}
          <span class="text-amber-700 dark:text-amber-400">{t("backup.storage.unavailable")}</span>
        {/if}
      </div>
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
              {#if isEncrypted(entry)}
                <Badge variant="warning" class="ml-2" title={t("backup.encrypted.help")}>
                  {t("backup.encrypted")}
                </Badge>
              {/if}
            {:else if col.key === "central"}
              <Badge variant="muted">{entry.central}</Badge>
            {:else if col.key === "size"}
              <span class="font-mono text-xs">{formatBytes(entry.bytes)}</span>
            {:else if col.key === "id"}
              <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{entry.id}</span>
            {:else if col.key === "action"}
              {@const action = restoreAction(entry)}
              <div class="flex items-center justify-end gap-2">
                <a
                  class="text-brand-700 hover:text-brand-800 dark:text-brand-400 dark:hover:text-brand-300"
                  href={api.backupDownloadUrl(entry.id)}
                  download={entry.filename || `${entry.id}.sbk`}
                >
                  {t("backup.download")}
                </a>
                {#if action.kind === "enabled"}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => void restore(entry)}
                    disabled={restoring === entry.id}
                  >
                    {restoring === entry.id ? "…" : t("common.restore")}
                  </Button>
                {:else if action.kind === "disabled"}
                  <!-- Natively disabled: neither a click nor the keyboard
                       reaches it, and the reason stays readable for both
                       pointer (title) and assistive technology. -->
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled
                    title={action.reason}
                    aria-describedby={`backup-restore-reason-${entry.id}`}
                  >
                    {t("common.restore")}
                  </Button>
                  <span id={`backup-restore-reason-${entry.id}`} class="sr-only">{action.reason}</span>
                {/if}
                <Button
                  type="button"
                  variant="outline-destructive"
                  size="sm"
                  onclick={() => void remove(entry)}
                  disabled={deleting === entry.id}
                >
                  {deleting === entry.id ? t("backup.deleting") : t("backup.delete")}
                </Button>
              </div>
            {/if}
          {/snippet}
        </DataTable>
      </Card>
    {/if}
  </FeatureGate>
</PageShell>
