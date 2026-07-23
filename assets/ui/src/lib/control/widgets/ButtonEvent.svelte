<!--
  CONTROL widget for the BUTTON / BTN_SHORT_ONLY families. Buttons
  emit transient events (SHORT, LONG) — the DP's value bumps
  momentarily and resets. The widget surfaces this as a "last
  press" indicator with a relative timestamp and, when a press slot
  is writable (virtual remotes, writable BidCos keys), renders
  short / long press buttons that perform the single boolean write
  the CCU WebUI's own key control issues per click.
-->
<script lang="ts">
  import { onMount } from "svelte";
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
    onSetSlot: (slot: string, value: unknown) => void;
  };

  let { resolved, title, secondary, onSetSlot }: Props = $props();

  const shortDP = $derived(resolved.slots.SHORT);
  const longDP = $derived(resolved.slots.LONG);

  // A press slot is clickable when the daemon reports it writable AND
  // usage-visible ("data_point") — the same gate the MQTT press-button
  // discovery applies, so every surface offers the identical key set.
  function pressable(dp: { operations?: { write?: boolean }; usage?: string } | undefined): boolean {
    return Boolean(dp?.operations?.write) && dp?.usage === "data_point";
  }

  const shortPressable = $derived(pressable(shortDP));
  const longPressable = $derived(pressable(longDP));

  function recentMs(dp: { modified_at?: string } | undefined): number | null {
    if (!dp?.modified_at) return null;
    const t = Date.parse(dp.modified_at);
    if (!Number.isFinite(t)) return null;
    return t;
  }

  const shortAt = $derived(recentMs(shortDP));
  const longAt = $derived(recentMs(longDP));

  // Pick whichever fired most recently for the secondary label.
  const lastPress = $derived.by(() => {
    if (shortAt && (!longAt || shortAt > longAt)) {
      return { kind: t("remote.press_short"), at: shortAt };
    }
    if (longAt) {
      return { kind: t("remote.press_long"), at: longAt };
    }
    return null;
  });

  // Live ticker so the relative timestamp animates.
  let nowMs = $state(Date.now());
  onMount(() => {
    const id = setInterval(() => {
      nowMs = Date.now();
    }, 1000);
    return () => clearInterval(id);
  });

  const relative = $derived.by(() => {
    if (!lastPress) return "—";
    const dt = Math.max(0, Math.floor((nowMs - lastPress.at) / 1000));
    if (dt < 5) return t("remote.press_just_now", { kind: lastPress.kind });
    if (dt < 60) return t("remote.press_ago_sec", { kind: lastPress.kind, n: dt });
    if (dt < 3600) return t("remote.press_ago_min", { kind: lastPress.kind, n: Math.floor(dt / 60) });
    if (dt < 86400) return t("remote.press_ago_hour", { kind: lastPress.kind, n: Math.floor(dt / 3600) });
    return t("remote.press_ago_day", { kind: lastPress.kind, n: Math.floor(dt / 86400) });
  });

  const fresh = $derived(
    lastPress ? nowMs - lastPress.at < 5_000 : false,
  );
  const tileColor = $derived(
    resolveTileColor(resolved.family, fresh, lastPress !== null),
  );

  const computedSecondary = $derived(secondary ?? relative);
</script>

<ControlTile {tileColor} focused={fresh}>
  {#snippet icon()}
    <ControlTileIcon active={fresh} label={title}>
      <span aria-hidden="true">🔘</span>
    </ControlTileIcon>
  {/snippet}
  {#snippet info()}
    <ControlTileInfo primary={title} secondary={computedSecondary} />
  {/snippet}
  {#snippet features()}
    {#if shortPressable || longPressable}
      <div class="flex gap-1.5">
        {#if shortPressable}
          <button
            type="button"
            class="min-h-9 flex-1 rounded-md border border-[var(--ha-divider-color)] px-2 py-1 text-xs text-[var(--ha-primary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)]"
            aria-label={t("remote.press_short_title", { title })}
            onclick={() => onSetSlot("SHORT", true)}
          >
            {t("remote.press_short")}
          </button>
        {/if}
        {#if longPressable}
          <button
            type="button"
            class="min-h-9 flex-1 rounded-md border border-[var(--ha-divider-color)] px-2 py-1 text-xs text-[var(--ha-primary-text-color)] transition hover:bg-[var(--ha-secondary-background-color)]"
            aria-label={t("remote.press_long_title", { title })}
            onclick={() => onSetSlot("LONG", true)}
          >
            {t("remote.press_long")}
          </button>
        {/if}
      </div>
    {/if}
  {/snippet}
</ControlTile>
