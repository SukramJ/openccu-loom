<!--
  CONTROL widget for the DOOR_RECEIVER family (HmIP-MOD-HO, HmIP-MOD-TM).
  Different shape from the regular cover/blind: no LEVEL slider, two
  ENUM slots instead — DOOR_COMMAND drives the door, DOOR_STATE
  reports back. The CCU enums include sentinels (NOP, POSITION_UNKNOWN)
  that aren't user-facing.

  Layout mirrors HA frontend's cover-card with action buttons
  (frontend/src/panels/lovelace/card-features/hui-cover-open-close-card-feature.ts,
  Apache-2.0), extended with a "Lüften" (ventilation) press for the
  HmIP garage door's PARTIAL_OPEN command.
-->
<script lang="ts">
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { resolveTileColor } from "../state-color";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const cmdDP = $derived(resolved.slots.DOOR_COMMAND);
  const stateDP = $derived(resolved.slots.DOOR_STATE);

  const writable = $derived(cmdDP?.operations.write ?? false);
  const stateLabel = $derived<string>(
    typeof stateDP?.value === "string" ? stateDP.value : "",
  );

  // CCU DOOR_STATE labels: CLOSED / OPEN / VENTILATION_POSITION /
  // POSITION_UNKNOWN. Map to German for the secondary line.
  const STATE_DE: Record<string, string> = {
    CLOSED: "Geschlossen",
    OPEN: "Offen",
    VENTILATION_POSITION: "Lüftet",
    POSITION_UNKNOWN: "Unbekannt",
  };
  const isOpen = $derived(stateLabel === "OPEN" || stateLabel === "VENTILATION_POSITION");
  const observed = $derived(stateDP?.observed ?? false);

  const tileColor = $derived(
    resolveTileColor(resolved.family, isOpen, observed),
  );

  const computedSecondary = $derived(
    secondary ?? (observed && stateLabel in STATE_DE ? STATE_DE[stateLabel] : "—"),
  );

  // DOOR_COMMAND value_list is ["NOP","OPEN","STOP","CLOSE","PARTIAL_OPEN"].
  // NOP is a sentinel (CCU echoes it after a command completes) — never
  // surfaced as a button. The remaining four map onto open/stop/close/vent.
  const COMMANDS = ["OPEN", "STOP", "CLOSE", "PARTIAL_OPEN"] as const;
  const COMMAND_LABELS: Record<(typeof COMMANDS)[number], string> = {
    OPEN: "Öffnen",
    STOP: "Halt",
    CLOSE: "Schließen",
    PARTIAL_OPEN: "Lüften",
  };
  const availableCommands = $derived<readonly (typeof COMMANDS)[number][]>(
    cmdDP?.value_list
      ? COMMANDS.filter((c) => (cmdDP.value_list as string[]).includes(c))
      : COMMANDS,
  );

  function isActive(cmd: string): boolean {
    if (!observed) return false;
    if (cmd === "OPEN") return stateLabel === "OPEN";
    if (cmd === "CLOSE") return stateLabel === "CLOSED";
    if (cmd === "PARTIAL_OPEN") return stateLabel === "VENTILATION_POSITION";
    return false;
  }
</script>

<ControlTile {tileColor} focused={isOpen}>
  {#snippet icon()}
    <ControlTileIcon active={isOpen} label={title}>
      <Icon name={isOpen ? "mdi:sliders" : "mdi:door-closed"} size={22} />
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if cmdDP}
      <ControlButtonGroup>
        {#each availableCommands as cmd (cmd)}
          <ControlButton
            active={isActive(cmd)}
            color={tileColor}
            disabled={!writable}
            label={COMMAND_LABELS[cmd]}
            onClick={() => onSetSlot("DOOR_COMMAND", cmd)}
          >
            {COMMAND_LABELS[cmd]}
          </ControlButton>
        {/each}
      </ControlButtonGroup>
    {/if}
  {/snippet}
</ControlTile>
