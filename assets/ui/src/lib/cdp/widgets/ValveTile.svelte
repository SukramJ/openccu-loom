<!--
  CDP-aware valve tile. Covers `valve_irrigation` (on/off with an
  optional auto-close duration) and `valve_modulating` (continuous
  level). The Modulating variant uses the same NumericInputFeature
  slider as Cover / Light tiles; Irrigation falls back to a simple
  open/close button group plus an optional "Auf 10 min" preset.

  Service operations: open / close / set_level (Modulating only).
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
  import EmptyTile from "$lib/control/tile/EmptyTile.svelte";
  import NumericInputFeature from "$lib/control/features/NumericInputFeature.svelte";
  import TimedActionFeature from "$lib/control/features/TimedActionFeature.svelte";
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
  const isModulating = $derived(cdp.kind === "valve_modulating");

  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  const levelDP = $derived(dp("LEVEL"));
  const stateDP = $derived(dp("STATE"));

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const isOn = $derived(
    isModulating ? level > 0 : Boolean(stateDP?.value),
  );
  const observed = $derived(
    (isModulating ? levelDP?.observed : stateDP?.observed) ?? false,
  );

  // Open/close stay operable regardless of the observed state (neither
  // button is gated on a live read), so this tile never falls back to
  // the compact EmptyTile.
  const hasControls = true;
  const showEmpty = $derived(!observed && !hasControls);

  const tileColor = $derived(
    isOn && observed
      ? "var(--state-switch-active-color, var(--ha-primary-color))"
      : "var(--ha-secondary-text-color)",
  );

  const secondary = $derived.by(() => {
    if (!observed) return undefined;
    if (isModulating) return t("cdp.valve.secondary_open", { pct: Math.round(level * 100) });
    return isOn ? t("cdp.valve.state_open") : t("cdp.valve.state_closed");
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
{#if showEmpty}
  <EmptyTile icon="mdi:gauge" title={displayTitle} />
{:else}
<ControlTile {tileColor} focused={isOn}>
    {#snippet icon()}
      <ControlTileIcon active={isOn} label={displayTitle}>
        <Icon name="mdi:gauge" size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      <ControlButtonGroup>
        <ControlButton
          active={!isOn}
          color={tileColor}
          label={t("cdp.valve.close")}
          onClick={() => invoke("close")}
        >{t("cdp.valve.close")}</ControlButton>
        <ControlButton
          active={isOn}
          color={tileColor}
          label={t("cdp.valve.open")}
          onClick={() => invoke("open")}
        >{t("cdp.valve.open")}</ControlButton>
      </ControlButtonGroup>
      {#if !isModulating}
        <TimedActionFeature
          label={t("cdp.valve.open_for")}
          presets={[60, 300, 600]}
          defaultSeconds={600}
          color={tileColor}
          disabled={!(stateDP?.operations.write ?? true)}
          onSubmit={(seconds) => invoke("open", { seconds })}
        />
      {/if}
      {#if isModulating && levelDP}
        <NumericInputFeature
          value={level}
          color={tileColor}
          disabled={!levelDP.operations.write}
          label={t("cdp.valve.opening")}
          onChange={(v) => invoke("set_level", { level: v })}
        />
      {/if}
    {/snippet}
  </ControlTile>
{/if}
