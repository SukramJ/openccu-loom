// Shared WebSocket pump. Lazily opens the daemon event stream the
// first time someone calls `subscribe(...)`, multicasts every
// envelope to all registered handlers and cleans up on the last
// unsubscribe so stale browser tabs don't keep idle sockets open.

import { connectEvents, type EventStream } from "$lib/api/ws";
import type { EventEnvelope } from "$lib/api/types";

let stream: EventStream | null = null;
const handlers = new Set<(ev: EventEnvelope) => void>();

// Reactive connection state — updated via onStateChange so components
// receive transitions without polling. Starts "closed" because no
// stream has been opened yet (the pump is lazy).
const connectionState = $state<{
  current: "connecting" | "open" | "closed";
}>({ current: "closed" });

// Diagnostic counters — kept reactive so the ConnectionBadge can
// display a "received N events since open" hint. Helps debug live-
// update plumbing without resorting to browser-devtools poking.
const counters = $state<{ received: number; lastType: string }>({
  received: 0,
  lastType: "",
});

function ensure(): EventStream {
  if (stream) return stream;
  stream = connectEvents();
  stream.onStateChange((s) => {
    connectionState.current = s;
  });
  // Sync the initial state emitted during connect() before this
  // onStateChange registration could fire.
  connectionState.current = stream.state();
  stream.onMessage((ev) => {
    counters.received += 1;
    counters.lastType = ev.type ?? "";
    for (const h of handlers) h(ev);
  });
  return stream;
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
 * Reactive connection state of the WS pump. Returns a getter so
 * Svelte's reactivity graph tracks reads inside `$derived` blocks.
 * Components call `wsState()` (no arguments needed — no tick polling).
 */
export function status(): "connecting" | "open" | "closed" {
  return connectionState.current;
}

/**
 * Reactive read of the event-pump diagnostics. The ConnectionBadge
 * exposes them as a tooltip so a glance at the topbar tells you
 * whether events are flowing — handy for debugging cases where the
 * server is up but the SPA isn't refreshing.
 */
export function diagnostics(): { received: number; lastType: string } {
  return counters;
}
