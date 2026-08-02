<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError, friendlyError } from "$lib/api/client";
  import type {
    CentralBehavior,
    CentralRow,
    DescriptionMarker,
    DiscoveredCCU,
    InterfaceSpec,
  } from "$lib/api/client";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import ExpertGate from "$lib/components/ui/ExpertGate.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  // Catalogue of Homematic interfaces the daemon knows how to talk
  // to. Default ports mirror pkg/hmenum/ports.go (DetectionPorts +
  // DefaultBINRPCPort). The CCU dialog renders one row per entry as
  // a checkbox + optional per-interface port override.
  type InterfaceSlot = {
    name: string;
    defaultPort: number;
    rpcType: "xmlrpc" | "binrpc";
  };

  // HmIP-Wired devices share the HmIP-RF XML-RPC proxy on the CCU,
  // they are NOT a separate transport. Listing it here would
  // duplicate the same port and confuse operators.
  const INTERFACE_CATALOGUE: InterfaceSlot[] = [
    { name: "HmIP-RF", defaultPort: 2010, rpcType: "xmlrpc" },
    { name: "BidCos-RF", defaultPort: 2001, rpcType: "xmlrpc" },
    { name: "BidCos-Wired", defaultPort: 2000, rpcType: "xmlrpc" },
    { name: "VirtualDevices", defaultPort: 9292, rpcType: "xmlrpc" },
    { name: "CUxD", defaultPort: 8701, rpcType: "binrpc" },
  ];

  // Per-interface modal state: same length as INTERFACE_CATALOGUE,
  // indexed positionally so checkbox/port-override rows stay
  // aligned with the catalogue.
  type InterfaceFormRow = {
    checked: boolean;
    // portOverride is "" when the user accepts the default; a
    // string holding a positive integer otherwise. We keep this as
    // a string so the controlled <input type="number"> behaves
    // naturally (empty = unset, no NaN dance).
    portOverride: string;
  };

  function freshInterfaceForm(): InterfaceFormRow[] {
    return INTERFACE_CATALOGUE.map((slot) => ({
      checked: slot.name === "HmIP-RF",
      portOverride: "",
    }));
  }

  // Per-central behaviour toggles. The form always holds resolved
  // values (defaulting); buildBehavior emits the explicit object.
  type BehaviorForm = {
    lightLastBrightness: boolean;
    useGroupChannelForCoverState: boolean;
    enableSysvarScan: boolean;
    enableProgramScan: boolean;
    includeInternalSysvars: boolean;
    includeInternalPrograms: boolean;
    enableDeviceFirmwareCheck: boolean;
    delayNewDeviceCreation: boolean;
    sysvarMarkers: DescriptionMarker[];
    programMarkers: DescriptionMarker[];
    // 0 = daemon default (5m). Shown to the operator in seconds.
    sysvarScanIntervalSec: number;
  };

  // Markers a system variable may carry. HAHM is sysvar-only: it makes the
  // variable writable, and a program has no value to write — so the program
  // list below deliberately omits it. MQTT is omitted from both: it steers
  // an MQTT hand-off the reference stack needs and this daemon does not,
  // since its own bridge publishes every hub entity regardless.
  const SYSVAR_MARKERS: DescriptionMarker[] = ["HAHM", "HX", "INTERNAL"];
  const PROGRAM_MARKERS: DescriptionMarker[] = ["HX", "INTERNAL"];
  const NS_PER_SEC = 1_000_000_000;

  // Per-marker explanation shown next to each checkbox. INTERNAL means the
  // same thing for both lists but lands very differently — the CCU flags
  // most ordinary programs as internal and hardly any variables — so a
  // scoped text wins over the shared one where it exists.
  // Markers whose effect reads differently per list. Listing them beats
  // probing for a scoped key: a lookup miss is indistinguishable from a
  // key that echoes itself.
  const SCOPED_MARKERS: DescriptionMarker[] = ["INTERNAL"];

  function markerHelp(m: DescriptionMarker, scope: "sysvar" | "program"): string {
    const base = `centrals.behavior.marker.${m.toLowerCase()}`;
    return t(SCOPED_MARKERS.includes(m) ? `${base}.${scope}` : base);
  }

  function freshBehaviorForm(): BehaviorForm {
    return {
      lightLastBrightness: true,
      useGroupChannelForCoverState: true,
      enableSysvarScan: true,
      enableProgramScan: true,
      includeInternalSysvars: true,
      includeInternalPrograms: false,
      enableDeviceFirmwareCheck: true,
      delayNewDeviceCreation: false,
      sysvarMarkers: [],
      programMarkers: [],
      sysvarScanIntervalSec: 0,
    };
  }

  function behaviorFromRow(b: CentralBehavior | undefined): BehaviorForm {
    const d = freshBehaviorForm();
    if (!b) return d;
    return {
      lightLastBrightness: b.light_last_brightness ?? d.lightLastBrightness,
      useGroupChannelForCoverState:
        b.use_group_channel_for_cover_state ?? d.useGroupChannelForCoverState,
      enableSysvarScan: b.enable_sysvar_scan ?? d.enableSysvarScan,
      enableProgramScan: b.enable_program_scan ?? d.enableProgramScan,
      includeInternalSysvars: b.include_internal_sysvars ?? d.includeInternalSysvars,
      includeInternalPrograms: b.include_internal_programs ?? d.includeInternalPrograms,
      enableDeviceFirmwareCheck:
        b.enable_device_firmware_check ?? d.enableDeviceFirmwareCheck,
      delayNewDeviceCreation: b.delay_new_device_creation ?? d.delayNewDeviceCreation,
      // Drop markers the lists no longer offer, so what is shown is what gets
      // saved rather than an invisible leftover.
      sysvarMarkers: (b.sysvar_markers ?? []).filter((m) => SYSVAR_MARKERS.includes(m)),
      programMarkers: (b.program_markers ?? []).filter((m) => PROGRAM_MARKERS.includes(m)),
      sysvarScanIntervalSec: b.sysvar_scan_interval
        ? Math.round(b.sysvar_scan_interval / NS_PER_SEC)
        : 0,
    };
  }

  function buildBehavior(b: BehaviorForm): CentralBehavior {
    return {
      light_last_brightness: b.lightLastBrightness,
      use_group_channel_for_cover_state: b.useGroupChannelForCoverState,
      enable_sysvar_scan: b.enableSysvarScan,
      enable_program_scan: b.enableProgramScan,
      include_internal_sysvars: b.includeInternalSysvars,
      include_internal_programs: b.includeInternalPrograms,
      enable_device_firmware_check: b.enableDeviceFirmwareCheck,
      delay_new_device_creation: b.delayNewDeviceCreation,
      sysvar_markers: b.sysvarMarkers,
      program_markers: b.programMarkers,
      sysvar_scan_interval:
        b.sysvarScanIntervalSec > 0 ? b.sysvarScanIntervalSec * NS_PER_SEC : undefined,
    };
  }

  function toggleMarker(list: DescriptionMarker[], m: DescriptionMarker): DescriptionMarker[] {
    return list.includes(m) ? list.filter((x) => x !== m) : [...list, m];
  }

  let centrals = $state<CentralRow[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  let discovered = $state<DiscoveredCCU[]>([]);
  let discoveredLoading = $state(false);
  let discoveredError = $state<string | null>(null);

  // Modal state (used for both Add and Edit)
  let showModal = $state(false);
  let isEdit = $state(false);
  let saving = $state(false);
  let modalError = $state<string | null>(null);
  // Snapshot of the row as loaded when an Edit was opened — kept so
  // saveModal can tell "nothing southbound-relevant changed" apart from
  // "the running connection cannot pick this up without a restart"
  // (see centralNeedsRestartSignal below). null outside an edit flow.
  let editOriginal = $state<CentralRow | null>(null);

  // Form fields
  let fName = $state("");
  let fHost = $state("");
  // Discovery serial — carried through adoption so the central is matched by
  // serial later, not by its (mutable) host. Empty for manual entries.
  let fSerial = $state("");
  let fEnabled = $state(true);
  let fTls = $state(false);
  let fTlsInsecure = $state(false);
  let fUsername = $state("");
  // Two parallel password channels — only one is meaningful at a
  // time. The basic form binds to `fPassword` (plain) and writes
  // it to `password_plain`. The expert form additionally exposes
  // `fPasswordEnv` for operators who want the daemon to resolve
  // the value from an env variable. When both are set, the env
  // reference wins at runtime (the daemon checks env first); the
  // SPA mirrors that by treating env as an override.
  let fPassword = $state("");
  let fPasswordEnv = $state("");
  // JSON-RPC (ReGa/hub) port override. Empty = use the CCU default
  // derived from the TLS toggle (443 with TLS, 80 without). Stored in
  // CentralRow.json_rpc_port.
  let fJsonRpcPort = $state("");
  let fPrimaryInterface = $state("");
  let fInterfaces = $state<InterfaceFormRow[]>(freshInterfaceForm());
  let fBehavior = $state<BehaviorForm>(freshBehaviorForm());
  let showBehavior = $state(false);

  // Derived: the catalogue names whose checkbox is currently
  // checked. Drives the "Primary interface" dropdown so operators
  // cannot accidentally pick an unselected one.
  const selectedNames = $derived(
    INTERFACE_CATALOGUE.filter((_, i) => fInterfaces[i]?.checked).map((s) => s.name),
  );

  async function load() {
    loading = true;
    loadError = null;
    try {
      centrals = await api.listCentralsV2();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function loadDiscovered() {
    discoveredLoading = true;
    discoveredError = null;
    try {
      discovered = await api.listDiscoveredCentrals();
    } catch (err) {
      discoveredError = friendlyError(err, t);
    } finally {
      discoveredLoading = false;
    }
  }

  function prefillFromDiscovered(ccu: DiscoveredCCU) {
    isEdit = false;
    editOriginal = null;
    fName = ccu.name;
    // Prefer the daemon's suggested address (localhost for a co-located CCU, a
    // stable docker hostname for an HA add-on); fall back to the raw host.
    fHost = ccu.suggested_host || ccu.host;
    fSerial = ccu.serial;
    fEnabled = true;
    fTls = false;
    fTlsInsecure = false;
    fUsername = "";
    fPassword = "";
    fPasswordEnv = "";
    fJsonRpcPort = "";
    fPrimaryInterface = "HmIP-RF";
    fInterfaces = freshInterfaceForm();
    fBehavior = freshBehaviorForm();
    showBehavior = false;
    modalError = null;
    showModal = true;
  }

  async function ignoreDiscovered(ccu: DiscoveredCCU) {
    const ok = await confirmStore.ask({
      title: t("discovery.ignore"),
      body: t("discovery.ignore_confirm", { name: ccu.name, serial: ccu.serial }),
      confirmLabel: t("discovery.ignore"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.ignoreDiscoveredCentral(ccu.serial);
      toastStore.success(t("discovery.ignored", { name: ccu.name }));
      await loadDiscovered();
    } catch (err) {
      toastStore.error(friendlyError(err, t));
    }
  }

  onMount(() => { void load(); void loadDiscovered(); });

  function openAdd() {
    isEdit = false;
    editOriginal = null;
    fName = "";
    fHost = "";
    fSerial = "";
    fEnabled = true;
    fTls = false;
    fTlsInsecure = false;
    fUsername = "";
    fPassword = "";
    fPasswordEnv = "";
    fJsonRpcPort = "";
    fPrimaryInterface = "HmIP-RF";
    fInterfaces = freshInterfaceForm();
    fBehavior = freshBehaviorForm();
    showBehavior = false;
    modalError = null;
    showModal = true;
  }

  function openEdit(row: CentralRow) {
    isEdit = true;
    editOriginal = row;
    fName = row.name;
    fHost = row.host;
    fSerial = row.serial ?? "";
    fEnabled = row.enabled;
    fTls = row.tls ?? false;
    fTlsInsecure = row.tls_insecure_skip_verify ?? false;
    fUsername = row.username ?? "";
    fPassword = row.password_plain ?? "";
    fPasswordEnv = row.password_env ?? "";
    fJsonRpcPort = row.json_rpc_port ? String(row.json_rpc_port) : "";
    fPrimaryInterface = row.primary_interface ?? "";
    fBehavior = behaviorFromRow(row.behavior);
    showBehavior = false;
    // Re-build per-interface form rows by aligning the incoming
    // row.interfaces against the catalogue. Catalogue order wins;
    // unknown interface names from the server are appended below
    // the catalogue so we never silently drop data we cannot
    // render in the standard list.
    const incoming = new Map(row.interfaces.map((i) => [i.name, i]));
    fInterfaces = INTERFACE_CATALOGUE.map((slot) => {
      const found = incoming.get(slot.name);
      if (!found) return { checked: false, portOverride: "" };
      return {
        checked: true,
        portOverride:
          found.port && found.port !== slot.defaultPort
            ? String(found.port)
            : "",
      };
    });
    modalError = null;
    showModal = true;
  }

  function buildInterfaces(): InterfaceSpec[] {
    const out: InterfaceSpec[] = [];
    for (let i = 0; i < INTERFACE_CATALOGUE.length; i += 1) {
      const row = fInterfaces[i];
      if (!row?.checked) continue;
      const slot = INTERFACE_CATALOGUE[i];
      const spec: InterfaceSpec = { name: slot.name };
      const trimmed = row.portOverride.trim();
      if (trimmed !== "") {
        const port = Number.parseInt(trimmed, 10);
        if (Number.isFinite(port) && port > 0) spec.port = port;
      }
      out.push(spec);
    }
    return out;
  }

  function buildRow(): CentralRow {
    const jsonRpcTrimmed = fJsonRpcPort.trim();
    let jsonRpcPort: number | undefined;
    if (jsonRpcTrimmed !== "") {
      const n = Number.parseInt(jsonRpcTrimmed, 10);
      if (Number.isFinite(n) && n > 0) jsonRpcPort = n;
    }
    return {
      name: fName,
      host: fHost,
      serial: fSerial || undefined,
      enabled: fEnabled,
      json_rpc_port: jsonRpcPort,
      tls: fTls || undefined,
      tls_insecure_skip_verify: fTlsInsecure || undefined,
      username: fUsername || undefined,
      password_plain: fPassword || undefined,
      password_env: fPasswordEnv || undefined,
      primary_interface: fPrimaryInterface || undefined,
      interfaces: buildInterfaces(),
      behavior: buildBehavior(fBehavior),
    };
  }

  // interfaceSetKey normalises an interface list to a sorted, order-independent
  // key so a reload that happens to reorder entries doesn't read as a change,
  // and resolves an omitted port to the catalogue default so "no override"
  // and "override equal to the default" compare equal too.
  function interfaceSetKey(list: InterfaceSpec[]): string {
    return [...list]
      .map((i) => {
        const port = i.port ?? INTERFACE_CATALOGUE.find((s) => s.name === i.name)?.defaultPort ?? 0;
        return `${i.name}:${port}`;
      })
      .sort()
      .join("|");
  }

  // centralNeedsRestartSignal mirrors centralConfigNeedsRestart
  // (cmd/openccu-loom/central_adopt.go): the fields the running southbound
  // connection only reads once, at adopt time. Editing any of them on an
  // already-enabled CCU cannot be hot-applied — the daemon keeps the OLD
  // connection running until restarted — so a save that changes one of
  // these must not claim an unqualified "updated" success.
  function centralNeedsRestartSignal(before: CentralRow, after: CentralRow): boolean {
    return (
      before.host !== after.host ||
      (before.json_rpc_port ?? 0) !== (after.json_rpc_port ?? 0) ||
      (before.tls ?? false) !== (after.tls ?? false) ||
      (before.tls_insecure_skip_verify ?? false) !== (after.tls_insecure_skip_verify ?? false) ||
      (before.username ?? "") !== (after.username ?? "") ||
      (before.password_plain ?? "") !== (after.password_plain ?? "") ||
      (before.password_env ?? "") !== (after.password_env ?? "") ||
      (before.primary_interface ?? "") !== (after.primary_interface ?? "") ||
      interfaceSetKey(before.interfaces) !== interfaceSetKey(after.interfaces)
    );
  }

  async function saveModal() {
    if (buildInterfaces().length === 0) {
      modalError = t("centrals.error.no_interface");
      return;
    }
    saving = true;
    modalError = null;
    try {
      const row = buildRow();
      if (isEdit) {
        await api.updateCentralV2(row.name, row);
        const before = editOriginal;
        const needsRestart =
          !!before && before.enabled && row.enabled && centralNeedsRestartSignal(before, row);
        if (needsRestart) {
          toastStore.warn(t("centrals.updated_restart_required"));
        } else {
          toastStore.success(t("centrals.updated"));
        }
      } else {
        await api.createCentralV2(row);
        toastStore.success(t("centrals.created"));
      }
      showModal = false;
      await load();
    } catch (err) {
      modalError = err instanceof ApiError ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function toggleEnabled(row: CentralRow) {
    try {
      await api.updateCentralV2(row.name, { ...row, enabled: !row.enabled });
      toastStore.success(!row.enabled ? t("centrals.enabled") : t("centrals.disabled"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function deleteCentral(name: string) {
    const ok = await confirmStore.ask({
      title: t("centrals.confirm_delete_title"),
      body: t("centrals.confirm_delete_body", { name }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteCentralV2(name);
      toastStore.success(t("centrals.deleted"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  const columns: DataColumn<CentralRow>[] = $derived([
    { key: "name", label: t("centrals.col.name"), sortable: true, title: true, get: (c) => c.name },
    { key: "host", label: t("centrals.col.host"), sortable: true, get: (c) => c.host },
    { key: "status", label: t("centrals.col.status"), sortable: true, get: (c) => (c.enabled ? 1 : 0) },
    { key: "actions", label: t("centrals.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);
</script>

<div class="space-y-4">
  <!-- Discovered CCUs section -->
  <div class="space-y-3">
    <div class="flex items-center justify-between gap-2">
      <h3 class="text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
        {t("discovery.title")}
      </h3>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onclick={() => void loadDiscovered()}
        disabled={discoveredLoading}
      >
        {t("discovery.refresh")}
      </Button>
    </div>

    {#if discoveredLoading}
      <LoadingState />
    {:else if discoveredError}
      <ErrorState message={discoveredError} onRetry={() => void loadDiscovered()} />
    {:else if discovered.length === 0}
      <EmptyState message={t("discovery.empty")} icon="mdi:radio-tower" />
    {:else}
      <div class="space-y-2">
        {#each discovered as ccu (ccu.serial)}
          <Card class="flex flex-wrap items-center gap-3 px-4 py-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium text-sm">{ccu.name}</span>
                {#if ccu.already_configured}
                  <Badge variant="success">{t("discovery.already_configured")}</Badge>
                {/if}
                {#if ccu.manufacturer || ccu.model}
                  <Badge variant="muted">{[ccu.manufacturer, ccu.model].filter(Boolean).join(" ")}</Badge>
                {/if}
              </div>
              <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{ccu.host}</span>
            </div>
            {#if !ccu.already_configured}
              <div class="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => prefillFromDiscovered(ccu)}
                >
                  {t("discovery.add")}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onclick={() => void ignoreDiscovered(ccu)}
                >
                  {t("discovery.ignore")}
                </Button>
              </div>
            {/if}
          </Card>
        {/each}
      </div>
    {/if}
  </div>

  <hr class="border-slate-200 dark:border-slate-700" />

  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
      {t("settings.tab.ccus")}
    </h3>
    <Button type="button" variant="outline" size="sm" onclick={openAdd}>
      {t("centrals.add")}
    </Button>
  </div>

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if loadError}
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {loadError}</p>
  {:else}
    <DataTable
      rows={centrals}
      {columns}
      rowKey={(c) => c.name}
      search
      searchPlaceholder={t("common.search")}
      persistKey="centrals-admin"
      initialSort={{ key: "name", asc: true }}
      emptyMessage={t("centrals.empty")}
      emptyIcon="mdi:server"
    >
      {#snippet cell(c, col)}
        {#if col.key === "name"}
          <span class="font-medium">{c.name}</span>
          {#if c.interfaces.length > 0}
            <div class="mt-1 flex flex-wrap gap-1">
              {#each c.interfaces as iface (iface.name)}
                <Badge variant="muted">
                  {iface.name}{iface.port ? `:${iface.port}` : ""}
                </Badge>
              {/each}
            </div>
          {/if}
        {:else if col.key === "host"}
          <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{c.host}</span>
        {:else if col.key === "status"}
          <Badge variant={c.enabled ? "success" : "muted"}>
            {c.enabled ? t("settings.enabled") : t("common.disable")}
          </Badge>
        {:else if col.key === "actions"}
          <div class="flex justify-end gap-1">
            <Button type="button" variant="ghost" size="sm" onclick={() => openEdit(c)}>
              {t("common.edit")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onclick={() => void toggleEnabled(c)}
            >
              {c.enabled ? t("common.disable") : t("common.enable")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              class="text-red-600 hover:text-red-700 dark:text-red-400"
              onclick={() => void deleteCentral(c.name)}
            >
              {t("common.delete")}
            </Button>
          </div>
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</div>

<!-- Add / Edit modal -->
{#if showModal}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    onclick={(e) => { if (e.target === e.currentTarget) showModal = false; }}
    onkeydown={(e) => { if (e.key === "Escape") showModal = false; }}
    tabindex="-1"
  >
    <div class="max-h-[90dvh] w-full max-w-2xl overflow-y-auto rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-4 text-base font-semibold">
        {isEdit ? t("centrals.edit_title") : t("centrals.add_title")}
      </h2>

      <div class="space-y-4 text-sm">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label class="flex flex-col gap-1">
            <span>{t("centrals.field.name")} *</span>
            <input
              type="text"
              bind:value={fName}
              disabled={isEdit}
              class="h-9 rounded border border-slate-300 px-3 text-sm disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900"
            />
          </label>
          <label class="flex flex-col gap-1">
            <span>{t("centrals.field.host")} *</span>
            <input
              type="text"
              bind:value={fHost}
              class="h-9 rounded border border-slate-300 px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
          </label>
          <label class="flex flex-col gap-1">
            <span>{t("centrals.field.json_rpc_port")}</span>
            <input
              type="text"
              inputmode="numeric"
              pattern="[0-9]*"
              bind:value={fJsonRpcPort}
              placeholder={fTls ? "443" : "80"}
              class="h-9 rounded border border-slate-300 px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
            <span class="text-xs text-[var(--ha-secondary-text-color)]"
              >{t("centrals.field.json_rpc_port_hint")}</span
            >
          </label>
        </div>

        <fieldset class="space-y-2 rounded border border-slate-200 p-3 dark:border-slate-700">
          <legend class="px-1 text-xs font-medium text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
            {t("centrals.field.interfaces")}
          </legend>
          {#each INTERFACE_CATALOGUE as slot, i (slot.name)}
            <div class="flex items-center gap-3">
              <label class="flex flex-1 items-center gap-2">
                <input
                  type="checkbox"
                  bind:checked={fInterfaces[i].checked}
                  class="h-4 w-4"
                />
                <span class="font-medium">{slot.name}</span>
                <span class="text-xs text-[var(--ha-secondary-text-color)]">
                  ({slot.rpcType})
                </span>
              </label>
              <label class="flex items-center gap-2 text-xs">
                <span class="text-[var(--ha-secondary-text-color)]">
                  {t("centrals.field.port")}
                </span>
                <input
                  type="text"
                  inputmode="numeric"
                  pattern="[0-9]*"
                  bind:value={fInterfaces[i].portOverride}
                  disabled={!fInterfaces[i].checked}
                  placeholder={String(slot.defaultPort)}
                  class="h-7 w-24 rounded border border-slate-300 px-2 text-xs disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900"
                />
              </label>
            </div>
          {/each}
          <p class="pt-1 text-xs text-[var(--ha-secondary-text-color)]">
            {t("centrals.field.port_hint")}
          </p>
        </fieldset>

        <label class="flex flex-col gap-1">
          <span>{t("centrals.field.primary_interface")}</span>
          <Select
            class="h-9"
            bind:value={fPrimaryInterface}
            disabled={selectedNames.length === 0}
            options={[
              { value: "", label: "—" },
              ...selectedNames.map((name) => ({ value: name, label: name })),
            ]}
          />
        </label>

        <!-- Per-central behaviour toggles (CentralConfig.Behavior). -->
        <div class="rounded border border-slate-200 dark:border-slate-700">
          <button
            type="button"
            class="flex w-full items-center justify-between px-3 py-2 text-left text-sm font-medium"
            onclick={() => (showBehavior = !showBehavior)}
          >
            <span>{t("centrals.behavior.title")}</span>
            <span class="text-slate-400">{showBehavior ? "−" : "+"}</span>
          </button>
          {#if showBehavior}
            <div class="flex flex-col gap-2 border-t border-slate-200 px-3 py-3 text-sm dark:border-slate-700">
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.lightLastBrightness} />
                <span>{t("centrals.behavior.light_last_brightness")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.useGroupChannelForCoverState} />
                <span>{t("centrals.behavior.use_group_channel_for_cover_state")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.enableSysvarScan} />
                <span>{t("centrals.behavior.enable_sysvar_scan")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.enableProgramScan} />
                <span>{t("centrals.behavior.enable_program_scan")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.includeInternalSysvars} />
                <span>{t("centrals.behavior.include_internal_sysvars")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.includeInternalPrograms} />
                <span>{t("centrals.behavior.include_internal_programs")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.enableDeviceFirmwareCheck} />
                <span>{t("centrals.behavior.enable_device_firmware_check")}</span>
              </label>
              <label class="flex items-center gap-2">
                <input type="checkbox" bind:checked={fBehavior.delayNewDeviceCreation} />
                <span>{t("centrals.behavior.delay_new_device_creation")}</span>
              </label>

              <label class="flex flex-col gap-1">
                <span>{t("centrals.behavior.sysvar_scan_interval")}</span>
                <input
                  type="number"
                  min="0"
                  bind:value={fBehavior.sysvarScanIntervalSec}
                  class="h-9 w-32 rounded border border-slate-300 px-2 text-sm dark:border-slate-700 dark:bg-slate-900"
                />
              </label>

              <div class="flex flex-col gap-1">
                <span>{t("centrals.behavior.sysvar_markers")}</span>
                <p class="text-xs text-slate-600 dark:text-slate-400">
                  {t("centrals.behavior.markers_hint")}
                </p>
                <div class="flex flex-col gap-1.5">
                  {#each SYSVAR_MARKERS as m (m)}
                    <label class="flex items-start gap-2">
                      <input
                        type="checkbox"
                        class="mt-0.5"
                        checked={fBehavior.sysvarMarkers.includes(m)}
                        onchange={() =>
                          (fBehavior.sysvarMarkers = toggleMarker(fBehavior.sysvarMarkers, m))}
                      />
                      <span>
                        <code
                          class="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">{m}</code
                        >
                        <span class="text-xs text-slate-600 dark:text-slate-400"
                          >{markerHelp(m, "sysvar")}</span
                        >
                      </span>
                    </label>
                  {/each}
                </div>
              </div>

              <div class="flex flex-col gap-1">
                <span>{t("centrals.behavior.program_markers")}</span>
                <p class="text-xs text-slate-600 dark:text-slate-400">
                  {t("centrals.behavior.markers_hint")}
                </p>
                <div class="flex flex-col gap-1.5">
                  {#each PROGRAM_MARKERS as m (m)}
                    <label class="flex items-start gap-2">
                      <input
                        type="checkbox"
                        class="mt-0.5"
                        checked={fBehavior.programMarkers.includes(m)}
                        onchange={() =>
                          (fBehavior.programMarkers = toggleMarker(fBehavior.programMarkers, m))}
                      />
                      <span>
                        <code
                          class="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">{m}</code
                        >
                        <span class="text-xs text-slate-600 dark:text-slate-400"
                          >{markerHelp(m, "program")}</span
                        >
                      </span>
                    </label>
                  {/each}
                </div>
              </div>
            </div>
          {/if}
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label class="flex flex-col gap-1">
            <span>{t("centrals.field.username")}</span>
            <input
              type="text"
              bind:value={fUsername}
              autocomplete="off"
              class="h-9 rounded border border-slate-300 px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
            />
          </label>
          <label class="flex flex-col gap-1">
            <span>{t("centrals.field.password")}</span>
            <input
              type="password"
              bind:value={fPassword}
              autocomplete="new-password"
              disabled={!!fPasswordEnv}
              placeholder={fPasswordEnv ? t("centrals.field.password_placeholder_env") : ""}
              class="h-9 rounded border border-slate-300 px-3 text-sm disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900"
            />
            <span class="text-xs text-[var(--ha-secondary-text-color)]">
              {t("centrals.field.password_hint")}
            </span>
          </label>
        </div>

        <div class="flex flex-wrap gap-4">
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={fEnabled} class="h-4 w-4" />
            <span>{t("settings.enabled")}</span>
          </label>
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={fTls} class="h-4 w-4" />
            <span>TLS</span>
          </label>
          <label class="flex items-center gap-2">
            <input
              type="checkbox"
              bind:checked={fTlsInsecure}
              disabled={!fTls}
              class="h-4 w-4 disabled:opacity-50"
            />
            <span class:opacity-50={!fTls}>{t("centrals.field.tls_insecure")}</span>
          </label>
        </div>
        {#if fTls && fTlsInsecure}
          <p class="text-xs text-amber-700 dark:text-amber-300">
            {t("centrals.field.tls_insecure_warn")}
          </p>
        {/if}

        <ExpertGate>
          <div class="space-y-3 rounded border border-slate-200 p-3 dark:border-slate-700">
            <p class="text-xs font-medium text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
              {t("settings.expert_mode")}
            </p>
            <label class="flex flex-col gap-1">
              <span>{t("centrals.field.password_env")}</span>
              <input
                type="text"
                bind:value={fPasswordEnv}
                placeholder="CCU_PASSWORD"
                autocomplete="off"
                class="h-9 rounded border border-slate-300 px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
              />
              <span class="text-xs text-[var(--ha-secondary-text-color)]">
                {t("centrals.field.password_env_hint")}
              </span>
            </label>
          </div>
        </ExpertGate>
      </div>

      {#if modalError}
        <p class="mt-3 text-xs text-red-600 dark:text-red-400">{modalError}</p>
      {/if}

      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (showModal = false)}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={saving || !fName || !fHost}
          onclick={() => void saveModal()}
        >
          {saving ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}
