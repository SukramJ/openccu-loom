<!--
  CDP-aware siren tile. The CDP exposes turn_on / turn_off; the
  capabilities map tells us which axes (acoustic / optical / volume /
  soundfile / duration) the device supports — we surface them as
  badges and as optional parameters on the turn_on press, but the
  default press is a plain "Test" / "Aus" toggle.
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

  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  // The active acoustic/optical selection. Either non-zero ENUM index
  // signals "alarming"; we surface both for the secondary line.
  const acousticDP = $derived(dp("ACOUSTIC_ALARM_SELECTION"));
  const opticalDP = $derived(dp("OPTICAL_ALARM_SELECTION"));
  const levelDP = $derived(dp("LEVEL"));

  const activeAcoustic = $derived(
    Number(acousticDP?.value ?? 0) > 0,
  );
  const activeOptical = $derived(Number(opticalDP?.value ?? 0) > 0);
  const isActive = $derived(
    activeAcoustic || activeOptical || Boolean(levelDP?.value),
  );
  const observed = $derived(
    (acousticDP?.observed || opticalDP?.observed || levelDP?.observed) ?? false,
  );

  const tileColor = $derived(
    isActive
      ? "var(--state-siren-active-color, var(--ha-error-color))"
      : "var(--ha-secondary-text-color)",
  );

  const secondary = $derived.by(() => {
    if (!observed) return "—";
    if (isActive) {
      const parts: string[] = [];
      if (activeAcoustic) parts.push("Akustisch");
      if (activeOptical) parts.push("Optisch");
      return parts.length > 0 ? parts.join(" + ") : "Alarm aktiv";
    }
    return "Ruhe";
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
<ControlTile {tileColor} focused={isActive}>
    {#snippet icon()}
      <ControlTileIcon active={isActive} label={displayTitle}>
        <span class:pulse={isActive}>
          <Icon name="mdi:alert-triangle" size={22} />
        </span>
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      <ControlButtonGroup>
        <ControlButton
          active={!isActive}
          color={tileColor}
          label="Aus"
          onClick={() => invoke("turn_off")}
        >Aus</ControlButton>
        <ControlButton
          active={isActive}
          color={tileColor}
          label="Test"
          onClick={() => invoke("turn_on")}
        >Test</ControlButton>
      </ControlButtonGroup>
      {#if caps.acoustic || caps.optical || caps.volume_set || caps.duration}
        <div class="flex flex-wrap gap-2 text-xs text-[var(--ha-secondary-text-color)]">
          {#if caps.acoustic}<span class="rounded bg-[var(--ha-divider-color)] px-2 py-0.5">Akustik</span>{/if}
          {#if caps.optical}<span class="rounded bg-[var(--ha-divider-color)] px-2 py-0.5">Optik</span>{/if}
          {#if caps.volume_set}<span class="rounded bg-[var(--ha-divider-color)] px-2 py-0.5">Lautstärke</span>{/if}
          {#if caps.duration}<span class="rounded bg-[var(--ha-divider-color)] px-2 py-0.5">Dauer</span>{/if}
        </div>
      {/if}
    {/snippet}
  </ControlTile>

<style>
  .pulse {
    display: inline-block;
    animation: pulse 1s ease-in-out infinite;
  }
  @keyframes pulse {
    0%, 100% { transform: scale(1); }
    50% { transform: scale(1.2); }
  }
</style>
