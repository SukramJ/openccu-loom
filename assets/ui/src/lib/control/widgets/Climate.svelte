<!--
  CONTROL widget covering both Climate families the CCU exposes:
  - HEATING_CONTROL_HMIP: HmIP thermostats (HmIP-BWTH/WTH/eTRV-E and
    floor / chamber thermostats). CONTROL_MODE is a writable integer
    enum: 0=AUTO, 1=MANU, 2=AWAY. BOOST_MODE / FROST_PROTECTION
    surface as bool toggles, BOOST optionally with BOOST_TIME.
  - HEATING_CONTROL: classic RF thermostats (HM-CC-RT-DN, HM-TC-IT-…).
    CONTROL_MODE is read-only — the mode is changed by firing the
    matching ACTION slot (AUTO / BOOST / COMFORT / LOWERING / MANU).

  The widget reads its slot inventory and chooses the appropriate
  control surface; HmIP and RF can therefore share the same tile
  without forking. Layout pattern from HA frontend's more-info
  climate dialog (Apache-2.0).
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import TargetTemperatureFeature from "../features/TargetTemperatureFeature.svelte";
  import HvacModesFeature from "../features/HvacModesFeature.svelte";
  import PresetModesFeature from "../features/PresetModesFeature.svelte";
  import StatReadoutFeature from "../features/StatReadoutFeature.svelte";
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";
  import { enumValueToken } from "$lib/sensor-actor/classify";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  // The RF family writes its modes via ACTION slots (AUTO / BOOST /
  // COMFORT / LOWERING). HmIP writes CONTROL_MODE directly.
  const isRf = $derived(resolved.family === "HEATING_CONTROL");

  const setpointDP = $derived(resolved.slots.SETPOINT);
  const tempDP = $derived(resolved.slots.TEMPERATURE);
  const humidityDP = $derived(resolved.slots.HUMIDITY);
  const modeDP = $derived(resolved.slots.CONTROL_MODE);
  const boostDP = $derived(resolved.slots.BOOST_MODE);
  const frostDP = $derived(resolved.slots.FROST_PROTECTION);
  const valveDP = $derived(resolved.slots.LEVEL);
  const windowDP = $derived(resolved.slots.WINDOW_STATE);
  const heatCoolDP = $derived(resolved.slots.HEATING_COOLING);

  // RF-only action slots.
  const autoAction = $derived(resolved.slots.AUTO);
  const boostAction = $derived(resolved.slots.BOOST);
  const comfortAction = $derived(resolved.slots.COMFORT);
  const loweringAction = $derived(resolved.slots.LOWERING);

  const setpoint = $derived(
    typeof setpointDP?.value === "number" ? setpointDP.value : 21,
  );
  const currentTemp = $derived(tempDP?.value);
  const currentHumidity = $derived(humidityDP?.value);
  const modeValue = $derived(modeDP?.value);

  // HmIP CONTROL_MODE is an INTEGER without VALUE_LIST on the wire
  // (the CCU just ships the raw enum index). The label map mirrors
  // aiohomematic's `_ModeHmIP` (climate.py:76-81).
  const HMIP_MODES = $derived([
    { value: 0, label: t("climate.mode.auto") },
    { value: 1, label: t("climate.mode.manual") },
    { value: 2, label: t("climate.mode.away") },
  ]);

  // RF CONTROL_MODE ships a VALUE_LIST when present —
  // ["AUTO-MODE","MANU-MODE","PARTY-MODE","BOOST-MODE"]. Display the
  // current mode as a status string; the user changes the mode via
  // the action buttons below.
  const RF_MODE_LABEL = $derived<Record<string, string>>({
    "AUTO-MODE": t("climate.mode.auto"),
    "MANU-MODE": t("climate.mode.manual"),
    "PARTY-MODE": t("climate.mode.away"),
    "BOOST-MODE": t("climate.mode.boost"),
  });
  // The RF CONTROL_MODE is read-only, so the wire carries the value_list
  // index — never the token the mode table is keyed on. Resolve it back
  // before comparing, otherwise no mode ever matches.
  const rfModeToken = $derived<string>((modeDP ? enumValueToken(modeDP) : undefined) ?? "");
  const currentRfModeLabel = $derived<string>(
    rfModeToken ? (RF_MODE_LABEL[rfModeToken] ?? rfModeToken) : "",
  );

  const tileColor = $derived(
    resolveTileColor(resolved.family, modeValue, setpointDP?.observed ?? false),
  );

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    const parts: string[] = [];
    if (isRf && currentRfModeLabel) parts.push(currentRfModeLabel);
    if (typeof currentTemp === "number") {
      parts.push(`${currentTemp.toFixed(1)} °C → ${setpoint.toFixed(1)} °C`);
    } else {
      parts.push(`${setpoint.toFixed(1)} °C`);
    }
    return parts.join(" · ");
  });

  const hmipPresets = $derived.by(() => {
    const out: { key: string; label: string; value: boolean; writable: boolean }[] = [];
    if (boostDP) {
      out.push({
        key: "BOOST_MODE",
        label: t("climate.preset.boost"),
        value: Boolean(boostDP.value),
        writable: boostDP.operations.write,
      });
    }
    if (frostDP) {
      out.push({
        key: "FROST_PROTECTION",
        label: t("climate.preset.frost"),
        value: Boolean(frostDP.value),
        writable: frostDP.operations.write,
      });
    }
    return out;
  });
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={setpointDP?.observed ?? false} label={title}>
      <Icon name="mdi:thermometer" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if setpointDP}
      <!-- Bounds come from the descriptor: a virtual heating group's
           SETPOINT is 5–30 °C, and a stepper that offers 4.5 °C only
           earns a 400. -->
      <TargetTemperatureFeature
        value={setpoint}
        color={tileColor}
        min={typeof setpointDP.min === "number" ? setpointDP.min : undefined}
        max={typeof setpointDP.max === "number" ? setpointDP.max : undefined}
        disabled={!setpointDP.operations.write}
        onChange={(v) => onSetSlot("SETPOINT", v)}
      />
    {/if}

    {#if !isRf && modeDP && modeDP.operations.write}
      <!-- HmIP mode picker — writes CONTROL_MODE directly. -->
      <HvacModesFeature
        value={typeof modeValue === "number" || typeof modeValue === "string" ? modeValue : 0}
        options={HMIP_MODES}
        color={tileColor}
        onChange={(v) => onSetSlot("CONTROL_MODE", v)}
      />
    {/if}

    {#if isRf && (autoAction || boostAction || comfortAction || loweringAction)}
      <!-- RF mode picker — each press fires the matching ACTION slot. -->
      <ControlButtonGroup>
        {#if autoAction}
          <ControlButton
            active={rfModeToken === "AUTO-MODE"}
            color={tileColor}
            label={t("climate.mode.auto")}
            onClick={() => onSetSlot("AUTO", true)}
          >{t("climate.mode.auto")}</ControlButton>
        {/if}
        {#if boostAction}
          <ControlButton
            active={rfModeToken === "BOOST-MODE"}
            color={tileColor}
            label={t("climate.mode.boost")}
            onClick={() => onSetSlot("BOOST", true)}
          >{t("climate.mode.boost")}</ControlButton>
        {/if}
        {#if comfortAction}
          <ControlButton
            active={false}
            color={tileColor}
            label={t("climate.preset.comfort")}
            onClick={() => onSetSlot("COMFORT", true)}
          >{t("climate.preset.comfort")}</ControlButton>
        {/if}
        {#if loweringAction}
          <ControlButton
            active={false}
            color={tileColor}
            label={t("climate.preset.lowering")}
            onClick={() => onSetSlot("LOWERING", true)}
          >{t("climate.preset.lowering")}</ControlButton>
        {/if}
      </ControlButtonGroup>
    {/if}

    {#if !isRf && hmipPresets.length > 0}
      <PresetModesFeature
        presets={hmipPresets}
        color={tileColor}
        onToggle={(slot, next) => onSetSlot(slot, next)}
      />
    {/if}

    {#if tempDP || humidityDP || valveDP || windowDP || heatCoolDP}
      <div class="grid grid-cols-2 gap-2">
        {#if tempDP}
          <StatReadoutFeature label={t("climate.stat.current_temp")} value={currentTemp} unit="°C" />
        {/if}
        {#if humidityDP}
          <StatReadoutFeature label={t("climate.stat.humidity")} value={currentHumidity} unit="%" />
        {/if}
        {#if valveDP}
          <StatReadoutFeature label={t("climate.stat.valve")} value={valveDP.value} />
        {/if}
        {#if windowDP}
          <StatReadoutFeature label={t("climate.stat.window")} value={windowDP.value} />
        {/if}
        {#if heatCoolDP}
          <StatReadoutFeature label={t("climate.stat.heat_cool")} value={heatCoolDP.value} />
        {/if}
      </div>
    {/if}
  {/snippet}
</ControlTile>
