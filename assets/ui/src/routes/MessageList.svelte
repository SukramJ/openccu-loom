<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type {
    AlarmMessage,
    ServiceMessage,
    SuppressedServiceMessage,
  } from "$lib/api/types";
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
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let alarms = $state<AlarmMessage[]>([]);
  let services = $state<ServiceMessage[]>([]);
  let suppressed = $state<SuppressedServiceMessage[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let tab = $state<"alarm" | "service" | "suppressed">("alarm");
  let acking = $state<string | null>(null);
  let ackingAll = $state(false);
  let suppressingId = $state<string | null>(null);
  let unsuppressingKey = $state<string | null>(null);

  function suppressedKey(s: SuppressedServiceMessage): string {
    return `${s.central ?? ""}/${s.interface ?? ""}/${s.channel}/${s.parameter ?? ""}`;
  }
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

  // A broadcast-driven refresh must not blank the table: `silent` keeps
  // the rendered rows in place while the new ones are fetched, so a
  // message acknowledged elsewhere updates without the view flashing
  // back to its loading state.
  async function load(opts: { silent?: boolean } = {}) {
    if (!opts.silent) loading = true;
    loadError = null;
    try {
      const [a, s, sup] = await Promise.all([
        api.listAlarmMessages(),
        api.listServiceMessages(),
        api.listSuppressedServices(),
      ]);
      alarms = a;
      services = s;
      suppressed = sup;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function suppressService(s: ServiceMessage) {
    const ok = await confirmStore.ask({
      title: t("messages.suppress.confirm"),
      confirmLabel: t("messages.suppress.button"),
      destructive: true,
    });
    if (!ok) return;
    suppressingId = s.id;
    try {
      await api.disableService(s.id, s.central ?? undefined);
      toastStore.success(t("messages.suppressed"));
      await load();
    } catch (err) {
      ackError(err);
    } finally {
      suppressingId = null;
    }
  }

  async function unsuppressService(s: SuppressedServiceMessage) {
    const ok = await confirmStore.ask({
      title: t("messages.unsuppress.confirm"),
      confirmLabel: t("messages.unsuppress.button"),
      destructive: false,
    });
    if (!ok) return;
    unsuppressingKey = suppressedKey(s);
    try {
      await api.unsuppressService(
        {
          interface: s.interface ?? undefined,
          channel: s.channel,
          parameter: s.parameter ?? undefined,
        },
        s.central ?? undefined,
      );
      toastStore.success(t("messages.unsuppressed"));
      await load();
    } catch (err) {
      ackError(err);
    } finally {
      unsuppressingKey = null;
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

  function ackError(err: unknown): void {
    toastStore.error(
      err instanceof ApiError
        ? `${err.status}: ${err.message}`
        : err instanceof Error
          ? err.message
          : String(err),
    );
  }

  async function ackAllAlarms() {
    const ok = await confirmStore.ask({
      title: t("messages.ack_all.confirm_alarms"),
      confirmLabel: t("messages.ack_all.button"),
      destructive: false,
    });
    if (!ok) return;
    ackingAll = true;
    try {
      const res = await api.ackAllAlarms(centralFilter || undefined);
      toastStore.success(t("messages.ack_all.done", { count: res.acknowledged }));
      await load();
    } catch (err) {
      ackError(err);
    } finally {
      ackingAll = false;
    }
  }

  async function ackAllServices() {
    const ok = await confirmStore.ask({
      title: t("messages.ack_all.confirm_services"),
      confirmLabel: t("messages.ack_all.button"),
      destructive: false,
    });
    if (!ok) return;
    ackingAll = true;
    try {
      const res = await api.ackAllServices(centralFilter || undefined);
      toastStore.success(t("messages.ack_all.done", { count: res.acknowledged }));
      await load();
    } catch (err) {
      ackError(err);
    } finally {
      ackingAll = false;
    }
  }

  // A message acknowledged elsewhere (another tab, the CCU WebUI, a
  // rule) only reaches this view over the hub broadcast; without the
  // subscription the list would keep showing entries that are already
  // gone. The broadcast carries a count, not the rows, so a change
  // triggers a reload rather than a patch.
  let unsubEvents: (() => void) | null = null;
  // The boot snapshot signals a resync instead of replaying the model
  // into the stream, so the view reloads what it read over REST.
  let unsubResync: (() => void) | null = null;
  let reloadTimer: ReturnType<typeof setTimeout> | null = null;

  // Acknowledging in this view already reloads, and the server answers the
  // same action with a broadcast; a multi-central ack-all produces one
  // broadcast per central. Debouncing collapses that burst into a single
  // silent refetch instead of one per event.
  function scheduleReload(): void {
    if (reloadTimer) clearTimeout(reloadTimer);
    reloadTimer = setTimeout(() => {
      reloadTimer = null;
      void load({ silent: true });
    }, 300);
  }

  onMount(() => {
    void load();
    unsubResync = onResync(() => void load());
    unsubEvents = subscribe((ev) => {
      if (ev.type === "hub.service_message" || ev.type === "hub.alarm_message") {
        scheduleReload();
      }
    });
  });

  onDestroy(() => {
    unsubEvents?.();
    unsubResync?.();
    if (reloadTimer) clearTimeout(reloadTimer);
  });

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

  const suppressedCentrals = $derived.by(() => {
    const set = new Set<string>();
    for (const s of suppressed) if (s.central) set.add(s.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const suppressedRows = $derived(
    suppressed.filter((s) => !centralFilter || s.central === centralFilter),
  );

  // Central-filter options for the currently active tab.
  const tabCentrals = $derived(
    tab === "alarm"
      ? alarmCentrals
      : tab === "service"
        ? serviceCentrals
        : suppressedCentrals,
  );

  // The filter is persisted and spans all three tabs, while each tab derives
  // its own central list from its own rows. An active filter therefore
  // regularly names a central the active tab knows nothing about — after a
  // tab switch, after the last message of that central was acknowledged, or
  // after the CCU was renamed. Keep the stored value among the options so
  // the filter never hides every row with no control to clear it.
  function centralOptions(list: string[]): { value: string; label: string }[] {
    const names =
      centralFilter && !list.includes(centralFilter)
        ? [...list, centralFilter]
        : list;
    return [
      { value: "", label: t("common.all_ccus") },
      ...names.map((c) => ({ value: c, label: c })),
    ];
  }

  // Bulk-acknowledge scope: how many messages the "acknowledge all"
  // button would clear given the current central filter (the type /
  // quittable-only view filters do not narrow the CCU-side bulk pass).
  const alarmAckAllCount = $derived(
    alarms.filter((a) => !centralFilter || a.central === centralFilter).length,
  );
  const serviceAckAllCount = $derived(
    services.filter(
      (s) => (!centralFilter || s.central === centralFilter) && s.quittable,
    ).length,
  );

  const alarmColumns: DataColumn<AlarmMessage>[] = $derived([
    {
      key: "name",
      label: t("messages.col.name"),
      sortable: true,
      title: true,
      get: (a) => a.name,
    },
    {
      key: "time",
      label: t("messages.col.time"),
      sortable: true,
      get: (a) => a.timestamp ?? "",
    },
    {
      key: "last_timestamp",
      label: t("messages.col.last_timestamp"),
      sortable: true,
      get: (a) => a.last_timestamp ?? "",
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
      get: (s) => s.timestamp ?? "",
    },
    {
      key: "last_timestamp",
      label: t("messages.col.last_timestamp"),
      sortable: true,
      get: (s) => s.last_timestamp ?? "",
    },
    {
      key: "actions",
      label: t("messages.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);

  const suppressedColumns: DataColumn<SuppressedServiceMessage>[] = $derived([
    {
      key: "parameter",
      label: t("messages.suppressed.col.parameter"),
      sortable: true,
      title: true,
      get: (s) => s.parameter ?? "",
    },
    {
      key: "device",
      label: t("messages.col.device"),
      sortable: true,
      get: (s) => s.device_name ?? "",
    },
    {
      key: "channel",
      label: t("messages.suppressed.col.channel"),
      sortable: true,
      get: (s) => s.channel,
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
      {#if tabCentrals.length > 1 || centralFilter}
        <Select class="w-auto" bind:value={centralFilter} options={centralOptions(tabCentrals)} />
      {/if}
      {#if tab === "alarm" && alarmAckAllCount > 0}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => void ackAllAlarms()}
          disabled={ackingAll || loading}
        >
          {ackingAll ? "…" : t("messages.ack_all.button")}
        </Button>
      {:else if tab === "service" && serviceAckAllCount > 0}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => void ackAllServices()}
          disabled={ackingAll || loading}
        >
          {ackingAll ? "…" : t("messages.ack_all.button")}
        </Button>
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
    <button
      type="button"
      class="border-b-2 px-3 py-2 text-sm transition {tab === 'suppressed'
        ? 'border-brand-500 text-brand-700 dark:text-brand-400'
        : 'border-transparent text-slate-500 hover:text-brand-700 dark:text-slate-400 dark:hover:text-brand-400'}"
      onclick={() => (tab = "suppressed")}
    >
      {t("messages.suppressed.tab")}
      <Badge variant="muted">{suppressed.length}</Badge>
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
        emptyDescription={t("messages.empty.alarms.description")}
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
            {#if a.description}
              <span class="block text-xs text-slate-600 dark:text-slate-300">{a.description}</span>
            {/if}
          {:else if col.key === "time"}
            {#if a.timestamp}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(a.timestamp)}</span>
            {/if}
          {:else if col.key === "last_timestamp"}
            {#if a.last_timestamp}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(a.last_timestamp)}</span>
            {/if}
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
  {:else if tab === "service"}
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
      {#if serviceCentrals.length > 1 || centralFilter}
        <Select class="w-auto" bind:value={centralFilter} options={centralOptions(serviceCentrals)} />
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
        emptyDescription={t("messages.empty.service.description")}
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
            {#if s.rooms && s.rooms.length > 0}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{s.rooms.join(", ")}</span>
            {/if}
            {#if s.functions && s.functions.length > 0}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{s.functions.join(", ")}</span>
            {/if}
          {:else if col.key === "time"}
            {#if s.timestamp}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(s.timestamp)}</span>
            {/if}
          {:else if col.key === "last_timestamp"}
            {#if s.last_timestamp}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(s.last_timestamp)}</span>
            {/if}
          {:else if col.key === "actions"}
            <div class="inline-flex flex-wrap justify-end gap-1">
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
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void suppressService(s)}
                disabled={suppressingId === s.id}
                title={t("messages.suppress")}
              >
                {suppressingId === s.id ? "…" : t("messages.suppress")}
              </Button>
            </div>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {:else}
    <Card class="p-4">
      <DataTable
        rows={suppressedRows}
        columns={suppressedColumns}
        rowKey={(s) => suppressedKey(s)}
        search
        searchPlaceholder={t("common.search")}
        persistKey="messages-suppressed"
        initialSort={{ key: "channel", asc: true }}
        emptyMessage={t("messages.suppressed.empty")}
        emptyDescription={t("messages.suppressed.empty.description")}
        emptyIcon="mdi:bell-off"
      >
        {#snippet cell(s, col)}
          {#if col.key === "parameter"}
            {#if s.parameter}
              <span class="font-mono font-semibold">{s.parameter}</span>
            {:else}
              <span class="italic text-slate-500 dark:text-slate-400"
                >{t("messages.suppressed.all_parameters")}</span
              >
            {/if}
            {#if suppressedCentrals.length > 1 && s.central}
              <Badge variant="muted">{s.central}</Badge>
            {/if}
            {#if s.name}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{s.name}</span>
            {/if}
          {:else if col.key === "device"}
            {#if s.device_name}
              <span class="text-sm">{s.device_name}</span>
            {/if}
            {#if s.interface}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{s.interface}</span>
            {/if}
          {:else if col.key === "channel"}
            <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{s.channel}</span>
          {:else if col.key === "actions"}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void unsuppressService(s)}
              disabled={unsuppressingKey === suppressedKey(s)}
              title={t("messages.unsuppress.button")}
            >
              {unsuppressingKey === suppressedKey(s)
                ? "…"
                : t("messages.unsuppress.button")}
            </Button>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>
