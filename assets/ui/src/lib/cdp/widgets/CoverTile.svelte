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
  import { subscribe } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";
  import ControlTile from "$lib/control/tile/ControlTile.svelte";
  import ControlTileIcon from "$lib/control/tile/ControlTileIcon.svelte";
  import ControlTileInfo from "$lib/control/tile/ControlTileInfo.svelte";
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

  // Garage state mapping.
  const garageState = $derived<string>(
    typeof stateDP?.value === "string" ? stateDP.value : "",
  );
  const STATE_DE: Record<string, string> = {
    CLOSED: "Geschlossen",
    OPEN: "Offen",
    VENTILATION_POSITION: "Lüftet",
    POSITION_UNKNOWN: "Unbekannt",
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
    if (!observed) return "—";
    if (isGarage) {
      return STATE_DE[garageState] ?? "Unbekannt";
    }
    const parts = [`${Math.round(level * 100)} % geöffnet`];
    if (tiltDP?.observed) {
      parts.push(`Lamellen ${Math.round(tilt * 100)} %`);
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
    return () => unsub();
  });
</script>

{#if error}
  <div class="mb-2 rounded-md border border-[var(--ha-error-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-error-color)]">
    {error}
  </div>
{/if}
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
            label="Öffnen"
            onClick={() => invoke("open")}
          >Öffnen</ControlButton>
          {#if hasStop}
            <ControlButton
              active={false}
              color={tileColor}
              label="Halt"
              onClick={() => invoke("stop")}
            >Halt</ControlButton>
          {/if}
          <ControlButton
            active={garageState === "CLOSED"}
            color={tileColor}
            label="Schließen"
            onClick={() => invoke("close")}
          >Schließen</ControlButton>
          {#if hasVent}
            <ControlButton
              active={garageState === "VENTILATION_POSITION"}
              color={tileColor}
              label="Lüften"
              onClick={() => invoke("ventilate")}
            >Lüften</ControlButton>
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
            label="Position"
            onChange={(v) => invoke("set_position", { position: v })}
          />
        {/if}
        {#if hasTilt && tiltDP}
          <NumericInputFeature
            value={tilt}
            color={tileColor}
            disabled={!tiltDP.operations.write}
            label="Lamellen"
            onChange={(v) => invoke("set_tilt", { tilt: v })}
          />
        {/if}
      {/if}
    {/snippet}
  </ControlTile>
