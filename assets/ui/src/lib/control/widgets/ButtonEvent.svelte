<!--
  CONTROL widget for the BUTTON / BTN_SHORT_ONLY families. Buttons
  emit transient events (SHORT, LONG) — the DP's value bumps
  momentarily and resets. The widget surfaces this as a "last
  press" indicator with a relative timestamp.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import type { ResolvedChannel } from "../resolver";
  import ControlTile from "../tile/ControlTile.svelte";
  import ControlTileIcon from "../tile/ControlTileIcon.svelte";
  import ControlTileInfo from "../tile/ControlTileInfo.svelte";
  import { resolveTileColor } from "../state-color";

  type Props = {
    resolved: ResolvedChannel;
    title: string;
    secondary?: string;
  };

  let { resolved, title, secondary }: Props = $props();

  const shortDP = $derived(resolved.slots.SHORT);
  const longDP = $derived(resolved.slots.LONG);

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
      return { kind: "Kurz", at: shortAt };
    }
    if (longAt) {
      return { kind: "Lang", at: longAt };
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
    if (dt < 5) return `${lastPress.kind} jetzt`;
    if (dt < 60) return `${lastPress.kind} vor ${dt} Sek.`;
    if (dt < 3600) return `${lastPress.kind} vor ${Math.floor(dt / 60)} Min.`;
    if (dt < 86400) return `${lastPress.kind} vor ${Math.floor(dt / 3600)} Std.`;
    return `${lastPress.kind} vor ${Math.floor(dt / 86400)} Tg.`;
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
</ControlTile>
