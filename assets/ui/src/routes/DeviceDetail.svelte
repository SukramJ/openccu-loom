<script lang="ts">
  import { onMount } from "svelte";
  import type { DeviceDetail } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import TeamPicker from "$lib/components/device/TeamPicker.svelte";
  import DeviceLinks from "$lib/components/links/DeviceLinks.svelte";
  import CentralLinksPanel from "$lib/components/links/CentralLinksPanel.svelte";
  import CdpTilesPanel from "$lib/cdp/CdpTilesPanel.svelte";
  import ScheduleTab from "$lib/components/schedule/ScheduleTab.svelte";
  import MaintenanceStatusGrid from "$lib/components/device/MaintenanceStatusGrid.svelte";
  import AuditLog from "./AuditLog.svelte";
  import HistoryChart from "$lib/components/HistoryChart.svelte";
  import RecordToggle from "$lib/components/RecordToggle.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Breadcrumb from "$lib/components/ui/Breadcrumb.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { maintenanceStore } from "$lib/stores/maintenance.svelte";
  import { favoritesStore } from "$lib/stores/favorites.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";

  type Props = {
    address: string;
    channel?: number;
    locale: string;
  };

  let { address, channel, locale }: Props = $props();

  let detail = $state<DeviceDetail | null>(null);
  let error = $state<string | null>(null);

  // Top-level tabs.
  //   • overview  = Maintenance + CDP-Tiles + read-only sensor cards
  //   • configure = MASTER paramset + Links + Schedule (sub-tabbed)
  //   • history   = device-scoped audit log
  type TopTab = "overview" | "configure" | "history";
  let topTab = $state<TopTab>("overview");

  // Sub-tabs inside `configure`. WEEK_PROFILE channels redirect from
  // the channels strip to `schedule`, so users never end up in an
  // empty MASTER editor for a profile-only channel.
  type ConfigSub =
    | "device-config"
    | "channels"
    | "links"
    | "schedule";
  let configSub = $state<ConfigSub>("channels");

  // Probe whether the device exposes a climate schedule. Drives
  // whether we render the schedule sub-tab in `configure`.
  let scheduleSupported = $state<boolean | null>(null);

  // Measurement history: channel+parameter selector state.
  // Populated lazily when the user opens the history tab.
  let historyChannelNo = $state<number | null>(null);
  let historyParameter = $state<string | null>(null);
  let historyDPs = $state<import("$lib/api/types").DataPointSummary[]>([]);
  let historyDPsLoading = $state(false);

  async function probeSchedule() {
    if (!detail) {
      scheduleSupported = null;
      return;
    }
    try {
      await api.getDeviceSchedule(detail.address);
      scheduleSupported = true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        scheduleSupported = false;
      } else {
        scheduleSupported = null;
      }
    }
  }

  // Load data points for the history measurement chart when the
  // history tab opens. We pick the first user channel with numeric DPs.
  async function loadHistoryDPs(channelNo: number) {
    if (!detail) return;
    historyDPsLoading = true;
    historyDPs = [];
    historyParameter = null;
    try {
      const dps = await api.listDataPoints(detail.address, channelNo);
      // Only numeric parameters (FLOAT / INTEGER) make sense to chart.
      historyDPs = dps.filter(
        (dp) => dp.type === "FLOAT" || dp.type === "INTEGER",
      );
      if (historyDPs.length > 0) {
        historyParameter = historyDPs[0].parameter;
      }
    } catch {
      historyDPs = [];
    } finally {
      historyDPsLoading = false;
    }
  }

  function onHistoryTabClick() {
    if (!detail) return;
    // Pick the first user-facing channel as the default for history.
    const firstCh = userChannels[0];
    if (!firstCh) return;
    const no = firstCh.number;
    if (historyChannelNo !== no) {
      historyChannelNo = no;
      void loadHistoryDPs(no);
    }
  }

  // Rename / delete / firmware workflow state.
  let renaming = $state(false);
  let renameValue = $state("");
  let renameBusy = $state(false);
  // Whether the device rename also cascades to every channel
  // ("<name>:<channelNo>"). Default on, matching the CCU WebUI.
  let renameIncludeChannels = $state(true);

  // Per-channel rename workflow state. renameChannelNo is the channel
  // number currently being edited (null = no dialog open).
  let renameChannelNo = $state<number | null>(null);
  let renameChannelValue = $state("");
  let renameChannelBusy = $state(false);
  let deleting = $state(false);
  let updatingFw = $state(false);
  let restoringConfig = $state(false);
  let testingComm = $state(false);
  let commTestResult = $state<import("$lib/api/types").CommunicationTestResult | null>(null);
  let exportingDef = $state(false);

  // Delete-with-options dialog. The plain confirm becomes a small options
  // dialog: a mode radio (plain unpair / factory reset) plus a "force
  // removal" checkbox for unreachable devices. Before the dialog is usable
  // the direct links and programs that reference the device are loaded so a
  // dependency warning can be shown.
  let deleteDialogOpen = $state(false);
  let deleteMode = $state<"unpair" | "reset">("unpair");
  let deleteForce = $state(false);
  let deleteDepsLoading = $state(false);
  let deleteLinkCount = $state(0);
  let deleteProgramNames = $state<string[]>([]);
  const deleteHasDeps = $derived(
    deleteLinkCount > 0 || deleteProgramNames.length > 0,
  );

  let editingRooms = $state(false);
  let roomsDraft = $state("");
  let roomsBusy = $state(false);

  let editingFunctions = $state(false);
  let functionsDraft = $state("");
  let functionsBusy = $state(false);

  let editingChannelRooms = $state(false);
  let channelRoomsDraft = $state("");
  let channelRoomsBusy = $state(false);

  let editingChannelFunctions = $state(false);
  let channelFunctionsDraft = $state("");
  let channelFunctionsBusy = $state(false);

  async function load() {
    error = null;
    try {
      detail = await api.getDevice(address);
      void probeSchedule();
    } catch (err) {
      error =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    }
  }

  onMount(load);

  onMount(() => {
    if (!favoritesStore.loaded) void favoritesStore.load();
  });

  // Pin / unpin this device as a favorite. The label is cached so the
  // favorites view renders without a re-fetch.
  async function togglePin(label: string) {
    try {
      const pinned = await favoritesStore.toggle({
        type: "device",
        id: address,
        label,
      });
      toastStore.success(
        pinned ? t("favorites.added", { label }) : t("favorites.removed", { label }),
      );
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }

  // Channels arrive sorted by string from the REST handler; resort
  // numerically and strip channels that exist purely for week-profile
  // storage (the schedule sub-tab handles those device-scoped).
  const visibleChannels = $derived.by(() => {
    if (!detail) return [];
    return [...detail.channels]
      .filter((ch) => !(ch.type ?? "").toUpperCase().startsWith("WEEK_PROFILE"))
      .sort((a, b) => a.number - b.number);
  });

  // The "device-level" channel is the one without a colon in its
  // address (port of HMIP-local-frontend's `deviceChannel` lookup).
  // It groups truly device-scoped MASTER parameters (e.g. global
  // pairing settings).
  const deviceChannel = $derived.by(() => {
    if (!detail) return null;
    return detail.channels.find((c) => !c.address.includes(":")) ?? null;
  });

  // Channel `:0` carries the maintenance VALUES (RSSI, LOW_BAT, …) +
  // the device-wide MASTER. Used by the Status tab.
  const channelZero = $derived.by(() => {
    if (!detail) return null;
    return detail.channels.find((c) => c.address.endsWith(":0")) ?? null;
  });

  const selectedChannel = $derived(
    channel ?? visibleChannels[0]?.number ?? 0,
  );

  // Skip ":0" and the device-level channel from the user-facing
  // channel strip — those have their own dedicated cards.
  const userChannels = $derived(
    visibleChannels.filter(
      (c) => c.address.includes(":") && !c.address.endsWith(":0"),
    ),
  );

  function isVirtualChannel(no: number): boolean {
    return no >= 50;
  }

  function isWeekProfileChannel(type: string | undefined): boolean {
    return (type ?? "").toUpperCase().endsWith("WEEK_PROFILE");
  }

  function startRename() {
    renameValue = detail?.name ?? "";
    renameIncludeChannels = true;
    renaming = true;
  }

  async function commitRename() {
    if (!detail) return;
    const next = renameValue.trim();
    if (!next || next === detail.name) {
      renaming = false;
      return;
    }
    renameBusy = true;
    try {
      await api.renameDevice(address, next, renameIncludeChannels);
      toastStore.success(t("device.renamed"));
      renaming = false;
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      renameBusy = false;
    }
  }

  function startRenameChannel(no: number, currentName: string) {
    renameChannelNo = no;
    renameChannelValue = currentName;
  }

  function cancelRenameChannel() {
    renameChannelNo = null;
    renameChannelValue = "";
  }

  async function commitRenameChannel() {
    if (renameChannelNo === null) return;
    const next = renameChannelValue.trim();
    if (!next) {
      cancelRenameChannel();
      return;
    }
    renameChannelBusy = true;
    try {
      await api.renameChannel(address, renameChannelNo, next);
      toastStore.success(t("channel.renamed"));
      cancelRenameChannel();
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      renameChannelBusy = false;
    }
  }

  function onDelete() {
    if (!detail) return;
    deleteMode = "unpair";
    deleteForce = false;
    deleteDialogOpen = true;
    void loadDeleteDependencies();
  }

  // Load the dependencies that a removal would orphan: direct links on the
  // device and CCU programs that reference it. Best-effort — a failed probe
  // must not block the delete flow, so both fall back to an empty list and
  // simply suppress the corresponding warning.
  async function loadDeleteDependencies() {
    if (!detail) return;
    const addr = detail.address;
    deleteDepsLoading = true;
    deleteLinkCount = 0;
    deleteProgramNames = [];
    try {
      const [links, programs] = await Promise.all([
        api.listLinks(addr, locale).catch(() => []),
        api.listPrograms().catch(() => []),
      ]);
      deleteLinkCount = links.length;
      deleteProgramNames = programs
        .filter((p) => p.device_address === addr)
        .map((p) => p.name);
    } finally {
      deleteDepsLoading = false;
    }
  }

  function cancelDelete() {
    if (deleting) return;
    deleteDialogOpen = false;
  }

  async function confirmDelete() {
    if (!detail) return;
    deleting = true;
    try {
      await api.deleteDevice(address, {
        reset: deleteMode === "reset",
        force: deleteForce,
      });
      toastStore.success(t("device.removed"));
      deleteDialogOpen = false;
      location.hash = "#/devices";
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      deleting = false;
    }
  }

  async function onUpdateFirmware() {
    if (!detail) return;
    const ok = await confirmStore.ask({
      title: t("device.firmware_update"),
      body: t("device.confirm_firmware_body", {
        name: detail.name || detail.address,
      }),
      confirmLabel: t("device.firmware_update"),
    });
    if (!ok) return;
    updatingFw = true;
    try {
      await api.updateFirmware(address);
      toastStore.success(t("device.firmware_triggered"));
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      updatingFw = false;
    }
  }

  async function onRestoreConfig() {
    if (!detail) return;
    const ok = await confirmStore.ask({
      title: t("device.restore_config"),
      body: t("device.confirm_restore_config_body", {
        name: detail.name || detail.address,
      }),
      confirmLabel: t("device.restore_config"),
    });
    if (!ok) return;
    restoringConfig = true;
    try {
      await api.restoreDeviceConfig(address);
      toastStore.success(t("device.restore_config_triggered"));
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      restoringConfig = false;
    }
  }

  async function onTestCommunication() {
    if (!detail) return;
    testingComm = true;
    commTestResult = null;
    try {
      const r = await api.testDeviceCommunication(address);
      commTestResult = r;
      if (r.passed) {
        toastStore.success(t("device.communication_test_passed"));
      } else {
        toastStore.warn(t("device.communication_test_failed"));
      }
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      testingComm = false;
    }
  }

  async function exportDefinition() {
    if (!detail) return;
    exportingDef = true;
    try {
      const res = await fetch(
        `${(await import("$lib/api/base")).apiBase()}/devices/${encodeURIComponent(detail.address)}/export-definition`,
        { credentials: "same-origin" },
      );
      if (!res.ok) {
        toastStore.error(t("device.export_definition_error"));
        return;
      }
      const blob = await res.blob();
      const cd = res.headers.get("Content-Disposition") ?? "";
      const match = cd.match(/filename[^;=\n]*=["']?([^"';\n]+)["']?/i);
      const filename = match?.[1]?.trim() || `${detail.address}.zip`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toastStore.success(t("device.export_definition_success"));
    } catch {
      toastStore.error(t("device.export_definition_error"));
    } finally {
      exportingDef = false;
    }
  }

  function startEditRooms() {
    roomsDraft = (detail?.rooms ?? []).join(", ");
    editingRooms = true;
  }

  function startEditFunctions() {
    functionsDraft = (detail?.functions ?? []).join(", ");
    editingFunctions = true;
  }

  async function saveRooms() {
    if (!detail) return;
    roomsBusy = true;
    try {
      const list = roomsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setDeviceRooms(detail.address, list);
      toastStore.success(t("device.rooms_updated"));
      editingRooms = false;
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      roomsBusy = false;
    }
  }

  async function saveFunctions() {
    if (!detail) return;
    functionsBusy = true;
    try {
      const list = functionsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setDeviceFunctions(detail.address, list);
      toastStore.success(t("device.functions_updated"));
      editingFunctions = false;
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      functionsBusy = false;
    }
  }

  function clickChannelInStrip(ch: { number: number; type?: string }) {
    editingChannelRooms = false;
    editingChannelFunctions = false;
    if (isWeekProfileChannel(ch.type) && scheduleSupported) {
      configSub = "schedule";
      return;
    }
    location.hash = `#/devices/${detail?.address}/channels/${ch.number}`;
  }

  function startEditChannelRooms(rooms: string[] | undefined) {
    channelRoomsDraft = (rooms ?? []).join(", ");
    editingChannelRooms = true;
  }

  function startEditChannelFunctions(functions: string[] | undefined) {
    channelFunctionsDraft = (functions ?? []).join(", ");
    editingChannelFunctions = true;
  }

  async function saveChannelRooms(no: number) {
    channelRoomsBusy = true;
    try {
      const list = channelRoomsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setChannelRooms(address, no, list);
      toastStore.success(t("channel.rooms_updated"));
      editingChannelRooms = false;
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      channelRoomsBusy = false;
    }
  }

  async function saveChannelFunctions(no: number) {
    channelFunctionsBusy = true;
    try {
      const list = channelFunctionsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setChannelFunctions(address, no, list);
      toastStore.success(t("channel.functions_updated"));
      editingChannelFunctions = false;
      await load();
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    } finally {
      channelFunctionsBusy = false;
    }
  }

  // Top-level tabs. Three tabs only — the previous Bedienen/Status
  // split was redundant (both sourced from VALUES) and confusing.
  type TabDef = { key: TopTab; label: string; icon: IconName };
  const topTabs = $derived<TabDef[]>([
    { key: "overview", label: t("device.toptab.overview"), icon: "mdi:home" },
    { key: "configure", label: t("device.toptab.configure"), icon: "mdi:cog" },
    { key: "history", label: t("device.toptab.history"), icon: "mdi:history" },
  ]);

  type SubDef = { key: ConfigSub; label: string };
  const configSubs = $derived.by<SubDef[]>(() => {
    const out: SubDef[] = [];
    if (deviceChannel || channelZero) {
      out.push({
        key: "device-config",
        label: t("device.subtab.device_config"),
      });
    }
    if (userChannels.length > 0) {
      out.push({ key: "channels", label: t("device.subtab.channels") });
    }
    out.push({ key: "links", label: t("device.subtab.links") });
    if (scheduleSupported) {
      out.push({ key: "schedule", label: t("device.subtab.schedule") });
    }
    return out;
  });
</script>

<section class="@container mx-auto max-w-6xl px-4 py-6 sm:px-6">
  {#if error}
    <div class="mb-4">
      <ErrorState message={error} onRetry={load} />
    </div>
  {:else if !detail}
    <LoadingState />
  {/if}

  {#if detail}
    <!-- Header: breadcrumb + title row + secondary metadata + actions.
         Mirrors HA device-detail (icon, name, model line, copy-able
         address, action chips on the right). -->
    <header class="mb-6">
      <Breadcrumb
        items={[
          { label: t("nav.devices"), href: "#/devices" },
          { label: detail.name || detail.address },
        ]}
        class="mb-2"
      />
      <div
        class="flex flex-col gap-3 @3xl:flex-row @3xl:flex-wrap @3xl:items-start @3xl:justify-between"
      >
        <div class="min-w-0 flex-1">
          {#if renaming}
            <div class="flex flex-col gap-2">
              <div class="flex flex-wrap items-center gap-2">
                <div class="w-full sm:w-64">
                  <Input
                    type="text"
                    aria-label={t("device.rename")}
                    bind:value={renameValue}
                    onkeydown={(e) => {
                      if (e.key === "Enter") void commitRename();
                      else if (e.key === "Escape") renaming = false;
                    }}
                  />
                </div>
                <Button
                  type="button"
                  size="sm"
                  onclick={() => void commitRename()}
                  disabled={renameBusy}
                >
                  {t("common.save")}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => (renaming = false)}
                  disabled={renameBusy}
                >
                  {t("common.cancel")}
                </Button>
              </div>
              <label
                for="rename-include-channels"
                class="flex items-center gap-2 text-sm text-slate-600 dark:text-slate-300"
              >
                <Switch
                  id="rename-include-channels"
                  bind:checked={renameIncludeChannels}
                  disabled={renameBusy}
                />
                <span>{t("device.rename_include_channels")}</span>
              </label>
            </div>
          {:else}
            <h1 class="text-2xl font-semibold text-slate-900 dark:text-white">
              {detail.name || detail.address}
            </h1>
          {/if}
          <p class="mt-1 text-sm flex flex-wrap items-center gap-2 text-slate-500 dark:text-slate-400">
            <span class="font-mono">{detail.model}</span>
            {#if detail.model_label && detail.model_label !== detail.model}
              <span>·</span>
              <span>{detail.model_label}</span>
            {/if}
            <span aria-hidden="true">·</span>
            <span>{detail.interface}</span>
            <span aria-hidden="true">·</span>
            <span class="font-mono">{detail.address}</span>
            {#if !detail.available}
              <Badge variant="warning">{t("device.offline")}</Badge>
            {/if}
            {#if detail.update_available}
              <Badge variant="default">{t("device.update_available")}</Badge>
            {/if}
            {#if detail.master_pushes_config_pending && maintenanceStore.isPending(detail.address)}
              <Badge variant="warning" class="inline-flex items-center gap-1">
                <Icon name="mdi:calendar-clock" size={12} />
                {t("device.config_pending")}
              </Badge>
            {/if}
          </p>
          <div class="mt-1 grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
            <span class="font-semibold">{t("device.rooms")}:</span>
            <div class="flex items-baseline gap-2">
              {#if editingRooms}
                <div class="flex flex-1 items-center gap-2">
                  <input
                    type="text"
                    bind:value={roomsDraft}
                    placeholder={t("device.rooms.placeholder")}
                    class="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
                  />
                  <Button type="button" size="sm" onclick={() => void saveRooms()} disabled={roomsBusy}>
                    {t("common.save")}
                  </Button>
                  <Button type="button" variant="outline" size="sm" onclick={() => (editingRooms = false)} disabled={roomsBusy}>
                    ×
                  </Button>
                </div>
              {:else}
                <span>{(detail.rooms ?? []).join(", ") || t("common.none")}</span>
                <button
                  type="button"
                  class="text-brand-600 hover:underline dark:text-brand-400"
                  onclick={startEditRooms}
                >
                  {t("common.edit")}
                </button>
              {/if}
            </div>
            <span class="font-semibold">{t("device.functions")}:</span>
            <div class="flex items-baseline gap-2">
              {#if editingFunctions}
                <div class="flex flex-1 items-center gap-2">
                  <input
                    type="text"
                    bind:value={functionsDraft}
                    placeholder={t("device.functions.placeholder")}
                    class="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
                  />
                  <Button type="button" size="sm" onclick={() => void saveFunctions()} disabled={functionsBusy}>
                    {t("common.save")}
                  </Button>
                  <Button type="button" variant="outline" size="sm" onclick={() => (editingFunctions = false)} disabled={functionsBusy}>
                    ×
                  </Button>
                </div>
              {:else}
                <span>{(detail.functions ?? []).join(", ") || t("common.none")}</span>
                <button
                  type="button"
                  class="text-brand-600 hover:underline dark:text-brand-400"
                  onclick={startEditFunctions}
                >
                  {t("common.edit")}
                </button>
              {/if}
            </div>
          </div>
        </div>
        {#if !renaming}
          <div class="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={startRename}
            >
              <Icon name="mdi:pencil" size={14} />
              {t("device.rename")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void togglePin(detail?.name || address)}
              title={favoritesStore.isPinned("device", address)
                ? t("favorites.unpin")
                : t("favorites.pin")}
            >
              <Icon
                name={favoritesStore.isPinned("device", address)
                  ? "mdi:star"
                  : "mdi:star-outline"}
                size={14}
              />
              {favoritesStore.isPinned("device", address)
                ? t("favorites.pinned")
                : t("favorites.pin")}
            </Button>
            {#if detail.update_available}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void onUpdateFirmware()}
                disabled={updatingFw}
                title={t("device.firmware_update.tooltip", {
                  current: detail.firmware?.Current ?? "?",
                  available: detail.firmware?.Available ?? "?",
                })}
              >
                <Icon name="mdi:download" size={14} />
                {updatingFw ? "…" : t("device.firmware_update")}
              </Button>
            {/if}
            {#if detail.config_restore_supported}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void onRestoreConfig()}
                disabled={restoringConfig}
                title={t("device.restore_config.tooltip")}
              >
                <Icon name="mdi:backup-restore" size={14} />
                {restoringConfig ? "…" : t("device.restore_config")}
              </Button>
            {/if}
            {#if detail.communication_test_supported}
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => void onTestCommunication()}
                disabled={testingComm}
                title={t("device.communication_test.tooltip")}
              >
                <Icon name="mdi:radio-tower" size={14} />
                {testingComm
                  ? t("device.communication_test_running")
                  : t("device.communication_test")}
              </Button>
              {#if commTestResult}
                <Badge variant={commTestResult.passed ? "success" : "warning"}>
                  {commTestResult.passed
                    ? t("device.communication_test_passed")
                    : t("device.communication_test_failed")}
                </Badge>
              {/if}
            {/if}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void exportDefinition()}
              disabled={exportingDef}
            >
              <Icon name="mdi:download" size={14} />
              {exportingDef ? "…" : t("device.export_definition")}
            </Button>
            <Button
              type="button"
              variant="outline-destructive"
              size="sm"
              onclick={onDelete}
              disabled={deleting}
            >
              <Icon name="mdi:trash-can" size={14} />
              {t("device.remove")}
            </Button>
          </div>
        {/if}
      </div>
    </header>

    {#if detail.channels.length > 0}
      <!-- Top-level tab strip — Bedienen / Status / Konfigurieren / Verlauf.
           Sticks to icon + label, HA-style. -->
      <div
        class="mb-4 flex gap-0 border-b border-slate-200 dark:border-slate-700"
        role="tablist"
        aria-label={t("device.aria.top_tabs")}
      >
        {#each topTabs as tab (tab.key)}
          <button
            type="button"
            role="tab"
            aria-selected={topTab === tab.key}
            class="-mb-px inline-flex items-center gap-2 border-b-2 px-4 py-2 text-sm font-medium transition {topTab === tab.key
              ? 'border-brand-500 text-brand-700 dark:text-brand-300'
              : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200'}"
            onclick={() => {
              topTab = tab.key;
              if (tab.key === "history") onHistoryTabClick();
            }}
          >
            <Icon name={tab.icon} size={16} />
            {tab.label}
          </button>
        {/each}
      </div>

      {#if topTab === "overview"}
        <!-- Übersicht: maintenance grid (`:0` health) plus the
             CDP-tiles panel. CdpTilesPanel covers everything:
             actor widgets, orphan ChannelControl fallbacks, and the
             dense status stripe for read-only sensor channels. -->
        <div class="space-y-4">
          <MaintenanceStatusGrid address={detail.address} />
          <CdpTilesPanel {detail} />
        </div>
      {:else if topTab === "configure"}
        <!-- Sub-tab strip: Geräte-Konfiguration / Kanäle / Verknüpfungen / Zeitplan.
             Rendered as a quiet segmented control (recessed track + raised active
             card) so the brand-underlined top-level tab stays the only branded
             navigation marker and this second level does not compete with it. -->
        <div
          class="mb-4 inline-flex flex-wrap gap-0.5 rounded-lg bg-slate-100 p-0.5 dark:bg-slate-800"
          role="tablist"
          aria-label={t("device.aria.configure_sub_tabs")}
        >
          {#each configSubs as sub (sub.key)}
            <button
              type="button"
              role="tab"
              aria-selected={configSub === sub.key}
              class="rounded-md px-3 py-1.5 text-sm font-medium transition {configSub === sub.key
                ? 'bg-white text-slate-900 shadow-sm dark:bg-slate-700 dark:text-white'
                : 'text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white'}"
              onclick={() => (configSub = sub.key)}
            >
              {sub.label}
            </button>
          {/each}
        </div>

        {#if configSub === "device-config"}
          {#if deviceChannel}
            <h3 class="mb-2 text-sm font-semibold text-slate-900 dark:text-white">
              {t("device.subtab.device_config")} — {deviceChannel.address}
            </h3>
            <ChannelPanel
              address={detail.address}
              channel={deviceChannel.number}
              paramset="MASTER"
              {locale}
              pushesConfigPending={detail.master_pushes_config_pending}
            />
            {#if channelZero && channelZero.address !== deviceChannel.address}
              <h3 class="mb-2 mt-6 text-sm font-semibold text-slate-900 dark:text-white">
                {t("device.subtab.maintenance_config")} — {channelZero.address}
              </h3>
              <ChannelPanel
                address={detail.address}
                channel={channelZero.number}
                paramset="MASTER"
                {locale}
              />
            {/if}
          {:else if channelZero}
            <h3 class="mb-2 text-sm font-semibold text-slate-900 dark:text-white">
              {t("device.subtab.maintenance_config")} — {channelZero.address}
            </h3>
            <ChannelPanel
              address={detail.address}
              channel={channelZero.number}
              paramset="MASTER"
              {locale}
              pushesConfigPending={detail.master_pushes_config_pending}
            />
          {:else}
            <EmptyState message={t("device.no_device_config")} />
          {/if}
        {:else if configSub === "channels"}
          <!-- Channel selector strip. Each chip carries channel name +
               number badge, virtual marker (≥50), and a click that
               either selects the channel for editing or — for week-
               profile channels — switches to the Schedule sub-tab. -->
          <div
            class="mb-4 flex flex-wrap gap-1 border-b border-slate-200 dark:border-slate-700"
            role="tablist"
            aria-label={t("device.subtab.channels")}
          >
            {#each userChannels as ch (ch.address)}
              {@const isVirt = isVirtualChannel(ch.number)}
              {@const isWeek = isWeekProfileChannel(ch.type)}
              <button
                type="button"
                role="tab"
                aria-selected={ch.number === selectedChannel}
                onclick={() => clickChannelInStrip(ch)}
                class="-mb-px border-b-2 px-3 py-2 text-xs transition {ch.number === selectedChannel
                  ? 'border-slate-500 font-semibold text-slate-900 dark:border-slate-400 dark:text-white'
                  : 'border-transparent font-medium text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-200'} {isVirt ? '[border-style:dashed]' : ''}"
                title={ch.type_label ? `${ch.address} · ${ch.type_label}` : ch.address}
              >
                {ch.name?.trim() || ch.type_label || t("device.channel_n", { n: ch.number })}
                {#if isWeek}
                  <span class="ml-1">
                    <Icon name="mdi:calendar-clock" size={12} />
                  </span>
                {/if}
                {#if isVirt}
                  <Badge variant="muted" class="ml-1">{t("device.virtual")}</Badge>
                {/if}
                <span class="ml-1 text-xs text-slate-400 dark:text-slate-500">
                  ({ch.data_points_count})
                </span>
              </button>
            {/each}
          </div>

          {#if userChannels.length === 0}
            <EmptyState message={t("device.no_channels")} />
          {:else}
            {@const ch = userChannels.find((c) => c.number === selectedChannel) ?? userChannels[0]}
            {#if isWeekProfileChannel(ch.type)}
              <Card class="p-4">
                <div class="flex items-center gap-3">
                  <Icon name="mdi:calendar-clock" size={24} />
                  <div class="flex-1">
                    <h3 class="font-medium text-slate-900 dark:text-white">
                      {t("device.week_profile_channel.title")}
                    </h3>
                    <p class="text-sm text-slate-500 dark:text-slate-400">
                      {t("device.week_profile_channel.body")}
                    </p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => (configSub = "schedule")}
                  >
                    {t("device.subtab.schedule")}
                  </Button>
                </div>
              </Card>
            {:else}
              <!-- Per-channel rename affordance. The pencil opens an inline
                   editor; the CCU stores the channel name via Channel.setName. -->
              <div class="mb-3 flex flex-wrap items-center gap-2">
                {#if renameChannelNo === ch.number}
                  <div class="w-full sm:w-64">
                    <Input
                      type="text"
                      aria-label={t("channel.rename")}
                      bind:value={renameChannelValue}
                      onkeydown={(e) => {
                        if (e.key === "Enter") void commitRenameChannel();
                        else if (e.key === "Escape") cancelRenameChannel();
                      }}
                    />
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    onclick={() => void commitRenameChannel()}
                    disabled={renameChannelBusy}
                  >
                    {t("common.save")}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={cancelRenameChannel}
                    disabled={renameChannelBusy}
                  >
                    {t("common.cancel")}
                  </Button>
                {:else}
                  <h3 class="font-medium text-slate-900 dark:text-white">
                    {ch.name?.trim() ||
                      ch.type_label ||
                      t("device.channel_n", { n: ch.number })}
                  </h3>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-label={t("channel.rename")}
                    title={t("channel.rename")}
                    onclick={() => startRenameChannel(ch.number, ch.name ?? "")}
                  >
                    <Icon name="mdi:pencil" size={16} />
                  </Button>
                {/if}
              </div>
              <!-- Per-channel room / function assignment. Same comma-list
                   editor as the device level, persisted via
                   PATCH /devices/{addr}/channels/{no}. -->
              <div class="mb-3 grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-1 text-xs text-slate-500 dark:text-slate-400">
                <span class="font-semibold">{t("channel.rooms")}:</span>
                <div class="flex items-baseline gap-2">
                  {#if editingChannelRooms}
                    <div class="flex flex-1 items-center gap-2">
                      <input
                        type="text"
                        bind:value={channelRoomsDraft}
                        placeholder={t("device.rooms.placeholder")}
                        aria-label={t("channel.rooms")}
                        class="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
                      />
                      <Button type="button" size="sm" onclick={() => void saveChannelRooms(ch.number)} disabled={channelRoomsBusy}>
                        {t("common.save")}
                      </Button>
                      <Button type="button" variant="outline" size="sm" onclick={() => (editingChannelRooms = false)} disabled={channelRoomsBusy}>
                        ×
                      </Button>
                    </div>
                  {:else}
                    <span>{(ch.rooms ?? []).join(", ") || t("common.none")}</span>
                    <button
                      type="button"
                      class="text-brand-600 hover:underline dark:text-brand-400"
                      onclick={() => startEditChannelRooms(ch.rooms)}
                    >
                      {t("common.edit")}
                    </button>
                  {/if}
                </div>
                <span class="font-semibold">{t("channel.functions")}:</span>
                <div class="flex items-baseline gap-2">
                  {#if editingChannelFunctions}
                    <div class="flex flex-1 items-center gap-2">
                      <input
                        type="text"
                        bind:value={channelFunctionsDraft}
                        placeholder={t("device.functions.placeholder")}
                        aria-label={t("channel.functions")}
                        class="flex-1 rounded-md border border-slate-300 bg-white px-2 py-1 text-xs text-slate-900 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
                      />
                      <Button type="button" size="sm" onclick={() => void saveChannelFunctions(ch.number)} disabled={channelFunctionsBusy}>
                        {t("common.save")}
                      </Button>
                      <Button type="button" variant="outline" size="sm" onclick={() => (editingChannelFunctions = false)} disabled={channelFunctionsBusy}>
                        ×
                      </Button>
                    </div>
                  {:else}
                    <span>{(ch.functions ?? []).join(", ") || t("common.none")}</span>
                    <button
                      type="button"
                      class="text-brand-600 hover:underline dark:text-brand-400"
                      onclick={() => startEditChannelFunctions(ch.functions)}
                    >
                      {t("common.edit")}
                    </button>
                  {/if}
                </div>
              </div>
              {#if detail.team_supported}
                <div class="mb-3">
                  <TeamPicker address={detail.address} channel={ch.number} />
                </div>
              {/if}
              <ChannelPanel
                address={detail.address}
                channel={ch.number}
                paramset="MASTER"
                {locale}
                pushesConfigPending={detail.master_pushes_config_pending}
              />
            {/if}
          {/if}
        {:else if configSub === "links"}
          <div class="space-y-4">
            <CentralLinksPanel address={detail.address} />
            <DeviceLinks
              deviceAddress={detail.address}
              interfaceId={detail.interface_id}
              {locale}
            />
          </div>
        {:else if configSub === "schedule"}
          <ScheduleTab address={detail.address} />
        {/if}
      {:else if topTab === "history"}
        <div class="space-y-6">
          <!-- Measurement history chart for a user-selected numeric data point. -->
          {#if userChannels.length > 0}
            <div>
              <div class="mb-2 flex flex-wrap items-center gap-2">
                <span class="text-xs font-semibold text-slate-500 dark:text-slate-400">{t("history.label_channel")}</span>
                <Select
                  class="w-auto"
                  value={String(historyChannelNo ?? userChannels[0]?.number)}
                  onValueChange={(v) => {
                    const no = Number(v);
                    historyChannelNo = no;
                    void loadHistoryDPs(no);
                  }}
                  options={userChannels.map((ch) => ({
                    value: String(ch.number),
                    label: `${ch.name?.trim() || ch.type_label || t("history.channel_n", { n: ch.number })} (${ch.number})`,
                  }))}
                />
                {#if historyDPs.length > 0}
                  <span class="text-xs font-semibold text-slate-500 dark:text-slate-400">{t("history.label_parameter")}</span>
                  <Select
                    class="w-auto"
                    value={historyParameter ?? historyDPs[0]?.parameter}
                    onValueChange={(v) => {
                      historyParameter = v;
                    }}
                    options={historyDPs.map((dp) => ({
                      value: dp.parameter,
                      label: `${dp.parameter_label || dp.parameter}${dp.unit ? ` (${dp.unit})` : ""}`,
                    }))}
                  />
                {:else if historyDPsLoading}
                  <span class="text-xs text-slate-500 dark:text-slate-400">{t("history.loading_parameters")}</span>
                {:else if historyChannelNo !== null}
                  <span class="text-xs text-slate-500 dark:text-slate-400">{t("history.no_numeric")}</span>
                {/if}
                {#if historyParameter && historyChannelNo !== null && detail.central && detail.interface_id}
                  <RecordToggle
                    central={detail.central}
                    interfaceId={detail.interface_id}
                    channel={`${detail.address}:${historyChannelNo}`}
                    parameter={historyParameter}
                  />
                {/if}
              </div>
              {#if historyParameter && historyChannelNo !== null && detail.central && detail.interface_id}
                {@const selectedDP = historyDPs.find((dp) => dp.parameter === historyParameter)}
                <HistoryChart
                  central={detail.central ?? ""}
                  interfaceId={detail.interface_id}
                  channel={`${detail.address}:${historyChannelNo}`}
                  parameter={historyParameter}
                  parameterLabel={selectedDP?.parameter_label || selectedDP?.parameter}
                  unit={selectedDP?.unit ?? ""}
                />
              {/if}
            </div>
          {/if}
          <!-- Change audit log (existing). -->
          <AuditLog deviceFilter={detail.address} embedded />
        </div>
      {/if}
    {:else}
      <EmptyState message={t("device.no_channels")} />
    {/if}

    <!-- Remove-device options dialog. Follows the shared ConfirmDialog
         visual pattern (overlay + card tokens + destructive confirm) but
         hosts the removal-mode radio, force checkbox, and dependency
         warning the plain confirm dialog cannot carry. -->
    {#if deleteDialogOpen}
      <div
        class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
        style="background-color: rgb(0 0 0 / 0.45);"
        role="dialog"
        aria-modal="true"
        aria-label={t("device.confirm_remove_title")}
        tabindex="-1"
        onclick={(e) => {
          if (e.target === e.currentTarget) cancelDelete();
        }}
        onkeydown={(e) => {
          if (e.key === "Escape") cancelDelete();
        }}
      >
        <div
          class="w-full max-w-md p-5"
          style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color); border-radius: var(--ha-radius-card); box-shadow: var(--ha-elevation-modal);"
        >
          <h2 class="mb-2 text-lg font-semibold">
            {t("device.confirm_remove_title")}
          </h2>
          <p class="mb-4 text-sm" style="color: var(--ha-secondary-text-color);">
            {t("device.confirm_remove_body", {
              name: detail.name || detail.address,
            })}
          </p>

          {#if deleteDepsLoading}
            <p
              class="mb-4 text-sm"
              style="color: var(--ha-secondary-text-color);"
            >
              {t("device.delete.checking")}
            </p>
          {:else if deleteHasDeps}
            <div
              class="mb-4 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-950/40 dark:text-amber-200"
              role="alert"
            >
              <p class="font-semibold">{t("device.delete.warning_title")}</p>
              <ul class="mt-1 list-inside list-disc space-y-0.5">
                {#if deleteLinkCount > 0}
                  <li>
                    {t("device.delete.warning_links", {
                      count: deleteLinkCount,
                    })}
                  </li>
                {/if}
                {#if deleteProgramNames.length > 0}
                  <li>
                    {t("device.delete.warning_programs", {
                      count: deleteProgramNames.length,
                    })}
                  </li>
                {/if}
              </ul>
            </div>
          {/if}

          <fieldset class="mb-4">
            <legend class="mb-2 text-sm font-medium">
              {t("device.delete.mode_label")}
            </legend>
            <label class="flex items-start gap-2 py-1 text-sm">
              <input
                type="radio"
                name="delete-mode"
                value="unpair"
                bind:group={deleteMode}
                class="mt-0.5 accent-brand-600"
              />
              <span>
                <span class="font-medium">{t("device.delete.mode_unpair")}</span>
                <span
                  class="block text-xs"
                  style="color: var(--ha-secondary-text-color);"
                >
                  {t("device.delete.mode_unpair_hint")}
                </span>
              </span>
            </label>
            <label class="flex items-start gap-2 py-1 text-sm">
              <input
                type="radio"
                name="delete-mode"
                value="reset"
                bind:group={deleteMode}
                class="mt-0.5 accent-brand-600"
              />
              <span>
                <span class="font-medium">{t("device.delete.mode_reset")}</span>
                <span
                  class="block text-xs"
                  style="color: var(--ha-secondary-text-color);"
                >
                  {t("device.delete.mode_reset_hint")}
                </span>
              </span>
            </label>
          </fieldset>

          <label class="mb-4 flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              bind:checked={deleteForce}
              class="mt-0.5 accent-brand-600"
            />
            <span>
              <span class="font-medium">{t("device.delete.force")}</span>
              <span
                class="block text-xs"
                style="color: var(--ha-secondary-text-color);"
              >
                {t("device.delete.force_hint")}
              </span>
            </span>
          </label>

          <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button
              type="button"
              variant="outline"
              size="md"
              class="w-full sm:w-auto"
              onclick={cancelDelete}
              disabled={deleting}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="md"
              class="w-full sm:w-auto"
              onclick={() => void confirmDelete()}
              disabled={deleting}
            >
              {t("common.delete")}
            </Button>
          </div>
        </div>
      </div>
    {/if}
  {/if}
</section>
