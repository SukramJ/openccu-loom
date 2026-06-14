<!--
  CONTROL widget for the UNIVERSAL_LIGHT_RECEIVER family — HmIP-RGBW
  and HmIP-DRG-DALI 48-channel DALI bus. The richest light surface in
  the CCU catalogue: brightness, HSV colour, colour temperature and
  effect programs all coexist on a single channel; the widget renders
  only the slots the descriptor actually exposes.

  Layout pattern from HA frontend's more-info-light dialog
  (frontend/src/dialogs/more-info/controls/more-info-light.ts,
  Apache-2.0). Picker mode (Color vs. Temperature) is local UI state
  — the CCU keeps both modes addressable simultaneously, so flipping
  the toggle changes which slider the user sees, not which mode is
  "live".
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ToggleFeature from "../features/ToggleFeature.svelte";
  import NumericInputFeature from "../features/NumericInputFeature.svelte";
  import ControlHueSlider from "../controls/ControlHueSlider.svelte";
  import ControlSaturationSlider from "../controls/ControlSaturationSlider.svelte";
  import ControlColorTempSlider from "../controls/ControlColorTempSlider.svelte";
  import ControlEnumSelect from "../controls/ControlEnumSelect.svelte";
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
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

  const levelDP = $derived(resolved.slots.LEVEL);
  const hueDP = $derived(resolved.slots.HUE);
  const satDP = $derived(resolved.slots.SATURATION);
  const ctDP = $derived(resolved.slots.COLOR_TEMPERATURE);
  const effectDP = $derived(resolved.slots.EFFECT);

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const observed = $derived(levelDP?.observed ?? false);
  const writable = $derived(levelDP?.operations.write ?? false);

  const hue = $derived(typeof hueDP?.value === "number" ? hueDP.value : 0);
  const saturation = $derived(typeof satDP?.value === "number" ? satDP.value : 0);
  const kelvin = $derived(typeof ctDP?.value === "number" ? ctDP.value : 3000);

  const effectOptions = $derived<string[]>(effectDP?.value_list ?? []);

  const hasColor = $derived(Boolean(hueDP && satDP));
  const hasCT = $derived(Boolean(ctDP));

  // Picker mode: Color (HUE+SAT) vs Temperature (kelvin). Default to
  // whichever the channel supports; when both, prefer the last-touched.
  let mode = $state<"color" | "temp">("color");
  $effect(() => {
    if (!hasColor && hasCT) mode = "temp";
    else if (!hasCT && hasColor) mode = "color";
  });

  const isOn = $derived(level > 0);
  const tileColor = $derived.by(() => {
    if (mode === "color" && hasColor && observed && isOn) {
      return `hsl(${hue} ${Math.round(saturation * 100)}% 55%)`;
    }
    return resolveTileColor(resolved.family, level, observed);
  });

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    if (!observed) return "—";
    const parts = [`${Math.round(level * 100)} %`];
    if (mode === "color" && hasColor) parts.push(`H ${Math.round(hue)}° · S ${Math.round(saturation * 100)} %`);
    if (mode === "temp" && hasCT) parts.push(`${Math.round(kelvin)} K`);
    return parts.join(" · ");
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
        onChange={(next) => onSetSlot("LEVEL", next ? 1 : 0)}
      />
      <NumericInputFeature
        value={level}
        color={tileColor}
        disabled={!writable}
        label={t("light.brightness")}
        onChange={(v) => onSetSlot("LEVEL", v)}
      />
    {/if}
    {#if hasColor && hasCT}
      <ControlButtonGroup>
        <ControlButton
          active={mode === "color"}
          color={tileColor}
          label={t("light.mode.color")}
          onClick={() => (mode = "color")}
        >{t("light.mode.color")}</ControlButton>
        <ControlButton
          active={mode === "temp"}
          color={tileColor}
          label={t("light.mode.white")}
          onClick={() => (mode = "temp")}
        >{t("light.mode.white")}</ControlButton>
      </ControlButtonGroup>
    {/if}
    {#if mode === "color" && hasColor}
      <div class="flex flex-col gap-1">
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("light.hue")}</span>
        <ControlHueSlider
          value={hue}
          min={0}
          max={360}
          disabled={!(hueDP?.operations.write)}
          label={t("light.hue")}
          onChange={(v) => onSetSlot("HUE", v)}
        />
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("light.saturation")}</span>
        <ControlSaturationSlider
          value={saturation}
          {hue}
          disabled={!(satDP?.operations.write)}
          label={t("light.saturation")}
          onChange={(v) => onSetSlot("SATURATION", v)}
        />
      </div>
    {/if}
    {#if mode === "temp" && hasCT}
      <div class="flex flex-col gap-1">
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("light.color_temp")}</span>
        <ControlColorTempSlider
          value={kelvin}
          disabled={!(ctDP?.operations.write)}
          label={t("light.color_temp")}
          onChange={(v) => onSetSlot("COLOR_TEMPERATURE", v)}
        />
      </div>
    {/if}
    {#if effectDP && effectOptions.length > 0}
      <ControlEnumSelect
        value={effectDP.value as string | number | undefined}
        options={effectOptions}
        disabled={!(effectDP.operations.write)}
        label={t("light.effect")}
        onChange={(v) => onSetSlot("EFFECT", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
