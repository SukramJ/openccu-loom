<!--
  CONTROL widget for the DUAL_WHITE_COLOR / COLORTEMP families.
  Tile + kelvin slider on the COLOR_TEMPERATURE slot. Defaults
  to 2000–6500 K; the actual descriptor range will replace these
  bounds when MIN/MAX show up in the REST DTO.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ControlColorTempSlider from "../controls/ControlColorTempSlider.svelte";
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

  const ctDP = $derived(
    resolved.slots.COLOR_TEMPERATURE ?? resolved.slots.COLORTEMPERATURE,
  );
  const kelvin = $derived(typeof ctDP?.value === "number" ? ctDP.value : 3000);
  const observed = $derived(ctDP?.observed ?? false);
  const writable = $derived(ctDP?.operations.write ?? false);

  const tileColor = $derived(resolveTileColor(resolved.family, kelvin, observed));

  const computedSecondary = $derived(
    secondary ?? (observed ? `${Math.round(kelvin)} K` : "—"),
  );
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={observed} label={title}>
      <Icon name="mdi:sun" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if ctDP}
      <ControlColorTempSlider
        value={kelvin}
        disabled={!writable}
        label={t("control.color_temp")}
        onChange={(v) => onSetSlot(ctDP === resolved.slots.COLOR_TEMPERATURE ? "COLOR_TEMPERATURE" : "COLORTEMPERATURE", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
