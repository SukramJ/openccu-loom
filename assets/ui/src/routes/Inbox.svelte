<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InboxDevice } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  // Inbox of pending pairing candidates. The CCU populates the list
  // through its system-variable feed; this view lets the operator
  // accept devices into the running registry. Mirrors the
  // "Posteingang" panel of the CCU WebUI.

  let entries = $state<InboxDevice[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("inbox:central"));
  $effect(() => saveLS("inbox:central", centralFilter));
  let accepting = $state<string | null>(null);

  // Teach-in scope: install mode on the CCU is per-interface only, so the
  // operator always pairs on a specific radio (BidCos-RF / HmIP-RF, …) —
  // the interface-selective pairing the CCU WebUI offers. selectedInterface
  // defaults to the first available radio (see the $effect below).
  let selectedInterface = $state("");
  // Keep the selection valid: default to the first interface once the list
  // loads, and recover if the selected interface disappears.
  $effect(() => {
    const list = installModeStore.interfaces;
    if (list.length === 0) return;
    if (!list.some((i) => i.interface === selectedInterface)) {
      selectedInterface = list[0].interface;
    }
  });
  const scopeEntry = $derived(
    installModeStore.interfaces.find((i) => i.interface === selectedInterface),
  );
  const scopeActive = $derived(scopeEntry?.active ?? false);
  const scopeRemaining = $derived(scopeEntry?.seconds ?? null);

  // Active-pairing tick: while the install mode is running on the CCU,
  // the inbox should reflect freshly-discovered candidates without the
  // user having to hit reload. 3 s is fast enough that the operator
  // sees the device shortly after pressing the physical pairing
  // button and slow enough to keep CCU pressure low.
  let pairingTimer: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    if (installModeStore.active) {
      if (!pairingTimer) {
        pairingTimer = setInterval(() => void load(), 3000);
      }
    } else if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.listInbox();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  // Targeted teach-in by serial / device address. Opens a pairing
  // window for exactly one device (CCU WebUI "Gerät per Seriennummer
  // anlernen"). The auto-poll on installModeStore.active surfaces the
  // device in the list once it reports in.
  let serial = $state("");
  let pairBusy = $state(false);
  async function pairBySerial() {
    const addr = serial.trim();
    if (!addr) return;
    pairBusy = true;
    try {
      await api.pairDeviceInstallMode(addr, 60);
      toastStore.success(t("inbox.pair_serial_started", { addr }));
      serial = "";
      installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      pairBusy = false;
    }
  }

  async function accept(addr: string, central: string) {
    accepting = addr;
    try {
      await api.acceptInboxDevice(addr, central);
      toastStore.success(t("inbox.accepted", { name: addr }));
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
      accepting = null;
    }
  }

  onMount(() => {
    void load();
    installModeStore.ensurePoll();
  });

  onDestroy(() => {
    if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
    installModeStore.release();
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const d of entries) if (d.central) set.add(d.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const visibleEntries = $derived(
    centralFilter ? entries.filter((d) => d.central === centralFilter) : entries,
  );

  function formatTs(secs: number | undefined): string {
    if (!secs) return "";
    try {
      return new Date(secs * 1000).toLocaleString(
        prefs.locale === "de" ? "de-DE" : "en-US",
      );
    } catch {
      return String(secs);
    }
  }

  const columns: DataColumn<InboxDevice>[] = $derived([
    {
      key: "address",
      label: t("inbox.col.address"),
      sortable: true,
      title: true,
      get: (d) => d.address,
    },
    {
      key: "model",
      label: t("inbox.col.model"),
      sortable: true,
      get: (d) => d.model,
    },
    {
      key: "serial",
      label: t("inbox.col.serial"),
      sortable: true,
      get: (d) => d.serial ?? "",
    },
    {
      key: "first_seen",
      label: t("inbox.col.first_seen"),
      sortable: true,
      get: (d) => d.first_seen ?? 0,
    },
    {
      key: "actions",
      label: t("inbox.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader title={t("inbox.title")} subtitle={t("inbox.subtitle")}>
    {#snippet actions()}
      {#if installModeStore.banner && !installModeStore.active}
        <span class="text-xs text-slate-500 dark:text-slate-400">{installModeStore.banner}</span>
      {/if}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      {#if installModeStore.interfaces.length > 0}
        <select
          bind:value={selectedInterface}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title={t("inbox.install_mode_interface_label")}
        >
          {#each installModeStore.interfaces as iface ((iface.central ?? "") + "/" + iface.interface)}
            <option value={iface.interface}>
              {iface.interface}{iface.active ? " ●" : ""}
            </option>
          {/each}
        </select>
      {/if}
      <Button
        type="button"
        variant={scopeActive ? "default" : "outline"}
        onclick={() => void installModeStore.toggle({ interface: selectedInterface })}
        disabled={installModeStore.busy || installModeStore.interfaces.length === 0}
        title={scopeActive
          ? t("inbox.install_mode_active_title")
          : t("inbox.install_mode_start_title")}
      >
        {#if scopeActive}
          {t("inbox.install_mode_pairing", { seconds: scopeRemaining ?? "…" })}
        {:else}
          {t("inbox.install_mode")}
        {/if}
      </Button>
      <Button type="button" variant="outline" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

  <!-- Targeted teach-in by serial / device address -->
  <form
    class="mb-4 flex flex-wrap items-center gap-2"
    onsubmit={(e) => {
      e.preventDefault();
      void pairBySerial();
    }}
  >
    <label class="text-xs text-slate-500 dark:text-slate-400" for="inbox-serial">
      {t("inbox.pair_serial_label")}
    </label>
    <input
      id="inbox-serial"
      type="text"
      bind:value={serial}
      placeholder={t("inbox.pair_serial_placeholder")}
      class="w-56 rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
      disabled={pairBusy}
    />
    <Button type="submit" variant="outline" disabled={pairBusy || serial.trim() === ""}>
      {t("inbox.pair_serial_submit")}
    </Button>
  </form>

  {#if installModeStore.active}
    <div class="mb-4 flex items-center gap-2 rounded border border-brand-300 bg-brand-50 p-3 text-sm text-brand-900 dark:border-brand-800 dark:bg-brand-950 dark:text-brand-200">
      <Badge variant="default">{t("inbox.install_mode_badge")}</Badge>
      <span>
        {t("inbox.install_mode_running")}
        {#if installModeStore.remainingSeconds !== null}
          · {installModeStore.remainingSeconds}&nbsp;{t("inbox.install_mode_seconds_left")}
        {/if}
      </span>
    </div>
  {/if}

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <Card class="p-4">
      <DataTable
        rows={visibleEntries}
        {columns}
        rowKey={(d) => (d.central ?? "") + "/" + d.address}
        search
        searchPlaceholder={t("common.search")}
        persistKey="inbox"
        initialSort={{ key: "first_seen", asc: false }}
        emptyMessage={t("inbox.empty")}
        emptyIcon="mdi:server"
      >
        {#snippet cell(d, col)}
          {#if col.key === "address"}
            <span class="font-mono font-semibold">{d.address}</span>
            {#if centrals.length > 1 && d.central}
              <Badge variant="muted">{d.central}</Badge>
            {/if}
          {:else if col.key === "model"}
            <Badge variant="muted">{d.model}</Badge>
            {#if d.manufacturer}
              <span class="block text-xs text-slate-500 dark:text-slate-400">{d.manufacturer}</span>
            {/if}
          {:else if col.key === "serial"}
            {#if d.serial}
              <span class="font-mono text-xs">{d.serial}</span>
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {:else if col.key === "first_seen"}
            {#if d.first_seen}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatTs(d.first_seen)}</span>
            {:else}
              <span class="text-slate-400 dark:text-slate-500">—</span>
            {/if}
          {:else if col.key === "actions"}
            <Button
              type="button"
              size="sm"
              onclick={() => void accept(d.address, d.central ?? "")}
              disabled={accepting === d.address}
            >
              {accepting === d.address ? "…" : t("inbox.accept")}
            </Button>
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>
