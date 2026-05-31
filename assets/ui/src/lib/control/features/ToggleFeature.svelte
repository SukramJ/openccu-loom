<!--
  Mirrors HA frontend's hui-toggle-card-feature
  (frontend/src/panels/lovelace/card-features/hui-toggle-card-feature.ts,
  Apache-2.0). Two-button group: Off / On, single-press = set state.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";

  type Props = {
    value: boolean;
    color: string;
    disabled?: boolean;
    labelOff?: string;
    labelOn?: string;
    onChange: (next: boolean) => void;
  };

  let {
    value,
    color,
    disabled = false,
    labelOff = "Aus",
    labelOn = "An",
    onChange,
  }: Props = $props();
</script>

<ControlButtonGroup>
  <ControlButton
    active={!value}
    {color}
    {disabled}
    label={labelOff}
    onClick={() => onChange(false)}
  >
    {labelOff}
  </ControlButton>
  <ControlButton
    active={value}
    {color}
    {disabled}
    label={labelOn}
    onClick={() => onChange(true)}
  >
    {labelOn}
  </ControlButton>
</ControlButtonGroup>
