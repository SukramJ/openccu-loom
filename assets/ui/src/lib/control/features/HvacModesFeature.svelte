<!--
  Mirrors HA frontend's hui-climate-hvac-modes-card-feature
  (frontend/src/panels/lovelace/card-features/hui-climate-hvac-modes-card-feature.ts,
  Apache-2.0). Segmented button-group for the CONTROL_MODE / mode
  enum slot. Each option is rendered as a button; the active mode
  fills with the state colour.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";

  type Option = {
    /** Wire value sent to the CCU on selection. */
    value: number | string;
    /** Display label rendered inside the button. */
    label: string;
  };

  type Props = {
    /** Currently selected option's `value`. */
    value: number | string;
    options: Option[];
    color: string;
    disabled?: boolean;
    onChange: (value: number | string) => void;
  };

  let { value, options, color, disabled = false, onChange }: Props = $props();
</script>

<ControlButtonGroup>
  {#each options as opt (opt.value)}
    <ControlButton
      active={opt.value === value}
      {color}
      {disabled}
      label={opt.label}
      onClick={() => onChange(opt.value)}
    >
      {opt.label}
    </ControlButton>
  {/each}
</ControlButtonGroup>
