<!--
  Multi-family widget for the CCU "simple RF thermostat" channel
  shape (HM-CC-TC channel 2 / CLIMATECONTROL_REGULATOR): one
  CONTROL-tagged DP from SWITCH (STATE) and one from TEMP (SETPOINT)
  on the same channel. The standard family resolver picks SWITCH
  alphabetically and the setpoint vanishes, so we wire the channel
  through this combined widget instead.

  Layout: small thermostat tile (HA more-info-climate, Apache-2.0)
  with a heat-on/off toggle and a setpoint stepper. No mode picker,
  no boost, no presets — this device is intentionally minimal.
-->
<script lang="ts">
  import type { DataPointSummary } from "$lib/api/types";
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ToggleFeature from "../features/ToggleFeature.svelte";
  import TargetTemperatureFeature from "../features/TargetTemperatureFeature.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  // The dominant family resolver picks one of {SWITCH, TEMP}; pull the
  // primary's slot map and the sibling's so we surface both.
  const stateDP = $derived<DataPointSummary | undefined>(
    resolved.slots.STATE ?? resolved.siblings.SWITCH?.STATE,
  );
  const setpointDP = $derived<DataPointSummary | undefined>(
    resolved.slots.SETPOINT ?? resolved.siblings.TEMP?.SETPOINT,
  );

  const isOn = $derived(Boolean(stateDP?.value));
  const setpoint = $derived(
    typeof setpointDP?.value === "number" ? setpointDP.value : 21,
  );
  const observed = $derived(stateDP?.observed || setpointDP?.observed || false);
  const tileColor = $derived(
    isOn
      ? "var(--state-climate-heat-color, var(--ha-warning-color))"
      : resolveTileColor("SWITCH", isOn, observed),
  );

  const computedSecondary = $derived(
    secondary ?? (observed ? `${setpoint.toFixed(1)} °C${isOn ? ` · ${t("quick.on")}` : ` · ${t("quick.off")}`}` : "—"),
  );
</script>

<ControlTile {tileColor} focused={isOn}>
  {#snippet icon()}
    <ControlTileIcon active={isOn} label={title}>
      <Icon name="mdi:thermometer" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if stateDP}
      <ToggleFeature
        value={isOn}
        color={tileColor}
        disabled={!stateDP.operations.write}
        labelOff={t("quick.off")}
        labelOn={t("quick.on")}
        {title}
        onChange={(v) => onSetSlot("STATE", v)}
      />
    {/if}
    {#if setpointDP}
      <TargetTemperatureFeature
        value={setpoint}
        color={tileColor}
        disabled={!setpointDP.operations.write}
        onChange={(v) => onSetSlot("SETPOINT", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
