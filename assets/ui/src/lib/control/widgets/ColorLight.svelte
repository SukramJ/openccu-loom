<!--
  CONTROL widget for the RGBW_COLOR / RGB_COLOR families. Tile +
  hue slider on the COLOR slot. The CCU encodes COLOR as an
  integer hue index (0–199 typical for HmIP-RGBW); the slider's
  range adapts when MIN/MAX are surfaced via the REST DTO in a
  future commit.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ControlHueSlider from "../controls/ControlHueSlider.svelte";
  import { resolveTileColor } from "../state-color";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const colorDP = $derived(resolved.slots.COLOR);
  const hue = $derived(typeof colorDP?.value === "number" ? colorDP.value : 0);
  const observed = $derived(colorDP?.observed ?? false);
  const writable = $derived(colorDP?.operations.write ?? false);

  const tileColor = $derived(resolveTileColor(resolved.family, hue, observed));

  // Hue index → CSS hsl preview. The CCU's exact mapping is
  // device-dependent; for the tile-icon preview a uniform mapping
  // is good enough.
  const hueDeg = $derived(observed ? Math.round((hue / 199) * 360) : 0);

  const computedSecondary = $derived(
    secondary ?? (observed ? `${t("control.hue")} ${hue}` : "—"),
  );
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={observed} label={title}>
      <span aria-hidden="true" style="color: hsl({hueDeg} 100% 50%);">●</span>
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if colorDP}
      <ControlHueSlider
        value={hue}
        disabled={!writable}
        label={t("control.hue")}
        onChange={(v) => onSetSlot("COLOR", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
