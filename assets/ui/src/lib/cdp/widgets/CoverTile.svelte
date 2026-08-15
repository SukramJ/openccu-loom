<!--
  CDP-aware cover tile. Covers `cover`, `cover_blind`, `cover_garage`
  via the same widget; capability flags + kind drive which controls
  render.

  Writes go through the semantic CDP operation surface:
  - open / close / stop / set_position (cover + blind)
  - set_tilt (blind)
  - ventilate (garage; "Lüften" intermediate position)

  Reads still come from the channel's data points until the daemon
  exposes a populated CDP-state surface — same pattern as LightTile.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { CustomDPSummary, DataPointSummary } from "$lib/api/types";
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import { enumValueToken } from "$lib/sensor-actor/classify";
  import { t } from "$lib/i18n";
  import ControlTile from "$lib/control/tile/ControlTile.svelte";
  import ControlTileIcon from "$lib/control/tile/ControlTileIcon.svelte";
  import ControlTileInfo from "$lib/control/tile/ControlTileInfo.svelte";
  import EmptyTile from "$lib/control/tile/EmptyTile.svelte";
  import CoverOpenCloseFeature from "$lib/control/features/CoverOpenCloseFeature.svelte";
  import NumericInputFeature from "$lib/control/features/NumericInputFeature.svelte";
  import ControlButtonGroup from "$lib/control/controls/ControlButtonGroup.svelte";
  import ControlButton from "$lib/control/controls/ControlButton.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  type Props = {
    address: string;
    cdp: CustomDPSummary;
    title?: string;
  };

  let { address, cdp, title }: Props = $props();
  const displayTitle = $derived(title ?? cdp.name);

  let dataPoints = $state<DataPointSummary[]>([]);
  let error = $state<string | null>(null);

  const channelAddress = $derived(`${address}:${cdp.channel_no}`);

  const caps = $derived(cdp.capabilities ?? {});
  const isGarage = $derived(cdp.kind === "cover_garage");
  const hasPosition = $derived(caps.position === true && !isGarage);
  const hasTilt = $derived(caps.tilt === true);
  const hasStop = $derived(caps.stop === true);
  const hasVent = $derived(caps.vent === true);

  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  const levelDP = $derived(dp("LEVEL"));
  // HmIP blinds use LEVEL_2 for slat tilt, RF jalousies LEVEL_SLATS.
  const tiltDP = $derived(dp("LEVEL_2") ?? dp("LEVEL_SLATS"));
  const stateDP = $derived(dp("DOOR_STATE"));

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const tilt = $derived(typeof tiltDP?.value === "number" ? tiltDP.value : 0);
  const observed = $derived((levelDP?.observed || stateDP?.observed) ?? false);

  // Open/close/stop stay operable regardless of the observed position —
  // neither the garage button group nor CoverOpenCloseFeature gate
  // open/close on a live read (only the optional stop/tilt/position
  // controls depend on capability flags, which are static CDP
  // metadata) — so this tile never falls back to the compact EmptyTile.
  const hasControls = true;
  const showEmpty = $derived(!observed && !hasControls);

  // Garage state mapping. DOOR_STATE is a read-only ENUM, so the value
  // that arrives over REST and over the WS patch below is the value_list
  // index — never the CLOSED / OPEN / … token STATE_KEY is keyed on.
  const garageState = $derived<string>(
    (stateDP ? enumValueToken(stateDP) : undefined) ?? "",
  );
  const STATE_KEY: Record<string, string> = {
    CLOSED: "cdp.cover.state_closed",
    OPEN: "cdp.cover.state_open",
    VENTILATION_POSITION: "cdp.cover.state_ventilating",
    POSITION_UNKNOWN: "cdp.cover.state_unknown",
  };

  const isOpen = $derived.by(() => {
    if (isGarage) {
      return garageState === "OPEN" || garageState === "VENTILATION_POSITION";
    }
    return level > 0 && level < 1;
  });

  const tileColor = $derived(
    isOpen
      ? "var(--state-cover-active-color, var(--ha-primary-color))"
      : "var(--ha-secondary-text-color)",
  );

  const secondary = $derived.by(() => {
    if (!observed) return undefined;
    if (isGarage) {
      const key = STATE_KEY[garageState];
      return key ? t(key) : t("cdp.cover.state_unknown");
    }
    const parts = [t("cdp.cover.secondary_open", { pct: Math.round(level * 100) })];
    if (tiltDP?.observed) {
      parts.push(t("cdp.cover.secondary_slats", { pct: Math.round(tilt * 100) }));
    }
    return parts.join(" · ");
  });

  async function load() {
    error = null;
    try {
      dataPoints = await api.listDataPoints(address, cdp.channel_no);
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  async function invoke(op: string, params: Record<string, unknown> = {}) {
    try {
      await api.invokeCustomDataPoint(address, cdp.name, op, params);
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  onMount(() => {
    load();
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const e = ev.payload as { channel_address: string; parameter: string; value: unknown };
      if (e.channel_address !== channelAddress) return;
      const idx = dataPoints.findIndex((d) => d.parameter === e.parameter);
      if (idx < 0) return;
      dataPoints[idx] = { ...dataPoints[idx], value: e.value, observed: true };
    });
    // The boot snapshot no longer replays values into the stream; it
    // signals a resync and the tile reloads its own state.
    const unsubResync = onResync(() => void load());
    return () => {
      unsub();
      unsubResync();
    };
  });
</script>

{#if error}
  <div class="mb-2 rounded-md border border-[var(--ha-error-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-error-color)]">
    {error}
  </div>
{/if}
{#if showEmpty}
  <EmptyTile icon={isGarage ? "mdi:door-closed" : "mdi:sliders"} title={displayTitle} />
{:else}
<ControlTile {tileColor} focused={isOpen}>
    {#snippet icon()}
      <ControlTileIcon active={isOpen} label={displayTitle}>
        <Icon name={isGarage ? "mdi:door-closed" : "mdi:sliders"} size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      {#if isGarage}
        <ControlButtonGroup>
          <ControlButton
            active={garageState === "OPEN"}
            color={tileColor}
            label={t("cdp.cover.open")}
            onClick={() => invoke("open")}
          >{t("cdp.cover.open")}</ControlButton>
          {#if hasStop}
            <ControlButton
              active={false}
              color={tileColor}
              label={t("cdp.cover.stop")}
              onClick={() => invoke("stop")}
            >{t("cdp.cover.stop")}</ControlButton>
          {/if}
          <ControlButton
            active={garageState === "CLOSED"}
            color={tileColor}
            label={t("cdp.cover.close")}
            onClick={() => invoke("close")}
          >{t("cdp.cover.close")}</ControlButton>
          {#if hasVent}
            <ControlButton
              active={garageState === "VENTILATION_POSITION"}
              color={tileColor}
              label={t("cdp.cover.ventilate")}
              onClick={() => invoke("ventilate")}
            >{t("cdp.cover.ventilate")}</ControlButton>
          {/if}
        </ControlButtonGroup>
      {:else}
        <CoverOpenCloseFeature
          color={tileColor}
          canOpen={true}
          canStop={hasStop}
          canClose={true}
          onOpen={() => invoke("open")}
          onStop={() => invoke("stop")}
          onClose={() => invoke("close")}
        />
        {#if hasPosition && levelDP}
          <NumericInputFeature
            value={level}
            color={tileColor}
            disabled={!levelDP.operations.write}
            label={t("cdp.cover.position")}
            onChange={(v) => invoke("set_position", { position: v })}
          />
        {/if}
        {#if hasTilt && tiltDP}
          <NumericInputFeature
            value={tilt}
            color={tileColor}
            disabled={!tiltDP.operations.write}
            label={t("cdp.cover.slats")}
            onChange={(v) => invoke("set_tilt", { tilt: v })}
          />
        {/if}
      {/if}
    {/snippet}
  </ControlTile>
{/if}
