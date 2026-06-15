<!--
  Mirrors HA frontend's hui-toggle-card-feature
  (frontend/src/panels/lovelace/card-features/hui-toggle-card-feature.ts,
  Apache-2.0). Two-button group: Off / On, single-press = set state.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import { t } from "$lib/i18n";

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
    labelOff,
    labelOn,
    onChange,
  }: Props = $props();

  const offLabel = $derived(labelOff ?? t("quick.off"));
  const onLabel = $derived(labelOn ?? t("quick.on"));
</script>

<ControlButtonGroup>
  <ControlButton
    active={!value}
    {color}
    {disabled}
    label={offLabel}
    onClick={() => onChange(false)}
  >
    {offLabel}
  </ControlButton>
  <ControlButton
    active={value}
    {color}
    {disabled}
    label={onLabel}
    onClick={() => onChange(true)}
  >
    {onLabel}
  </ControlButton>
</ControlButtonGroup>
