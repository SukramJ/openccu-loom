<!--
  Generic read-only widget for binary-state CONTROL families
  (BUTTON, DANGER, SMOKE_DETECTOR, WIN_SC, DOOR_SENSOR,
  MOTIONDETECTOR_TRANSCEIVER, BATTERIE, RHS, WATER_DETECTION_TRANSMITTER, …).
  Tile with a state-coloured icon flipping its glyph between
  active and inactive variants.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import { resolveTileColor } from "../state-color";
  import { t } from "$lib/i18n";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    /** Slot suffix to read. Defaults to "STATE". */
    slot?: string;
    /** Glyph when value is true / "active". */
    glyphActive?: string;
    /** Glyph when value is false / "idle". */
    glyphIdle?: string;
    /** Label when active (e.g. "Bewegung", "Offen", "Alarm"). */
    labelActive?: string;
    /** Label when idle (e.g. "Ruhe", "Geschlossen"). */
    labelIdle?: string;
  };

  let {
    resolved,
    title,
    secondary,
    slot = "STATE",
    glyphActive = "●",
    glyphIdle = "○",
    labelActive,
    labelIdle,
  }: Props = $props();

  const activeLabel = $derived(labelActive ?? t("control.active"));
  const idleLabel = $derived(labelIdle ?? t("control.idle"));

  const dp = $derived(resolved.slots[slot]);
  const isActive = $derived(Boolean(dp?.value));
  const observed = $derived(dp?.observed ?? false);
  const tileColor = $derived(resolveTileColor(resolved.family, isActive, observed));

  const computedSecondary = $derived(
    secondary ?? (observed ? (isActive ? activeLabel : idleLabel) : "—"),
  );
</script>

<ControlTile {tileColor} focused={isActive && observed}>
  {#snippet icon()}
    <ControlTileIcon active={isActive && observed} label={title}>
      <span aria-hidden="true">{isActive ? glyphActive : glyphIdle}</span>
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
</ControlTile>
