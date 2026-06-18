<script lang="ts">
  import { onMount } from "svelte";
  import type { DeviceDetail } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import DeviceLinks from "$lib/components/links/DeviceLinks.svelte";
  import CentralLinksPanel from "$lib/components/links/CentralLinksPanel.svelte";
  import CdpTilesPanel from "$lib/cdp/CdpTilesPanel.svelte";
  import ScheduleTab from "$lib/components/schedule/ScheduleTab.svelte";
  import MaintenanceStatusGrid from "$lib/components/device/MaintenanceStatusGrid.svelte";
  import AuditLog from "./AuditLog.svelte";
  import HistoryChart from "$lib/components/HistoryChart.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Breadcrumb from "$lib/components/ui/Breadcrumb.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { maintenanceStore } from "$lib/stores/maintenance.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    channel?: number;
    locale: string;
  };

  let { address, channel, locale }: Props = $props();

  let detail = $state<DeviceDetail | null>(null);
  let error = $state<string | null>(null);
  let banner = $state<string | null>(null);

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
  let deleting = $state(false);
  let updatingFw = $state(false);

  let editingRooms = $state(false);
  let roomsDraft = $state("");
  let roomsBusy = $state(false);

  let editingFunctions = $state(false);
  let functionsDraft = $state("");
  let functionsBusy = $state(false);

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
    renaming = true;
    banner = null;
  }

  async function commitRename() {
    if (!detail) return;
    const next = renameValue.trim();
    if (!next || next === detail.name) {
      renaming = false;
      return;
    }
    renameBusy = true;
    banner = null;
    try {
      await api.renameDevice(address, next);
      banner = t("device.renamed");
      renaming = false;
      await load();
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
    } finally {
      renameBusy = false;
    }
  }

  async function onDelete() {
    if (!detail) return;
    const ok = await confirmStore.ask({
      title: t("device.confirm_remove_title"),
      body: t("device.confirm_remove_body", {
        name: detail.name || detail.address,
      }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    deleting = true;
    banner = null;
    try {
      await api.deleteDevice(address);
      banner = t("device.removed");
      location.hash = "#/devices";
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
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
    banner = null;
    try {
      await api.updateFirmware(address);
      banner = t("device.firmware_triggered");
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
    } finally {
      updatingFw = false;
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
    banner = null;
    try {
      const list = roomsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setDeviceRooms(detail.address, list);
      banner = t("device.rooms_updated");
      editingRooms = false;
      await load();
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
    } finally {
      roomsBusy = false;
    }
  }

  async function saveFunctions() {
    if (!detail) return;
    functionsBusy = true;
    banner = null;
    try {
      const list = functionsDraft
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      await api.setDeviceFunctions(detail.address, list);
      banner = t("device.functions_updated");
      editingFunctions = false;
      await load();
    } catch (err) {
      banner = err instanceof Error ? err.message : String(err);
    } finally {
      functionsBusy = false;
    }
  }

  function clickChannelInStrip(ch: { number: number; type?: string }) {
    if (isWeekProfileChannel(ch.type) && scheduleSupported) {
      configSub = "schedule";
      return;
    }
    location.hash = `#/devices/${detail?.address}/channels/${ch.number}`;
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

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  {#if error}
    <Card class="mb-4 p-3">
      <p class="text-sm" style="color: var(--ha-error-color);">
        {t("device.error_label", { message: error })}
      </p>
    </Card>
  {/if}

  {#if banner}
    <Card class="mb-4 p-3">
      <p class="text-sm" style="color: var(--ha-secondary-text-color);">{banner}</p>
    </Card>
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
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0 flex-1">
          {#if renaming}
            <div class="flex flex-wrap items-center gap-2">
              <div class="w-full sm:w-64">
                <Input
                  type="text"
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
          {:else}
            <h1
              class="text-2xl font-semibold"
              style="color: var(--ha-primary-text-color);"
            >
              {detail.name || detail.address}
            </h1>
          {/if}
          <p class="mt-1 text-sm flex flex-wrap items-center gap-2" style="color: var(--ha-secondary-text-color);">
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
          <div class="mt-1 grid grid-cols-[auto_1fr] items-baseline gap-x-3 gap-y-1 text-xs" style="color: var(--ha-secondary-text-color);">
            <span class="font-semibold">{t("device.rooms")}:</span>
            <div class="flex items-baseline gap-2">
              {#if editingRooms}
                <div class="flex flex-1 items-center gap-2">
                  <input
                    type="text"
                    bind:value={roomsDraft}
                    placeholder={t("device.rooms.placeholder")}
                    class="flex-1 rounded-md border px-2 py-1 text-xs"
                    style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
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
                  class="hover:underline"
                  style="color: var(--ha-primary-color);"
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
                    class="flex-1 rounded-md border px-2 py-1 text-xs"
                    style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
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
                  class="hover:underline"
                  style="color: var(--ha-primary-color);"
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
            <Button
              type="button"
              variant="destructive"
              size="sm"
              onclick={() => void onDelete()}
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
      <nav
        class="mb-4 flex gap-0 border-b"
        style="border-color: var(--ha-divider-color);"
        aria-label={t("device.aria.top_tabs")}
      >
        {#each topTabs as tab (tab.key)}
          <button
            type="button"
            class="-mb-px inline-flex items-center gap-2 border-b-2 px-4 py-2 text-sm font-medium transition"
            style="border-color: {topTab === tab.key ? 'var(--ha-primary-color)' : 'transparent'}; color: {topTab === tab.key ? 'var(--ha-primary-color)' : 'var(--ha-secondary-text-color)'};"
            onclick={() => {
              topTab = tab.key;
              if (tab.key === "history") onHistoryTabClick();
            }}
          >
            <Icon name={tab.icon} size={16} />
            {tab.label}
          </button>
        {/each}
      </nav>

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
        <!-- Sub-tab strip: Geräte-Konfiguration / Kanäle / Verknüpfungen / Zeitplan -->
        <nav class="mb-4 flex flex-wrap gap-2" aria-label={t("device.aria.configure_sub_tabs")}>
          {#each configSubs as sub (sub.key)}
            <button
              type="button"
              class="rounded-md border px-3 py-1.5 text-sm transition"
              style="border-color: {configSub === sub.key ? 'var(--ha-primary-color)' : 'var(--ha-divider-color)'}; background-color: {configSub === sub.key ? 'var(--ha-primary-color)' : 'transparent'}; color: {configSub === sub.key ? 'white' : 'var(--ha-primary-text-color)'};"
              onclick={() => (configSub = sub.key)}
            >
              {sub.label}
            </button>
          {/each}
        </nav>

        {#if configSub === "device-config"}
          {#if deviceChannel}
            <h3 class="mb-2 text-sm font-semibold" style="color: var(--ha-primary-text-color);">
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
              <h3 class="mb-2 mt-6 text-sm font-semibold" style="color: var(--ha-primary-text-color);">
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
            <h3 class="mb-2 text-sm font-semibold" style="color: var(--ha-primary-text-color);">
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
            <Card class="p-4">
              <p class="text-sm" style="color: var(--ha-secondary-text-color);">
                {t("device.no_device_config")}
              </p>
            </Card>
          {/if}
        {:else if configSub === "channels"}
          <!-- Channel selector strip. Each chip carries channel name +
               number badge, virtual marker (≥50), and a click that
               either selects the channel for editing or — for week-
               profile channels — switches to the Schedule sub-tab. -->
          <nav
            class="mb-4 flex flex-wrap gap-1 border-b"
            style="border-color: var(--ha-divider-color);"
            aria-label={t("device.subtab.channels")}
          >
            {#each userChannels as ch (ch.address)}
              {@const isVirt = isVirtualChannel(ch.number)}
              {@const isWeek = isWeekProfileChannel(ch.type)}
              <button
                type="button"
                onclick={() => clickChannelInStrip(ch)}
                class="-mb-px border-b-2 px-3 py-2 text-sm transition"
                style="border-color: {ch.number === selectedChannel ? 'var(--ha-primary-color)' : 'transparent'}; color: {ch.number === selectedChannel ? 'var(--ha-primary-color)' : 'var(--ha-secondary-text-color)'}; {isVirt ? 'border-style: dashed;' : ''}"
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
                <span class="ml-1 text-xs" style="color: var(--ha-disabled-text-color);">
                  ({ch.data_points_count})
                </span>
              </button>
            {/each}
          </nav>

          {#if userChannels.length === 0}
            <Card class="p-4">
              <p class="text-sm" style="color: var(--ha-secondary-text-color);">
                {t("device.no_channels")}
              </p>
            </Card>
          {:else}
            {@const ch = userChannels.find((c) => c.number === selectedChannel) ?? userChannels[0]}
            {#if isWeekProfileChannel(ch.type)}
              <Card class="p-4">
                <div class="flex items-center gap-3">
                  <Icon name="mdi:calendar-clock" size={24} />
                  <div class="flex-1">
                    <h3 class="font-medium" style="color: var(--ha-primary-text-color);">
                      {t("device.week_profile_channel.title")}
                    </h3>
                    <p class="text-sm" style="color: var(--ha-secondary-text-color);">
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
                <span class="text-xs font-semibold" style="color: var(--ha-secondary-text-color);">{t("history.label_channel")}</span>
                <select
                  class="rounded border px-2 py-1 text-xs"
                  style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
                  value={historyChannelNo ?? userChannels[0]?.number}
                  onchange={(e) => {
                    const no = Number((e.target as HTMLSelectElement).value);
                    historyChannelNo = no;
                    void loadHistoryDPs(no);
                  }}
                >
                  {#each userChannels as ch (ch.number)}
                    <option value={ch.number}>
                      {ch.name?.trim() || ch.type_label || t("history.channel_n", { n: ch.number })} ({ch.number})
                    </option>
                  {/each}
                </select>
                {#if historyDPs.length > 0}
                  <span class="text-xs font-semibold" style="color: var(--ha-secondary-text-color);">{t("history.label_parameter")}</span>
                  <select
                    class="rounded border px-2 py-1 text-xs"
                    style="background-color: var(--ha-card-background-color); border-color: var(--ha-divider-color); color: var(--ha-primary-text-color);"
                    value={historyParameter ?? historyDPs[0]?.parameter}
                    onchange={(e) => {
                      historyParameter = (e.target as HTMLSelectElement).value;
                    }}
                  >
                    {#each historyDPs as dp (dp.parameter)}
                      <option value={dp.parameter}>
                        {dp.parameter_label || dp.parameter}{dp.unit ? ` (${dp.unit})` : ""}
                      </option>
                    {/each}
                  </select>
                {:else if historyDPsLoading}
                  <span class="text-xs" style="color: var(--ha-secondary-text-color);">{t("history.loading_parameters")}</span>
                {:else if historyChannelNo !== null}
                  <span class="text-xs" style="color: var(--ha-secondary-text-color);">{t("history.no_numeric")}</span>
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
      <Card class="p-4">
        <p class="text-sm" style="color: var(--ha-secondary-text-color);">
          {t("device.no_channels")}
        </p>
      </Card>
    {/if}
  {/if}
</section>
