<script lang="ts">
  import { onMount } from "svelte";
  import {
    subscribe,
    status as wsStatus,
    diagnostics,
  } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";

  // Tiny dot in the topbar that mirrors the WebSocket pump's
  // current state. Subscribes to the shared multiplexer so the WS
  // is kept open while at least one consumer is active anyway —
  // we just piggyback on the state transitions pushed by onStateChange.

  onMount(() => {
    // Keep the pump alive while the badge is mounted.
    const unsub = subscribe(() => {});
    return unsub;
  });

  // wsStatus() reads the reactive $state inside events.svelte.ts;
  // Svelte's $derived tracks it and re-runs whenever the state changes.
  const wsState: "connecting" | "open" | "closed" = $derived(wsStatus());

  const label = $derived(
    wsState === "open"
      ? t("diagnostics.connected")
      : wsState === "connecting"
        ? t("connection.reconnecting")
        : t("diagnostics.disconnected"),
  );

  const dotClass = $derived(
    wsState === "open"
      ? "bg-emerald-500"
      : wsState === "connecting"
        ? "bg-amber-500 animate-pulse"
        : "bg-red-500",
  );

  const diag = $derived(diagnostics());
  const tooltip = $derived(
    `WebSocket: ${label} · ${diag.received} events${diag.lastType ? ` · last: ${diag.lastType}` : ""}`,
  );
</script>

<span
  class="inline-flex items-center gap-1.5 rounded-md border border-slate-200 px-2 py-1 text-xs dark:border-slate-700"
  title={tooltip}
>
  <span class="inline-block h-2 w-2 rounded-full {dotClass}" aria-hidden="true"></span>
  <span class="hidden sm:inline">{label}</span>
  {#if diag.received > 0}
    <span
      class="rounded-full px-1.5 text-[10px] font-mono tabular-nums"
      style="background-color: var(--ha-secondary-background-color); color: var(--ha-secondary-text-color);"
      title={t("ui.events_since_connect")}
    >
      {diag.received}
    </span>
  {/if}
</span>
