<!--
  Compact fallback for CDP tiles that have neither an observed state
  nor an operable control (freshly paired device, no CCU push
  received yet, and the backing parameter is not currently
  write-capable). Rendered instead of the full ControlTile hero +
  feature stack so an empty widget renders at its natural one-line
  height rather than a full card with an empty body — see the
  tileGridClass discussion in ChannelTiles.svelte for why that matters
  next to taller sibling tiles in the same grid row.

  Widgets that always keep at least one control operable (e.g. a lock
  or cover whose open/close buttons never depend on a live read) never
  reach this branch — see each widget's `hasControls` derivation.
-->
<script lang="ts">
  import type { IconName } from "$lib/icons";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    icon: IconName;
    title: string;
  };

  let { icon, title }: Props = $props();
</script>

<div
  class="flex items-center gap-3 rounded-xl border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2.5 shadow-[var(--ha-elevation-card)]"
  title={t("cdp.tile.no_state")}
>
  <Icon name={icon} size={18} class="shrink-0 text-[var(--ha-secondary-text-color)] opacity-70" />
  <span class="min-w-0 flex-1 truncate text-sm font-medium text-[var(--ha-primary-text-color)]">
    {title}
  </span>
  <span class="sr-only">{t("cdp.tile.no_state")}</span>
</div>
