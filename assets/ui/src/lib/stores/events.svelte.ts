// Shared WebSocket pump. Lazily opens the daemon event stream the
// first time someone calls `subscribe(...)`, multicasts every
// envelope to all registered handlers and cleans up on the last
// unsubscribe so stale browser tabs don't keep idle sockets open.

import { connectEvents, type EventStream } from "$lib/api/ws";
import type { EventEnvelope } from "$lib/api/types";

let stream: EventStream | null = null;
const handlers = new Set<(ev: EventEnvelope) => void>();
const resyncHandlers = new Set<() => void>();

// Reactive connection state — updated via onStateChange so components
// receive transitions without polling. Starts "closed" because no
// stream has been opened yet (the pump is lazy).
const connectionState = $state<{
  current: "connecting" | "open" | "closed";
}>({ current: "closed" });

// Set when the daemon announced its own shutdown on the WebSocket before
// the socket went away. It is the one thing a closed socket cannot tell
// us on its own: "the daemon is stopping" and "your network dropped" look
// identical from this side, and only the first is worth telling the
// operator about — the second is self-healing and the badge already says
// so. Cleared as soon as a socket is open again.
const daemonStopping = $state<{ current: boolean }>({ current: false });

// Diagnostic counters — kept reactive so the ConnectionBadge can
// display a "received N events since open" hint. Helps debug live-
// update plumbing without resorting to browser-devtools poking.
const counters = $state<{
  received: number;
  lastType: string;
  resyncs: number;
  // Round-trip time to the daemon in ms, as the daemon measured it from the
  // heartbeat this client echoes. Null until the first ping interval (30s)
  // elapses on an open socket, and again on every disconnect: the next
  // socket may take a different route, so the old figure would describe a
  // connection that no longer exists.
  //
  // This is the browser→daemon leg only. It is NOT the daemon→CCU latency
  // the hub reports as `connection_latency_ms`; behind an Ingress tunnel or
  // a remote host this leg can be the larger of the two while the CCU link
  // is healthy, so the two are never summed or shown as one figure.
  latencyMs: number | null;
}>({
  received: 0,
  lastType: "",
  resyncs: 0,
  latencyMs: null,
});

function ensure(): EventStream {
  if (stream) return stream;
  stream = connectEvents();
  stream.onStateChange((s) => {
    connectionState.current = s;
    if (s === "open") {
      // A live socket means the daemon is back; whatever it announced
      // before the last disconnect no longer describes the present.
      daemonStopping.current = false;
    } else {
      counters.latencyMs = null;
    }
  });
  // Sync the initial state emitted during connect() before this
  // onStateChange registration could fire. The latency reading gets the same
  // treatment: a component mounting into an already-running pump would
  // otherwise show no latency until the next heartbeat, up to a ping interval
  // away, even though the stream has been measuring all along.
  connectionState.current = stream.state();
  counters.latencyMs = stream.latencyMs();
  stream.onMessage((ev) => {
    counters.received += 1;
    counters.lastType = ev.type ?? "";
    if (ev.type === "daemon_status.changed") {
      const status = (ev.payload as { status?: string } | undefined)?.status;
      daemonStopping.current = status === "offline";
    }
    for (const h of handlers) h(ev);
  });
  stream.onResync(() => {
    counters.resyncs += 1;
    for (const h of resyncHandlers) h();
  });
  stream.onLatency((rttMs) => {
    counters.latencyMs = rttMs;
  });
  return stream;
}

/**
 * Register a callback for the daemon's resync signal: the event stream
 * cannot bring this client to the current state, so anything loaded
 * over REST has to be loaded again.
 *
 * A view that keeps server state should pair its initial load with one
 * of these:
 *
 *     onMount(() => onResync(() => void load()));
 *
 * The daemon signals after a boot snapshot (it writes the model to
 * MQTT's retained topics rather than replaying every data point into
 * the live stream) and whenever a connection fell far enough behind
 * that queued events were dropped. Returns an unsubscribe function.
 */
export function onResync(handler: () => void): () => void {
  ensure();
  resyncHandlers.add(handler);
  return () => {
    resyncHandlers.delete(handler);
  };
}

export function subscribe(
  handler: (ev: EventEnvelope) => void,
): () => void {
  ensure();
  handlers.add(handler);
  return () => {
    handlers.delete(handler);
    if (handlers.size === 0) {
      stream?.close();
      stream = null;
      connectionState.current = "closed";
    }
  };
}

/**
 * Force-closes the pump's socket regardless of how many handlers are
 * still registered. The ref-counted teardown in `subscribe()`'s
 * unsubscribe only fires once every handler unsubscribes — a store
 * that binds once for the lifetime of the tab (e.g. `maintenanceStore`)
 * never lets that count reach zero, so on its own the pump would keep
 * reconnecting a socket the daemon rejects (401) for the rest of the
 * tab's life after logout or session expiry. Call this whenever the
 * session ends; the next `subscribe()`/`onResync()` reopens the stream
 * lazily against the (by then re-authenticated) session.
 */
export function shutdown(): void {
  stream?.close();
  stream = null;
  connectionState.current = "closed";
}

/**
 * Reactive connection state of the WS pump. Returns a getter so
 * Svelte's reactivity graph tracks reads inside `$derived` blocks.
 * Components call `wsState()` (no arguments needed — no tick polling).
 */
export function status(): "connecting" | "open" | "closed" {
  return connectionState.current;
}

/**
 * Reactive read of whether the daemon announced its own shutdown before
 * the stream went away. False whenever a socket is open.
 *
 * A closed socket alone says nothing about the cause: a stopping daemon
 * and a dropped network look the same from the browser. The daemon says
 * which it is while it still can (`daemon_status.changed`), and a stop it
 * could not announce — a killed process — correctly reads as an ordinary
 * disconnect here.
 */
export function daemonIsStopping(): boolean {
  return daemonStopping.current;
}

/**
 * Reactive read of the event-pump diagnostics. The ConnectionBadge
 * exposes them as a tooltip so a glance at the topbar tells you
 * whether events are flowing — handy for debugging cases where the
 * server is up but the SPA isn't refreshing.
 */
export function diagnostics(): {
  received: number;
  lastType: string;
  resyncs: number;
  latencyMs: number | null;
} {
  return counters;
}
