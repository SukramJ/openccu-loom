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
    /** Device or channel name included in the aria-label for screen readers. */
    title?: string;
    onChange: (next: boolean) => void;
  };

  let {
    value,
    color,
    disabled = false,
    labelOff,
    labelOn,
    title,
    onChange,
  }: Props = $props();

  const offLabel = $derived(labelOff ?? t("quick.off"));
  const onLabel = $derived(labelOn ?? t("quick.on"));

  // aria-label: "Kitchen lamp — Off" / "Kitchen lamp — On"
  // When no title is available fall back to label-only so the button
  // is still announced correctly by screen readers.
  const offAriaLabel = $derived(
    title ? `${title} — ${offLabel}` : offLabel,
  );
  const onAriaLabel = $derived(
    title ? `${title} — ${onLabel}` : onLabel,
  );
</script>

<ControlButtonGroup>
  <ControlButton
    active={!value}
    {color}
    {disabled}
    label={offAriaLabel}
    onClick={() => onChange(false)}
  >
    {offLabel}
  </ControlButton>
  <ControlButton
    active={value}
    {color}
    {disabled}
    label={onAriaLabel}
    onClick={() => onChange(true)}
  >
    {onLabel}
  </ControlButton>
</ControlButtonGroup>
