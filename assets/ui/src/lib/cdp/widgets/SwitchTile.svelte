<!--
  CDP-aware switch tile. The simplest CDP shape: a single STATE
  parameter driven by turn_on / turn_off (toggle is composed
  client-side from the current state). Optionally surfaces
  turn_on_for via a separate "kurz an" press when the CDP advertises
  the operation.
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
  import ToggleFeature from "$lib/control/features/ToggleFeature.svelte";
  import TimedActionFeature from "$lib/control/features/TimedActionFeature.svelte";
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

  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  const stateDP = $derived(dp("STATE"));

  const isOn = $derived(Boolean(stateDP?.value));
  const observed = $derived(stateDP?.observed ?? false);

  // The toggle stays operable even before the first observed value —
  // it is only truly non-operable when the STATE parameter itself is
  // read-only. Drives the compact EmptyTile fallback below.
  const hasControls = $derived(stateDP?.operations.write ?? true);
  const showEmpty = $derived(!observed && !hasControls);

  const tileColor = $derived(
    isOn && observed
      ? "var(--state-switch-active-color, var(--ha-primary-color))"
      : "var(--ha-secondary-text-color)",
  );

  // No placeholder dash while unobserved — an operable tile stays a
  // normal card without a status region; a non-operable one renders
  // EmptyTile instead (see `showEmpty`).
  const secondary = $derived(observed ? (isOn ? t("quick.on") : t("quick.off")) : undefined);

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
  <EmptyTile icon="mdi:power" title={displayTitle} />
{:else}
  <ControlTile {tileColor} focused={isOn}>
    {#snippet icon()}
      <ControlTileIcon active={isOn} label={displayTitle}>
        <Icon name="mdi:power" size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      <ToggleFeature
        value={isOn}
        color={tileColor}
        disabled={!(stateDP?.operations.write ?? true)}
        title={displayTitle}
        onChange={(next) => invoke(next ? "turn_on" : "turn_off")}
      />
      {#if cdp.supported_operations?.includes("turn_on_for")}
        <TimedActionFeature
          label={t("cdp.switch.on_for")}
          color={tileColor}
          disabled={!(stateDP?.operations.write ?? true)}
          onSubmit={(seconds) => invoke("turn_on_for", { seconds })}
        />
      {/if}
    {/snippet}
  </ControlTile>
{/if}
