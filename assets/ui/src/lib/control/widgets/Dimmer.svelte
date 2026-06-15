<!--
  CONTROL widget for the DIMMER family (CCU CONTROL prefixes DIMMER
  and DIMMER_REAL). Composes a ControlTile with a state-coloured
  icon, primary/secondary text, and two card-features:
  ToggleFeature (off ⇄ on) + NumericInputFeature (level 0–100 %).

  Layout pattern from frontend/src/panels/lovelace/cards/hui-tile-card.ts
  (Apache-2.0). The LEVEL slot drives both features — the toggle
  reads/writes a derived 0/1, the slider reads/writes the continuous
  value.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ToggleFeature from "../features/ToggleFeature.svelte";
  import NumericInputFeature from "../features/NumericInputFeature.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    /** Generic slot writer shared with every other CONTROL widget so
     *  the registry can call them uniformly. The widget always writes
     *  the `LEVEL` slot. */
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const levelDP = $derived(resolved.slots.LEVEL);
  const level = $derived(typeof levelDP?.value === "number" ? levelDP.value : 0);
  const observed = $derived(levelDP?.observed ?? false);
  const writable = $derived(levelDP?.operations.write ?? false);

  const tileColor = $derived(resolveTileColor(resolved.family, level, observed));
  const isOn = $derived(level > 0);

  const computedSecondary = $derived(
    secondary ?? (observed ? `${Math.round(level * 100)} %` : "—"),
  );
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
      label={t("control.brightness")}
      onChange={(v) => onSetSlot("LEVEL", v)}
    />
  {/snippet}
</ControlTile>
