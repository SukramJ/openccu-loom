<!--
  Mirrors HA frontend's hui-cover-open-close-card-feature
  (frontend/src/panels/lovelace/card-features/hui-cover-open-close-card-feature.ts,
  Apache-2.0). Three-button group: open ↑ / stop ⏹ / close ↓ for
  the BLIND family.
-->
<script lang="ts">
  import ControlButtonGroup from "../controls/ControlButtonGroup.svelte";
  import ControlButton from "../controls/ControlButton.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    color: string;
    canOpen: boolean;
    canStop: boolean;
    canClose: boolean;
    onOpen: () => void;
    onStop: () => void;
    onClose: () => void;
  };

  let { color, canOpen, canStop, canClose, onOpen, onStop, onClose }: Props =
    $props();
</script>

<ControlButtonGroup>
  <ControlButton {color} disabled={!canOpen} label={t("cover.open")} onClick={onOpen}>
    <span aria-hidden="true">▲</span>
  </ControlButton>
  <ControlButton {color} disabled={!canStop} label={t("cover.stop")} onClick={onStop}>
    <span aria-hidden="true">■</span>
  </ControlButton>
  <ControlButton {color} disabled={!canClose} label={t("cover.close")} onClick={onClose}>
    <span aria-hidden="true">▼</span>
  </ControlButton>
</ControlButtonGroup>
