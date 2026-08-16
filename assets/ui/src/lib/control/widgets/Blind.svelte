<!--
  CONTROL widget for the BLIND / JALOUSIE / SHUTTER / WINDOW
  families. Tile + open/stop/close buttons + LEVEL position slider.
  When the channel additionally exposes a slat-tilt slot (LEVEL_2 on
  HmIP blinds, LEVEL_SLATS on RF jalousies) a second slider drives
  the tilt. STOP is a write-only action; LEVEL / LEVEL_2 / LEVEL_SLATS
  are bi-directional.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import CoverOpenCloseFeature from "../features/CoverOpenCloseFeature.svelte";
  import NumericInputFeature from "../features/NumericInputFeature.svelte";
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
  const stopDP = $derived(resolved.slots.STOP);
  // HmIP blinds expose LEVEL_2 for slat tilt; RF jalousies use
  // LEVEL_SLATS. Either is shown as a second tilt slider when present.
  const tiltDP = $derived(resolved.slots.LEVEL_2 ?? resolved.slots.LEVEL_SLATS);
  const tiltSlot = $derived(resolved.slots.LEVEL_2 ? "LEVEL_2" : "LEVEL_SLATS");

  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const tilt = $derived(typeof tiltDP?.value === "number" ? tiltDP.value : 0);
  const observed = $derived(levelDP?.observed ?? false);
  const writable = $derived(levelDP?.operations.write ?? false);
  const tiltWritable = $derived(tiltDP?.operations.write ?? false);

  const tileColor = $derived(resolveTileColor(resolved.family, level, observed));
  // Fully open (LEVEL 1.0) is an open cover, same as any intermediate
  // position — the icon must not fall back to the closed look there.
  const isOpen = $derived(level > 0);

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    if (!observed) return "—";
    const pct = `${Math.round(level * 100)} % ${t("blind.pct_open")}`;
    if (tiltDP?.observed) return `${pct} · ${t("blind.label.slats")} ${Math.round(tilt * 100)} %`;
    return pct;
  });
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={isOpen} label={title}>
      <Icon name="mdi:sliders" size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    <CoverOpenCloseFeature
      color={tileColor}
      canOpen={writable}
      canStop={stopDP?.operations.write ?? false}
      canClose={writable}
      onOpen={() => onSetSlot("LEVEL", 1)}
      onStop={() => onSetSlot("STOP", true)}
      onClose={() => onSetSlot("LEVEL", 0)}
    />
    {#if levelDP}
      <NumericInputFeature
        value={level}
        color={tileColor}
        disabled={!writable}
        label={t("blind.label.position")}
        onChange={(v) => onSetSlot("LEVEL", v)}
      />
    {/if}
    {#if tiltDP}
      <NumericInputFeature
        value={tilt}
        color={tileColor}
        disabled={!tiltWritable}
        label={t("blind.label.slats")}
        onChange={(v) => onSetSlot(tiltSlot, v)}
      />
    {/if}
  {/snippet}
</ControlTile>
