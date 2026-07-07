<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { AlarmMessage, ServiceMessage } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  let alarms = $state<AlarmMessage[]>([]);
  let services = $state<ServiceMessage[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let tab = $state<"alarm" | "service">("alarm");
  let acking = $state<string | null>(null);
  let typeFilter = $state<string>("");
  let onlyQuittable = $state(false);
  let centralFilter = $state(loadLS("messages:central"));
  $effect(() => saveLS("messages:central", centralFilter));

  function serviceTypeLabel(typeId: string): string {
    const known = new Set([
      "generic",
      "sticky",
      "config_pending",
      "alarm",
      "update_pending",
      "communication",
    ]);
    return known.has(typeId) ? t(`messages.type.${typeId}`) : typeId;
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      const [a, s] = await Promise.all([
        api.listAlarmMessages(),
        api.listServiceMessages(),
      ]);
      alarms = a;
      services = s;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function ackAlarm(id: string, central?: string) {
    acking = id;
    try {
      await api.ackAlarm(id, central);
      toastStore.success(t("messages.acknowledged"));
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
      acking = null;
    }
  }

  async function ackService(id: string, central?: string) {
    acking = id;
    try {
      await api.ackService(id, central);
      toastStore.success(t("messages.acknowledged"));
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
      acking = null;
    }
  }

  onMount(load);

  function formatDate(iso: string): string {
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  const alarmCentrals = $derived.by(() => {
    const set = new Set<string>();
    for (const a of alarms) if (a.central) set.add(a.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const serviceCentrals = $derived.by(() => {
    const set = new Set<string>();
    for (const s of services) if (s.central) set.add(s.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const alarmRows = $derived(
    alarms.filter((a) => !centralFilter || a.central === centralFilter),
  );

  const serviceRows = $derived(
    services.filter((s) => {
      if (onlyQuittable && !s.quittable) return false;
      if (typeFilter && (s.type ?? "") !== typeFilter) return false;
      if (centralFilter && s.central !== centralFilter) return false;
      return true;
    }),
  );

  const serviceTypes = $derived.by(() => {
    const set = new Set<string>();
    for (const s of services) if (s.type) set.add(s.type);
    return Array.from(set).sort();
  });

  const alarmColumns: DataColumn<AlarmMessage>[] = $derived([
    {
      key: "name",
      label: t("messages.col.name"),
      sortable: true,
      title: true,
      get: (a) => a.name,
    },
    {
      key: "device",
      label: t("messages.col.device"),
      sortable: true,
      get: (a) => a.device_name ?? "",
    },
    {
      key: "time",
      label: t("messages.col.time"),
      sortable: true,
      get: (a) => a.timestamp,
    },
    {
      key: "actions",
      label: t("messages.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);

  const serviceColumns: DataColumn<ServiceMessage>[] = $derived([
    {
      key: "name",
      label: t("messages.col.name"),
      sortable: true,
      title: true,
      get: (s) => s.name,
    },
    {
      key: "type",
      label: t("messages.col.type"),
      sortable: true,
      get: (s) => s.type ?? "",
    },
    {
      key: "device",
      label: t("messages.col.device"),
      sortable: true,
      get: (s) => s.device_name ?? s.address ?? "",
    },
    {
      key: "time",
      label: t("messages.col.time"),
      sortable: true,
      get: (s) => s.timestamp,
    },
    {
      key: "actions",
      label: t("messages.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("messages.title")}
    subtitle={t("messages.summary", { alarms: alarms.length, services: services.length })}
  >
    {#snippet actions()}
      {#if (tab === "alarm" ? alarmCentrals : serviceCentrals).length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...(tab === "alarm" ? alarmCentrals : serviceCentrals).map((c) => ({
              value: c,
              label: c,
            })),
          ]}
        />
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

  <nav class="mb-4 flex gap-1 border-b border-slate-200 dark:border-slate-800">
    <button
      type="button"
      class="border-b-2 px-3 py-2 text-sm transition {tab === 'alarm'
        ? 'border-brand-500 text-brand-700 dark:text-brand-400'
        : 'border-transparent text-slate-500 hover:text-brand-700 dark:text-slate-400 dark:hover:text-brand-400'}"
      onclick={() => (tab = "alarm")}
    >
      {t("messages.alarms")}
      <Badge variant="muted">{alarms.length}</Badge>
    </button>
    <button
      type="button"
      class="border-b-2 px-3 py-2 text-sm transition {tab === 'service'
        ? 'border-brand-500 text-brand-700 dark:text-brand-400'
        : 'border-transparent text-slate-500 hover:text-brand-700 dark:text-slate-400 dark:hover:text-brand-400'}"
      onclick={() => (tab = "service")}
    >
      {t("messages.service")}
      <Badge variant="muted">{services.length}</Badge>
    </button>
  </nav>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if tab === "alarm"}
    <Card class="p-4">
      <DataTable
        rows={alarmRows}
        columns={alarmColumns}
        rowKey={(a) => (a.central ?? "") + "/" + a.id}
        search
        searchPlaceholder={t("common.search")}
        persistKey="messages-alarm"
        initialSort={{ key: "time", asc: false }}
        emptyMessage={t("messages.empty.alarms")}
        emptyIcon="mdi:bell-off"
      >
        {#snippet cell(a, col)}
          {#if col.key === "name"}
            <span class="font-semibold">{a.name}</span>
            {#if a.counter > 1}
              <Badge variant="warning">×{a.counter}</Badge>
            {/if}
            {#if alarmCentrals.length > 1 && a.central}
              <Badge variant="muted">{a.central}</Badge>
            {/if}
            {#if a.last_trigger}
              <span class="block text-xs italic text-slate-500 dark:text-slate-400">
                {t("messages.last_trigger")} {a.last_trigger}
              </span>
            {/if}
            {#if a.description}
              <span class="block text-xs text-slate-600 dark:text-slate-300">{a.description}</span>
            {/if}
          {:else if col.key === "device"}
            {#if a.device_name}
              <span class="text-sm">{a.device_name}</span>
            {/if}
            {#if a.rooms && a.rooms.length > 0}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{a.rooms.join(", ")}</span>
            {/if}
          {:else if col.key === "time"}
            <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(a.timestamp)}</span>
          {:else if col.key === "actions"}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void ackAlarm(a.id, a.central)}
              disabled={acking === a.id}
              title={t("common.acknowledge")}
            >
              {acking === a.id ? "…" : t("common.acknowledge")}
            </Button>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {:else}
    <div class="mb-3 flex flex-wrap items-center gap-2 text-xs">
      <Select
        class="w-auto"
        bind:value={typeFilter}
        options={[
          { value: "", label: t("messages.all_types") },
          ...serviceTypes.map((st) => ({ value: st, label: serviceTypeLabel(st) })),
        ]}
      />
      <label class="flex items-center gap-1 text-slate-600 dark:text-slate-400">
        <input type="checkbox" bind:checked={onlyQuittable} />
        {t("messages.quittable_only")}
      </label>
      {#if serviceCentrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...serviceCentrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
    </div>
    <Card class="p-4">
      <DataTable
        rows={serviceRows}
        columns={serviceColumns}
        rowKey={(s) => (s.central ?? "") + "/" + s.id}
        search
        searchPlaceholder={t("common.search")}
        persistKey="messages-service"
        initialSort={{ key: "time", asc: false }}
        emptyMessage={t("messages.empty.service")}
        emptyIcon="mdi:bell-off"
      >
        {#snippet cell(s, col)}
          {#if col.key === "name"}
            <span class="font-semibold">{s.name}</span>
            {#if s.counter > 1}
              <Badge variant="warning">×{s.counter}</Badge>
            {/if}
            {#if s.quittable}
              <Badge variant="default">{t("messages.ackable")}</Badge>
            {/if}
            {#if serviceCentrals.length > 1 && s.central}
              <Badge variant="muted">{s.central}</Badge>
            {/if}
          {:else if col.key === "type"}
            {#if s.type}
              <Badge variant="muted">{serviceTypeLabel(s.type)}</Badge>
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {:else if col.key === "device"}
            {#if s.device_name}
              <span class="text-sm">{s.device_name}</span>
            {/if}
            {#if s.address}
              <span class="block font-mono text-xs text-slate-500 dark:text-slate-400">{s.address}</span>
            {/if}
          {:else if col.key === "time"}
            <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(s.timestamp)}</span>
          {:else if col.key === "actions"}
            {#if s.quittable}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void ackService(s.id, s.central)}
                disabled={acking === s.id}
                title={t("common.acknowledge")}
              >
                {acking === s.id ? "…" : t("common.acknowledge")}
              </Button>
            {/if}
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>
