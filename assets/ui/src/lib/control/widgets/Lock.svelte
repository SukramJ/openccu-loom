<!--
  CONTROL widget for the LOCK / DOOROPENER family. Tile + lock /
  unlock / open-door buttons. STATE = true means locked.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import LockCommandsFeature from "../features/LockCommandsFeature.svelte";
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
  const openDP = $derived(resolved.slots.OPEN);
  const uncertainDP = $derived(resolved.slots.UNCERTAIN);

  const isLocked = $derived(Boolean(stateDP?.value));
  const observed = $derived(stateDP?.observed ?? false);
  const writable = $derived(stateDP?.operations.write ?? false);

  // unlocked = active (HA convention — red when open/unlocked).
  const tileColor = $derived(
    resolveTileColor(resolved.family, !isLocked, observed),
  );

  const computedSecondary = $derived.by(() => {
    if (secondary) return secondary;
    if (uncertainDP?.value) return t("control.status_unknown");
    if (!observed) return "—";
    return isLocked ? t("control.locked") : t("control.unlocked");
  });
</script>

<ControlTile {tileColor}>
  {#snippet icon()}
    <ControlTileIcon active={!isLocked} label={title}>
      <Icon name={isLocked ? "mdi:lock" : "mdi:lock-open"} size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if stateDP}
      <LockCommandsFeature
        color={tileColor}
        {isLocked}
        canLock={writable}
        canUnlock={writable}
        canOpenDoor={openDP?.operations.write ?? false}
        onLock={() => onSetSlot("STATE", true)}
        onUnlock={() => onSetSlot("STATE", false)}
        onOpen={() => onSetSlot("OPEN", true)}
      />
    {/if}
  {/snippet}
</ControlTile>
