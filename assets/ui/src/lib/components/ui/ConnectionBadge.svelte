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
  // we just piggyback on the readyState updates.

  let tick: number = $state(0);

  onMount(() => {
    // Force re-evaluation every 2s so the badge picks up state
    // changes the WS layer doesn't push by itself (the mock
    // event-bus has no "status changed" event yet).
    const id = setInterval(() => {
      tick = Date.now();
    }, 2000);
    // Keep the pump alive while the badge is mounted.
    const unsub = subscribe(() => {});
    return () => {
      clearInterval(id);
      unsub();
    };
  });

  const wsState: "connecting" | "open" | "closed" = $derived(wsStatus(tick));

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
      title="Events seit Verbindungsaufbau"
    >
      {diag.received}
    </span>
  {/if}
</span>
