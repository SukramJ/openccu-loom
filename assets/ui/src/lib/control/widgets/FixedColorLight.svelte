<!--
  CONTROL widget for DIMMER channels that carry a fixed COLOR enum
  (HmIP-BSL, HmIPW-WGC, HM-LC-RGBW-WM). Same family as a plain
  Dimmer (`DIMMER.LEVEL`) but enriched with `DIMMER.COLOR` (eight
  named colours) and optionally `DIMMER.COLOR_BEHAVIOUR` (effect
  enum). Layout: Dimmer-Slider + 8-Farben-Palette + Effekt-Select.

  Visual idiom from HA's more-info-light dialog
  (frontend/src/dialogs/more-info/controls/more-info-light.ts,
  Apache-2.0).
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ToggleFeature from "../features/ToggleFeature.svelte";
  import NumericInputFeature from "../features/NumericInputFeature.svelte";
  import ControlColorPalette from "../controls/ControlColorPalette.svelte";
  import ControlEnumSelect from "../controls/ControlEnumSelect.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";
  import { enumValueLabel, enumValueToken } from "$lib/sensor-actor/classify";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const levelDP = $derived(resolved.slots.LEVEL);
  const colorDP = $derived(resolved.slots.COLOR);
  const effectDP = $derived(resolved.slots.COLOR_BEHAVIOUR);

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const levelObserved = $derived(levelDP?.observed ?? false);
  const writable = $derived(levelDP?.operations.write ?? false);

  const colorOptions = $derived<string[]>(colorDP?.value_list ?? []);
  // COLOR is a writable ENUM, so the wire value is the value_list index —
  // resolve it back before comparing or naming the current colour.
  const colorToken = $derived(colorDP ? enumValueToken(colorDP) : undefined);
  const colorCaption = $derived(colorDP ? enumValueLabel(colorDP) : undefined);
  const effectOptions = $derived<string[]>(effectDP?.value_list ?? []);

  const tileColor = $derived(resolveTileColor(resolved.family, level, levelObserved));
  const isOn = $derived(level > 0);

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    if (!levelObserved) return "—";
    const pct = `${Math.round(level * 100)} %`;
    if (colorToken && colorToken !== "BLACK") {
      return `${pct} · ${colorCaption ?? colorToken}`;
    }
    return pct;
  });
</script>

<ControlTile {tileColor} focused={isOn}>
  {#snippet icon()}
    <ControlTileIcon active={isOn} label={title}>
      <Icon name="mdi:lightbulb" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if levelDP}
      <ToggleFeature
        value={isOn}
        color={tileColor}
        disabled={!writable}
        {title}
        onChange={(next) => onSetSlot("LEVEL", next ? 1 : 0)}
      />
      <NumericInputFeature
        value={level}
        color={tileColor}
        disabled={!writable}
        label={t("control.brightness")}
        onChange={(v) => onSetSlot("LEVEL", v)}
      />
    {/if}
    {#if colorDP && colorOptions.length > 0}
      <ControlColorPalette
        value={colorDP.value as string | number | undefined}
        options={colorOptions}
        disabled={!(colorDP.operations.write)}
        label={t("control.color")}
        onChange={(v) => onSetSlot("COLOR", v)}
      />
    {/if}
    {#if effectDP && effectOptions.length > 0}
      <ControlEnumSelect
        value={effectDP.value as string | number | undefined}
        options={effectOptions}
        disabled={!(effectDP.operations.write)}
        label={t("control.effect")}
        onChange={(v) => onSetSlot("COLOR_BEHAVIOUR", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
