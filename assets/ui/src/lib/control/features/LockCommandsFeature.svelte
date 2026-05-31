<!--
  Mirrors HA frontend's hui-lock-commands-card-feature +
  hui-lock-open-door-card-feature
  (frontend/src/panels/lovelace/card-features/hui-lock-*-card-feature.ts,
  Apache-2.0). Two-or-three button group: lock / unlock / open.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";

  type Props = {
    color: string;
    isLocked: boolean;
    canLock: boolean;
    canUnlock: boolean;
    canOpenDoor: boolean;
    onLock: () => void;
    onUnlock: () => void;
    onOpen: () => void;
  };

  let {
    color,
    isLocked,
    canLock,
    canUnlock,
    canOpenDoor,
    onLock,
    onUnlock,
    onOpen,
  }: Props = $props();
</script>

<ControlButtonGroup>
  <ControlButton
    active={isLocked}
    {color}
    disabled={!canLock || isLocked}
    label="Verriegeln"
    onClick={onLock}
  >
    🔒 Zu
  </ControlButton>
  <ControlButton
    active={!isLocked}
    {color}
    disabled={!canUnlock || !isLocked}
    label="Entriegeln"
    onClick={onUnlock}
  >
    🔓 Auf
  </ControlButton>
  {#if canOpenDoor}
    <ControlButton {color} label="Tür öffnen" onClick={onOpen}>🚪</ControlButton>
  {/if}
</ControlButtonGroup>
