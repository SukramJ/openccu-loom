<script lang="ts">
  import { onMount } from "svelte";
  import {
    subscribe,
    status as wsStatus,
    diagnostics,
    daemonIsStopping,
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

  // The daemon announced its own stop before the socket went away. Without
  // this the badge shows the ordinary self-healing "reconnecting", which
  // reads as "wait a moment" when what actually happened is that the thing
  // being waited for is gone.
  const stopping = $derived(daemonIsStopping() && wsState !== "open");

  // This badge reflects the live-update (WebSocket) stream, not the CCU
  // link — label it as such so a red dot doesn't read as a CCU outage.
  const label = $derived(
    stopping
      ? t("connection.daemon_stopping")
      : wsState === "open"
        ? t("connection.live_on")
        : wsState === "connecting"
          ? t("connection.reconnecting")
          : t("connection.live_off"),
  );

  // A disconnected live-stream is an expected, self-healing state (the pump
  // reconnects on its own), so it must not read as a hard failure: amber on a
  // neutral slate badge, never red. Red stays reserved for genuine errors.
  const dotClass = $derived(
    wsState === "open"
      ? "bg-emerald-500"
      : wsState === "connecting"
        ? "bg-amber-500 animate-pulse"
        : "bg-amber-500 dark:bg-amber-400",
  );

  // Plain-language explanation of the current state — carried in the title
  // and the aria-label so the meaning survives even on phones, where the
  // text label collapses to just the coloured dot.
  const explain = $derived(
    stopping
      ? t("connection.tooltip.daemon_stopping")
      : wsState === "open"
        ? t("connection.tooltip.on")
        : wsState === "connecting"
          ? t("connection.tooltip.connecting")
          : t("connection.tooltip.off"),
  );

  const diag = $derived(diagnostics());

  // Round-trip to the daemon, measured by the daemon from the heartbeat this
  // browser echoes back. It describes THIS connection — a tab on the LAN and
  // one behind an Ingress tunnel read different numbers from the same daemon
  // — which is why it belongs on this badge and not on any CCU surface. Sub-
  // millisecond readings are shown with one decimal so a local connection
  // does not collapse to a bare "0 ms".
  const latency = $derived(
    diag.latencyMs === null
      ? ""
      : ` · ${diag.latencyMs < 10 ? diag.latencyMs.toFixed(1) : Math.round(diag.latencyMs)} ${t("connection.rtt_unit")}`,
  );
  const tooltip = $derived(
    `${explain}${latency}${diag.received ? ` · ${diag.received} ${t("connection.events")}` : ""}${diag.lastType ? ` · ${t("connection.last")}: ${diag.lastType}` : ""}`,
  );
  const ariaLabel = $derived(`${label} — ${explain}`);
</script>

<span
  class="inline-flex items-center gap-1.5 rounded-md border border-slate-200 px-2 py-1 text-xs dark:border-slate-700"
  title={tooltip}
  aria-label={ariaLabel}
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
