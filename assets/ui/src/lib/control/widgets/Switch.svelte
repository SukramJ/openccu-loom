<!--
  CONTROL widget for the SWITCH family. Tile + state-coloured icon
  + Off/On button group on the STATE slot.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ToggleFeature from "../features/ToggleFeature.svelte";
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

  const stateDP = $derived(resolved.slots.STATE);
  const isOn = $derived(Boolean(stateDP?.value));
  const observed = $derived(stateDP?.observed ?? false);
  const writable = $derived(stateDP?.operations.write ?? false);
  const tileColor = $derived(resolveTileColor(resolved.family, isOn, observed));

  const computedSecondary = $derived(
    secondary ?? (observed ? (isOn ? t("quick.on") : t("quick.off")) : "—"),
  );
</script>

<ControlTile {tileColor} focused={isOn}>
  {#snippet icon()}
    <ControlTileIcon active={isOn} label={title}>
      <Icon name="mdi:power" size={22} />
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
        disabled={!writable}
        {title}
        onChange={(v) => onSetSlot("STATE", v)}
      />
    {/if}
  {/snippet}
</ControlTile>
