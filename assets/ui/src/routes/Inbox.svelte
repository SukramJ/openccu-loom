<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InboxDevice, ReplaceCandidate, GroupEntry } from "$lib/api/types";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import RoomFunctionSelect from "$lib/components/RoomFunctionSelect.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import {
    isValidHmIPKeyInput,
    normalizeSgtin,
    stripLabelSeparators,
  } from "$lib/hmip";
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
        pairingTimer = setInterval(() => void load({ silent: true }), 3000);
      }
    } else if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  // The pairing tick must not blank the table: `loading` swaps the whole
  // result for the loading placeholder, so a poll that raises it destroys
  // and recreates the table — including the search input, which loses focus
  // mid-keystroke — roughly twenty times per teach-in window, precisely
  // while the operator is watching for the device they just paired.
  async function load(opts: { silent?: boolean } = {}) {
    if (!opts.silent) loading = true;
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

  // Keyserver-less HmIP LOCAL teach-in: pairing restricted to exactly
  // one device by SGTIN + device key from the label — works without
  // internet/keyserver access. Only offered on HmIP interfaces; the
  // daemon re-normalises both inputs authoritatively (incl. the Base32
  // label-form key conversion).
  let localSgtin = $state("");
  let localKey = $state("");
  let localBusy = $state(false);
  const selectedIsHmIP = $derived(selectedInterface.startsWith("HmIP"));
  const selectedIsWired = $derived(selectedInterface === "BidCos-Wired");
  let searchingWired = $state(false);
  async function searchWiredBus() {
    searchingWired = true;
    try {
      const r = await api.searchWiredDevices(
        selectedInterface,
        centralFilter || undefined,
      );
      toastStore.success(t("inbox.search_wired_done", { count: r.found }));
      // Give ReGa a moment to surface the found (not-yet-accepted)
      // devices in the inbox, then refetch.
      setTimeout(() => void load(), 1500);
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      searchingWired = false;
    }
  }
  const localSgtinInvalid = $derived(
    localSgtin.trim() !== "" && normalizeSgtin(localSgtin) === null,
  );
  const localKeyInvalid = $derived(
    localKey.trim() !== "" && !isValidHmIPKeyInput(localKey),
  );
  async function startLocalTeachIn() {
    const sgtin = normalizeSgtin(localSgtin);
    if (!sgtin || !isValidHmIPKeyInput(localKey)) return;
    localBusy = true;
    try {
      await api.setInstallModeInterface(selectedInterface, true, 60, undefined, {
        sgtin,
        key: stripLabelSeparators(localKey),
      });
      toastStore.success(t("inbox.install_mode_local_started"));
      localSgtin = "";
      localKey = "";
      void installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      localBusy = false;
    }
  }

  // Accept dialog — first-time configuration (name, rooms, functions)
  // applied right after the CCU accepts the device out of the inbox.
  // A null target means the dialog is closed; leaving every field empty
  // and confirming performs a plain accept.
  let acceptTarget = $state<{ address: string; central: string } | null>(null);
  let acceptName = $state("");
  let acceptIncludeChannels = $state(false);
  let acceptRooms = $state<Set<string>>(new Set());
  let acceptFunctions = $state<Set<string>>(new Set());
  let acceptSubmitting = $state(false);
  // GR05: optionally assign the accepted device to a heating group.
  let acceptGroups = $state<GroupEntry[]>([]);
  let acceptGroupId = $state<number | "">("");

  // Replace dialog — swap a paired device for the new (inbox) one.
  // Mirrors the CCU WebUI: the action lives on the new device's row and
  // is offered only for BidCos interfaces (HmIP cannot be replaced).
  let replaceTarget = $state<{ address: string; central: string } | null>(
    null,
  );
  let replaceCandidates = $state<ReplaceCandidate[]>([]);
  let replaceLoading = $state(false);
  let replaceLoadError = $state<string | null>(null);
  let replaceSubmitting = $state(false);

  // Focus-trap bookkeeping for the two hand-rolled dialogs, mirroring
  // ConfirmDialog.svelte. Without it focus stays on the row button that
  // opened the overlay: assistive technology never enters the aria-modal
  // dialog, a keyboard user has to tab through the table behind it, and the
  // overlay's own Escape handler is unreachable because the keydown never
  // travels through the overlay's subtree. Only one of the two can be open
  // at a time, so a single window handler dispatches to whichever it is.
  let acceptDialogEl = $state<HTMLDivElement | null>(null);
  let replaceDialogEl = $state<HTMLDivElement | null>(null);
  let dialogOpener: HTMLElement | null = null;

  function dialogFocusables(el: HTMLDivElement | null): HTMLElement[] {
    if (!el) return [];
    return Array.from(
      el.querySelectorAll<HTMLElement>(
        'input, button, select, textarea, [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((e) => !e.hasAttribute("disabled"));
  }

  $effect(() => {
    if (!acceptTarget && !replaceTarget) return;
    dialogOpener = document.activeElement as HTMLElement | null;
    // The dialog's DOM is inserted by the {#if} this effect depends on;
    // queue past the current microtask so Svelte has committed it.
    queueMicrotask(() =>
      dialogFocusables(acceptTarget ? acceptDialogEl : replaceDialogEl)[0]?.focus(),
    );
    return () => {
      dialogOpener?.focus();
      dialogOpener = null;
    };
  });

  function onDialogKey(e: KeyboardEvent) {
    const busy = acceptTarget ? acceptSubmitting : replaceSubmitting;
    const el = acceptTarget
      ? acceptDialogEl
      : replaceTarget
        ? replaceDialogEl
        : null;
    if (!el) return;
    if (e.key === "Escape") {
      if (busy) return;
      e.preventDefault();
      if (acceptTarget) closeAccept();
      else closeReplace();
      return;
    }
    if (e.key === "Tab") {
      const els = dialogFocusables(el);
      if (els.length === 0) return;
      const first = els[0];
      const last = els[els.length - 1];
      const active = document.activeElement;
      const atEdge = e.shiftKey ? active === first : active === last;
      const outside = !els.includes(active as HTMLElement);
      if (atEdge || outside) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      }
    }
  }

  function isReplaceable(d: InboxDevice): boolean {
    // The CCU exposes replaceDevice on BidCos only; HmIP throws
    // NotImplementedException, so hide the action there (the server
    // still enforces it). An unknown interface stays hidden.
    return d.interface === "BidCos-RF" || d.interface === "BidCos-Wired";
  }

  async function openReplace(address: string, central: string) {
    replaceTarget = { address, central };
    replaceCandidates = [];
    replaceLoadError = null;
    replaceLoading = true;
    try {
      replaceCandidates = await api.listReplaceCandidates(
        address,
        central || undefined,
      );
    } catch (err) {
      replaceLoadError =
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err);
    } finally {
      replaceLoading = false;
    }
  }

  function closeReplace() {
    replaceTarget = null;
  }

  async function confirmReplace(candidate: ReplaceCandidate) {
    if (!replaceTarget) return;
    const target = replaceTarget;
    const ok = await confirmStore.ask({
      title: t("inbox.replace.confirm_title"),
      body: t("inbox.replace.confirm_text", {
        old: candidate.name || candidate.address,
        new: target.address,
      }),
      confirmLabel: t("inbox.replace.confirm_label"),
      destructive: true,
    });
    if (!ok) return;
    replaceSubmitting = true;
    try {
      await api.replaceDevice(
        target.address,
        candidate.address,
        target.central || undefined,
      );
      toastStore.success(t("inbox.replace.success"));
      closeReplace();
      await load();
      installModeStore.refresh();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      replaceSubmitting = false;
    }
  }

  // Room / function catalogues for the multi-selects. Loaded lazily the
  // first time the dialog opens; a load failure leaves the lists empty so
  // the operator can still accept + rename.
  let roomOptions = $state<string[]>([]);
  let functionOptions = $state<string[]>([]);
  let catalogsLoaded = $state(false);

  async function loadCatalogs() {
    if (catalogsLoaded) return;
    try {
      const [rooms, functions] = await Promise.all([
        api.listRooms(),
        api.listFunctions(),
      ]);
      roomOptions = rooms.map((r) => r.name).sort((a, b) => a.localeCompare(b));
      functionOptions = functions
        .map((f) => f.name)
        .sort((a, b) => a.localeCompare(b));
      catalogsLoaded = true;
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${t("inbox.accept_dialog.catalog_error")}`
          : t("inbox.accept_dialog.catalog_error"),
      );
    }
  }

  function openAccept(addr: string, central: string) {
    acceptTarget = { address: addr, central };
    acceptName = "";
    acceptIncludeChannels = false;
    acceptRooms = new Set();
    acceptFunctions = new Set();
    acceptGroups = [];
    acceptGroupId = "";
    void loadCatalogs();
    void loadAcceptGroups(central);
  }

  // GR05: load the target central's heating groups so the accept dialog can
  // offer one. A failure leaves the picker empty (assignment stays optional).
  async function loadAcceptGroups(central: string) {
    try {
      const entries = await api.getGroups(central);
      acceptGroups = entries.flatMap((e) => e.groups);
    } catch {
      acceptGroups = [];
    }
  }

  // Add the just-accepted device to the chosen group: find the device's
  // channels that are assignable to the group's type and extend the roster.
  async function assignAcceptedToGroup(deviceAddress: string, central: string) {
    if (acceptGroupId === "") return;
    const g = acceptGroups.find((x) => x.id === acceptGroupId);
    if (!g) return;
    const suitable = await api.groupSuitableMembers(g.type_id, central);
    const channels = suitable.assignable
      .map((m) => m.address)
      .filter((a) => a.startsWith(deviceAddress + ":"));
    if (channels.length === 0) {
      toastStore.error(t("inbox.group_assign.no_channel"));
      return;
    }
    const members = [
      ...new Set([...(g.members ?? []).map((m) => m.address), ...channels]),
    ];
    await api.updateGroup(
      g.id,
      {
        name: g.name,
        forbid_single_operation: g.forbid_single_operation ?? false,
        members,
      },
      central,
    );
    toastStore.success(t("inbox.group_assign.done", { group: g.name }));
  }

  function closeAccept() {
    acceptTarget = null;
  }

  // The combobox may create a brand-new CCU room / function on the spot;
  // append it to the catalogue so it renders immediately as selected.
  // The CCU refuses the write on a duplicate name, a missing permission or
  // an unreachable ReGa, and the combobox discards the returned promise, so
  // a rejection that is not caught here reaches nothing at all — the chip
  // never appears and the operator is told neither that it worked nor that
  // it failed.
  async function createRoomOption(name: string) {
    try {
      await api.createRoom(name, acceptTarget?.central);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
      throw err;
    }
    if (!roomOptions.includes(name))
      roomOptions = [...roomOptions, name].sort((a, b) => a.localeCompare(b));
    toastStore.success(t("roomfn.created.room"));
  }
  async function createFunctionOption(name: string) {
    try {
      await api.createFunction(name, acceptTarget?.central);
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
      throw err;
    }
    if (!functionOptions.includes(name))
      functionOptions = [...functionOptions, name].sort((a, b) =>
        a.localeCompare(b),
      );
    toastStore.success(t("roomfn.created.function"));
  }

  async function confirmAccept() {
    if (!acceptTarget) return;
    const { address, central } = acceptTarget;
    const name = acceptName.trim();
    const rooms = Array.from(acceptRooms);
    const functions = Array.from(acceptFunctions);
    // Build a config object carrying only the fields the operator set,
    // so an untouched field stays untouched on the CCU.
    const config: {
      name?: string;
      include_channels?: boolean;
      rooms?: string[];
      functions?: string[];
    } = {};
    if (name) {
      config.name = name;
      if (acceptIncludeChannels) config.include_channels = true;
    }
    if (rooms.length > 0) config.rooms = rooms;
    if (functions.length > 0) config.functions = functions;

    accepting = address;
    acceptSubmitting = true;
    try {
      await api.acceptInboxDevice(
        address,
        central,
        Object.keys(config).length > 0 ? config : undefined,
      );
      toastStore.success(t("inbox.accepted", { name: name || address }));
      // GR05: optional heating-group assignment. Best-effort — the device is
      // already accepted, so a group-assign failure only warns.
      if (acceptGroupId !== "") {
        try {
          await assignAcceptedToGroup(address, central);
        } catch (err) {
          toastStore.error(
            err instanceof ApiError
              ? `${err.status}: ${err.message}`
              : t("inbox.group_assign.failed"),
          );
        }
      }
      acceptTarget = null;
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
      acceptSubmitting = false;
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

<svelte:window onkeydown={onDialogKey} />

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader title={t("inbox.title")} subtitle={t("inbox.subtitle")}>
    {#snippet actions()}
      {#if installModeStore.banner && !installModeStore.active}
        <span class="text-xs text-slate-500 dark:text-slate-400">{installModeStore.banner}</span>
      {/if}
      {#if centrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...centrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
      {#if installModeStore.interfaces.length > 0}
        <Select
          class="w-auto"
          bind:value={selectedInterface}
          options={installModeStore.interfaces.map((iface) => ({
            value: iface.interface,
            label: `${iface.interface}${iface.active ? " ●" : ""}`,
          }))}
        />
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

  {#if selectedIsHmIP}
    <!-- Keyserver-less HmIP LOCAL teach-in (SGTIN + device key). -->
    <form
      class="mb-4 flex flex-wrap items-center gap-2"
      onsubmit={(e) => {
        e.preventDefault();
        void startLocalTeachIn();
      }}
    >
      <label class="text-xs text-slate-500 dark:text-slate-400" for="inbox-local-sgtin">
        {t("inbox.install_mode_local_label")}
      </label>
      <input
        id="inbox-local-sgtin"
        type="text"
        bind:value={localSgtin}
        placeholder={t("inbox.install_mode_local_sgtin_placeholder")}
        aria-label={t("inbox.install_mode_local_sgtin_label")}
        class="w-64 rounded-md border px-2 py-2 font-mono text-sm shadow-sm focus:outline-none {localSgtinInvalid
          ? 'border-red-400 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-200'
          : 'border-slate-300 bg-white focus:border-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100'}"
        disabled={localBusy}
      />
      <input
        id="inbox-local-key"
        type="text"
        bind:value={localKey}
        placeholder={t("inbox.install_mode_local_key_placeholder")}
        aria-label={t("inbox.install_mode_local_key_label")}
        class="w-64 rounded-md border px-2 py-2 font-mono text-sm shadow-sm focus:outline-none {localKeyInvalid
          ? 'border-red-400 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950 dark:text-red-200'
          : 'border-slate-300 bg-white focus:border-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100'}"
        disabled={localBusy}
      />
      <Button
        type="submit"
        variant="outline"
        disabled={localBusy ||
          normalizeSgtin(localSgtin) === null ||
          localKey.trim() === "" ||
          !isValidHmIPKeyInput(localKey)}
      >
        {t("inbox.install_mode_local_submit")}
      </Button>
      <span class="w-full text-xs text-slate-400 dark:text-slate-500 sm:w-auto">
        {t("inbox.install_mode_local_hint")}
      </span>
    </form>
  {/if}

  {#if selectedIsWired}
    <!-- BidCos-Wired: scan the bus for new devices (no pairing window). -->
    <div class="mb-4 flex flex-wrap items-center gap-2">
      <Button
        type="button"
        variant="outline"
        onclick={() => void searchWiredBus()}
        disabled={searchingWired}
        title={t("inbox.search_wired_title")}
      >
        {searchingWired ? t("inbox.search_wired_running") : t("inbox.search_wired")}
      </Button>
      <span class="text-xs text-slate-400 dark:text-slate-500">
        {t("inbox.search_wired_hint")}
      </span>
    </div>
  {/if}

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
            {#if d.pending_creation}
              <!-- The daemon parked this device (delay_new_device_creation):
                   it has no data points here until it is accepted. -->
              <Badge variant="warning" title={t("inbox.pending_creation_hint")}>
                {t("inbox.pending_creation_badge")}
              </Badge>
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
              onclick={() => openAccept(d.address, d.central ?? "")}
              disabled={accepting === d.address}
            >
              {accepting === d.address ? "…" : t("inbox.accept")}
            </Button>
            {#if isReplaceable(d)}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void openReplace(d.address, d.central ?? "")}
              >
                {t("inbox.replace.button")}
              </Button>
            {/if}
          {/if}
        {/snippet}
      </DataTable>
    </Card>
  {/if}
</section>

{#if acceptTarget}
  <!-- Accept dialog: optional first-time configuration before the device
       joins the running registry. Confirming with everything blank is a
       plain accept. -->
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={t("inbox.accept_dialog.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget && !acceptSubmitting) closeAccept();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape" && !acceptSubmitting) closeAccept();
    }}
  >
    <div
      bind:this={acceptDialogEl}
      class="max-h-[90vh] w-full max-w-lg overflow-y-auto p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-1 text-lg font-semibold">{t("inbox.accept_dialog.title")}</h2>
      <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
        {t("inbox.accept_dialog.subtitle", { address: acceptTarget.address })}
      </p>

      <form
        onsubmit={(e) => {
          e.preventDefault();
          void confirmAccept();
        }}
      >
        <div class="mb-4">
          <label
            class="mb-1 block text-sm font-medium"
            for="accept-name"
          >
            {t("inbox.accept_dialog.name_label")}
          </label>
          <Input
            id="accept-name"
            bind:value={acceptName}
            placeholder={t("inbox.accept_dialog.name_placeholder")}
            disabled={acceptSubmitting}
          />
          <label
            class="mt-2 flex items-center gap-2 text-sm"
            class:opacity-50={acceptName.trim() === ""}
          >
            <input
              type="checkbox"
              bind:checked={acceptIncludeChannels}
              disabled={acceptSubmitting || acceptName.trim() === ""}
              class="h-4 w-4 rounded border-[var(--ha-divider-color)] text-brand-600 focus:ring-brand-500"
            />
            {t("inbox.accept_dialog.include_channels")}
          </label>
        </div>

        <div class="mb-4">
          <span class="mb-1 block text-sm font-medium">{t("inbox.accept_dialog.rooms_label")}</span>
          <RoomFunctionSelect
            id="inbox-rooms"
            ariaLabel={t("inbox.accept_dialog.rooms_label")}
            selected={Array.from(acceptRooms)}
            options={roomOptions}
            onChange={(next) => (acceptRooms = new Set(next))}
            onCreate={createRoomOption}
            placeholder={t("roomfn.placeholder.room")}
            createLabel={(v) => t("roomfn.create.room", { name: v })}
            removeLabel={(n) => t("roomfn.remove_named", { name: n })}
            disabled={acceptSubmitting}
          />
        </div>

        <div class="mb-5">
          <span class="mb-1 block text-sm font-medium">{t("inbox.accept_dialog.functions_label")}</span>
          <RoomFunctionSelect
            id="inbox-functions"
            ariaLabel={t("inbox.accept_dialog.functions_label")}
            selected={Array.from(acceptFunctions)}
            options={functionOptions}
            onChange={(next) => (acceptFunctions = new Set(next))}
            onCreate={createFunctionOption}
            placeholder={t("roomfn.placeholder.function")}
            createLabel={(v) => t("roomfn.create.function", { name: v })}
            removeLabel={(n) => t("roomfn.remove_named", { name: n })}
            disabled={acceptSubmitting}
          />
        </div>

        {#if acceptGroups.length > 0}
          <div class="mb-5">
            <label class="mb-1 block text-sm font-medium" for="inbox-group">
              {t("inbox.accept_dialog.group_label")}
            </label>
            <select
              id="inbox-group"
              class="h-10 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)]"
              disabled={acceptSubmitting}
              value={acceptGroupId === "" ? "" : String(acceptGroupId)}
              onchange={(e) => {
                const v = (e.currentTarget as HTMLSelectElement).value;
                acceptGroupId = v === "" ? "" : Number(v);
              }}
            >
              <option value="">{t("inbox.accept_dialog.group_none")}</option>
              {#each acceptGroups as g (g.id)}
                <option value={String(g.id)}>{g.name}</option>
              {/each}
            </select>
            <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
              {t("inbox.accept_dialog.group_hint")}
            </p>
          </div>
        {/if}

        <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button
            type="button"
            variant="outline"
            class="w-full sm:w-auto"
            onclick={closeAccept}
            disabled={acceptSubmitting}
          >
            {t("common.cancel")}
          </Button>
          <Button type="submit" class="w-full sm:w-auto" disabled={acceptSubmitting}>
            {acceptSubmitting ? "…" : t("inbox.accept_dialog.submit")}
          </Button>
        </div>
      </form>
    </div>
  </div>
{/if}

{#if replaceTarget}
  <!-- Replace dialog: pick the paired device the new device replaces.
       The CCU migrates links / teams / ReGa references; the old device
       is unpaired. -->
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    aria-label={t("inbox.replace.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget && !replaceSubmitting) closeReplace();
    }}
    onkeydown={(e) => {
      if (e.key === "Escape" && !replaceSubmitting) closeReplace();
    }}
  >
    <div
      bind:this={replaceDialogEl}
      class="max-h-[90vh] w-full max-w-lg overflow-y-auto p-5"
      style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
    >
      <h2 class="mb-1 text-lg font-semibold">{t("inbox.replace.title")}</h2>
      <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
        {t("inbox.replace.intro", { address: replaceTarget.address })}
      </p>

      {#if replaceLoading}
        <LoadingState />
      {:else if replaceLoadError}
        <ErrorState
          message={replaceLoadError}
          onRetry={() =>
            void openReplace(replaceTarget!.address, replaceTarget!.central)}
        />
      {:else if replaceCandidates.length === 0}
        <EmptyState
          message={t("inbox.replace.empty")}
          description={t("inbox.replace.empty_description")}
        />
      {:else}
        <ul class="flex flex-col gap-2">
          {#each replaceCandidates as candidate (candidate.address)}
            <li>
              <button
                type="button"
                class="flex w-full items-center justify-between gap-3 rounded-md border border-[var(--ha-divider-color)] p-3 text-left transition hover:bg-[var(--ha-secondary-background-color)] disabled:opacity-50"
                disabled={replaceSubmitting}
                onclick={() => void confirmReplace(candidate)}
              >
                <span class="min-w-0">
                  <span class="block truncate font-medium">
                    {candidate.name || candidate.address}
                  </span>
                  <span
                    class="block truncate text-xs"
                    style="color: var(--ha-secondary-text-color);"
                  >
                    <span class="font-mono">{candidate.address}</span>
                    {#if candidate.model}· {candidate.model}{/if}
                  </span>
                </span>
                <Badge variant={candidate.model_matches ? "success" : "muted"}>
                  {candidate.model_matches
                    ? t("inbox.replace.same_type")
                    : t("inbox.replace.compatible_type")}
                </Badge>
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      <div class="mt-4 flex justify-end">
        <Button
          type="button"
          variant="outline"
          onclick={closeReplace}
          disabled={replaceSubmitting}
        >
          {t("common.cancel")}
        </Button>
      </div>
    </div>
  </div>
{/if}
