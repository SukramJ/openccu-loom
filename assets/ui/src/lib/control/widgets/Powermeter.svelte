<!--
  CONTROL widget for the POWERMETER / POWERMETER_IEC / POWERMETER_IGL
  / POWERMETER_PSM families. Read-only — all slots are sensor
  readouts. Hero is POWER (live load); secondary grid covers
  ENERGY_COUNTER, VOLTAGE, CURRENT, FREQUENCY.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import StatReadoutFeature from "../features/StatReadoutFeature.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
  };

  let { resolved, title, secondary }: Props = $props();

  const powerDP = $derived(resolved.slots.POWER);
  const energyDP = $derived(resolved.slots.ENERGY_COUNTER);
  const voltageDP = $derived(resolved.slots.VOLTAGE);
  const currentDP = $derived(resolved.slots.CURRENT);
  const frequencyDP = $derived(resolved.slots.FREQUENCY);

  const power = $derived(typeof powerDP?.value === "number" ? powerDP.value : 0);
  const observed = $derived(powerDP?.observed ?? false);

  const tileColor = $derived(resolveTileColor(resolved.family, power, observed));

  const computedSecondary = $derived(
    secondary ?? (observed ? `${power.toFixed(1)} W` : "—"),
  );
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={power > 0} label={title}>
      <Icon name="mdi:zap" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    <div class="grid grid-cols-2 gap-2">
      {#if powerDP}
        <StatReadoutFeature label={t("control.power")} value={powerDP.value} unit="W" />
      {/if}
      {#if energyDP}
        <StatReadoutFeature
          label={t("control.energy")}
          value={energyDP.value}
          unit="Wh"
          format={(v) =>
            typeof v === "number" ? v.toFixed(0) : String(v)}
        />
      {/if}
      {#if voltageDP}
        <StatReadoutFeature label={t("control.voltage")} value={voltageDP.value} unit="V" />
      {/if}
      {#if currentDP}
        <StatReadoutFeature
          label={t("control.current")}
          value={currentDP.value}
          unit="mA"
          format={(v) =>
            typeof v === "number" ? v.toFixed(0) : String(v)}
        />
      {/if}
      {#if frequencyDP}
        <StatReadoutFeature label={t("control.frequency")} value={frequencyDP.value} unit="Hz" />
      {/if}
    </div>
  {/snippet}
</ControlTile>
