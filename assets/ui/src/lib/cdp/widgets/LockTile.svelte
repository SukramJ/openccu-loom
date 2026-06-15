<!--
  CDP-aware lock tile. The CDP service surface exposes lock /
  unlock / open (the third action triggers the door-opener relay
  when the device has one — flagged by capabilities.open). Reads
  come from the channel's STATE + OPEN_BY_BUTTON parameters; the
  tile shows whether the lock is engaged + whether it is currently
  in motion.
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
  import LockCommandsFeature from "$lib/control/features/LockCommandsFeature.svelte";
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
  const stateDP = $derived(dp("STATE"));
  const uncertainDP = $derived(dp("UNCERTAIN"));

  const isLocked = $derived(Boolean(stateDP?.value));
  const observed = $derived(stateDP?.observed ?? false);

  const tileColor = $derived(
    !isLocked && observed
      ? "var(--state-lock-active-color, var(--ha-error-color))"
      : "var(--ha-secondary-text-color)",
  );

  const secondary = $derived.by(() => {
    if (uncertainDP?.value) return t("control.status_unknown");
    if (!observed) return "—";
    return isLocked ? t("control.locked") : t("control.unlocked");
  });

  async function load() {
    error = null;
    try {
      dataPoints = await api.listDataPoints(address, cdp.channel_no);
    } catch (err) {
      error = friendlyError(err, t);
    }
  }

  async function invoke(op: string) {
    try {
      await api.invokeCustomDataPoint(address, cdp.name, op);
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
<ControlTile {tileColor} focused={!isLocked && observed}>
    {#snippet icon()}
      <ControlTileIcon active={!isLocked} label={displayTitle}>
        <Icon name={isLocked ? "mdi:lock" : "mdi:lock-open"} size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      <LockCommandsFeature
        color={tileColor}
        {isLocked}
        canLock={true}
        canUnlock={true}
        canOpenDoor={caps.open === true}
        onLock={() => invoke("lock")}
        onUnlock={() => invoke("unlock")}
        onOpen={() => invoke("open")}
      />
    {/snippet}
  </ControlTile>
