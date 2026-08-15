<!--
  CDP-aware light tile. Renders one tile per CustomDpIp*Light /
  CustomDpRfDimmer / CustomDpDimmer / … by reading the CDP's
  capability flags and the underlying channel data points. Writes go
  through the semantic CDP operation surface
  (`POST /devices/.../cdps/{name}/{operation}`) rather than per-slot
  REST writes — the daemon handles the slot-level dispatch.

  Capability matrix:

  - dimmable           → brightness slider + on/off toggle
  - color (with COLOR slot, no HUE)  → fixed-colour palette
  - color (with HUE+SATURATION)      → HSV picker (hue + saturation)
  - color_temp                       → kelvin slider
  - effects                          → effect dropdown

  Modes that coexist (RGBW, DALI) carry a Color/Weiß toggle as in the
  channel-side UniversalLight widget — the same primitives back it.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { CustomDPSummary, DataPointSummary } from "$lib/api/types";
  import { onResync, subscribe } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";
  import ControlTile from "$lib/control/tile/ControlTile.svelte";
  import ControlTileIcon from "$lib/control/tile/ControlTileIcon.svelte";
  import ControlTileInfo from "$lib/control/tile/ControlTileInfo.svelte";
  import EmptyTile from "$lib/control/tile/EmptyTile.svelte";
  import ToggleFeature from "$lib/control/features/ToggleFeature.svelte";
  import NumericInputFeature from "$lib/control/features/NumericInputFeature.svelte";
  import ControlHueSlider from "$lib/control/controls/ControlHueSlider.svelte";
  import ControlSaturationSlider from "$lib/control/controls/ControlSaturationSlider.svelte";
  import ControlColorTempSlider from "$lib/control/controls/ControlColorTempSlider.svelte";
  import ControlColorPalette from "$lib/control/controls/ControlColorPalette.svelte";
  import ControlEnumSelect from "$lib/control/controls/ControlEnumSelect.svelte";
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

  // Capability shortcuts — undefined keys fall through as false.
  const caps = $derived(cdp.capabilities ?? {});
  const isDimmable = $derived(caps.dimmable === true);
  const hasHsvColor = $derived(caps.color === true && cdp.kind !== "light_fixed_color");
  const hasFixedColor = $derived(cdp.kind === "light_fixed_color");
  const hasColorTemp = $derived(caps.color_temp === true);
  const hasEffects = $derived(caps.effects === true);

  // Slot lookups by parameter name. Custom-DPs of the light family
  // expose LEVEL / HUE / SATURATION / COLOR (enum) / COLOR_TEMPERATURE /
  // COLOR_BEHAVIOUR / EFFECT on the same channel.
  function dp(name: string): DataPointSummary | undefined {
    return dataPoints.find((d) => d.parameter === name);
  }
  const levelDP = $derived(dp("LEVEL"));
  const hueDP = $derived(dp("HUE"));
  const satDP = $derived(dp("SATURATION"));
  const ctDP = $derived(dp("COLOR_TEMPERATURE"));
  const colorDP = $derived(dp("COLOR"));
  const effectDP = $derived(dp("COLOR_BEHAVIOUR") ?? dp("EFFECT"));

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const hue = $derived(typeof hueDP?.value === "number" ? hueDP.value : 0);
  const saturation = $derived(typeof satDP?.value === "number" ? satDP.value : 0);
  const kelvin = $derived(typeof ctDP?.value === "number" ? ctDP.value : 3000);

  const colorOptions = $derived<string[]>(colorDP?.value_list ?? []);
  const effectOptions = $derived<string[]>(effectDP?.value_list ?? []);

  const observed = $derived(levelDP?.observed ?? false);
  const isOn = $derived(level > 0);

  // The on/off toggle stays operable regardless of the observed level
  // (no `disabled` is wired to it below), so this tile never falls
  // back to the compact EmptyTile.
  const hasControls = true;
  const showEmpty = $derived(!observed && !hasControls);

  // Picker mode — only relevant when both HSV and CT coexist (RGBW).
  let mode = $state<"color" | "temp">("color");
  $effect(() => {
    if (!hasHsvColor && hasColorTemp) mode = "temp";
    else if (!hasColorTemp && hasHsvColor) mode = "color";
  });

  const tileColor = $derived.by(() => {
    if (!observed || !isOn) {
      return "var(--ha-secondary-text-color)";
    }
    if (mode === "color" && hasHsvColor) {
      return `hsl(${hue} ${Math.round(saturation * 100)}% 55%)`;
    }
    return "var(--state-light-active-color, var(--ha-primary-color))";
  });

  const secondary = $derived.by(() => {
    if (!observed) return undefined;
    const parts = [`${Math.round(level * 100)} %`];
    if (mode === "color" && hasHsvColor) parts.push(`H ${Math.round(hue)}°`);
    if (mode === "temp" && hasColorTemp) parts.push(`${Math.round(kelvin)} K`);
    const c = colorDP?.value;
    if (hasFixedColor && typeof c === "string" && c && c !== "BLACK") {
      parts.push(c.toLowerCase());
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
      // Optimistic — the WS event reconciles the state.
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
    // The boot snapshot no longer replays values into the stream; it
    // signals a resync and the tile reloads its own state.
    const unsubResync = onResync(() => void load());
    return () => {
      unsub();
      unsubResync();
    };
  });
</script>

{#if error}
  <div class="mb-2 rounded-md border border-[var(--ha-error-color)] bg-[var(--ha-card-background-color)] p-2 text-xs text-[var(--ha-error-color)]">
    {error}
  </div>
{/if}
{#if showEmpty}
  <EmptyTile icon="mdi:lightbulb" title={displayTitle} />
{:else}
<ControlTile {tileColor} focused={isOn}>
    {#snippet icon()}
      <ControlTileIcon active={isOn} label={displayTitle}>
        <Icon name="mdi:lightbulb" size={22} />
      </ControlTileIcon>
    {/snippet}
    {#snippet info()}
      <ControlTileInfo primary={displayTitle} {secondary} />
    {/snippet}
    {#snippet features()}
      <ToggleFeature
        value={isOn}
        color={tileColor}
        title={displayTitle}
        onChange={(next) => invoke(next ? "turn_on" : "turn_off")}
      />
      {#if isDimmable && levelDP}
        <NumericInputFeature
          value={level}
          color={tileColor}
          disabled={!levelDP.operations.write}
          label={t("cdp.light.brightness")}
          onChange={(v) => invoke("set_brightness", { brightness: v })}
        />
      {/if}
      {#if hasHsvColor && hasColorTemp}
        <ControlButtonGroup>
          <ControlButton
            active={mode === "color"}
            color={tileColor}
            label={t("cdp.light.color")}
            onClick={() => (mode = "color")}
          >{t("cdp.light.color")}</ControlButton>
          <ControlButton
            active={mode === "temp"}
            color={tileColor}
            label={t("cdp.light.white")}
            onClick={() => (mode = "temp")}
          >{t("cdp.light.white")}</ControlButton>
        </ControlButtonGroup>
      {/if}
      {#if mode === "color" && hasHsvColor && hueDP && satDP}
        <div class="flex flex-col gap-1">
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("cdp.light.hue")}</span>
          <ControlHueSlider
            value={hue}
            min={0}
            max={360}
            disabled={!hueDP.operations.write}
            label={t("cdp.light.hue")}
            onChange={(v) => invoke("set_color", { hue: v, saturation: saturation * 100 })}
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("cdp.light.saturation")}</span>
          <ControlSaturationSlider
            value={saturation}
            {hue}
            disabled={!satDP.operations.write}
            label={t("cdp.light.saturation")}
            onChange={(v) => invoke("set_color", { hue, saturation: v * 100 })}
          />
        </div>
      {/if}
      {#if mode === "temp" && hasColorTemp && ctDP}
        <div class="flex flex-col gap-1">
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{t("cdp.light.color_temp")}</span>
          <ControlColorTempSlider
            value={kelvin}
            disabled={!ctDP.operations.write}
            label={t("cdp.light.color_temp")}
            onChange={(v) => invoke("set_color_temperature", { kelvin: v })}
          />
        </div>
      {/if}
      {#if hasFixedColor && colorDP && colorOptions.length > 0}
        <ControlColorPalette
          value={colorDP.value as string | number | undefined}
          options={colorOptions}
          disabled={!colorDP.operations.write}
          label={t("cdp.light.color")}
          onChange={(v) => {
            // The label, never its position: the COLOR descriptor orders its
            // value list by the RGB bit pattern, so the index the palette sits
            // at is not the slot number the daemon's colour enum uses.
            invoke("set_color", { label: v });
          }}
        />
      {/if}
      {#if hasEffects && effectDP && effectOptions.length > 0}
        <ControlEnumSelect
          value={effectDP.value as string | number | undefined}
          options={effectOptions}
          disabled={!effectDP.operations.write}
          label={t("cdp.light.effect")}
          onChange={(v) => invoke("set_effect", { label: v })}
        />
      {/if}
    {/snippet}
  </ControlTile>
{/if}
