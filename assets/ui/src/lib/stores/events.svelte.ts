// Shared WebSocket pump. Lazily opens the daemon event stream the
// first time someone calls `subscribe(...)`, multicasts every
// envelope to all registered handlers and cleans up on the last
// unsubscribe so stale browser tabs don't keep idle sockets open.

import { connectEvents, type EventStream } from "$lib/api/ws";
import type { EventEnvelope } from "$lib/api/types";

let stream: EventStream | null = null;
const handlers = new Set<(ev: EventEnvelope) => void>();

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
    }
  };
}

// Mirror the WS pump's readyState. Takes a `tick` argument so callers
// (Svelte components) can re-trigger derivation without us needing to
// own a reactive state ourselves — the websocket abstraction does not
// emit state-change events, so polling is the simplest route.
export function status(_tick?: number): "connecting" | "open" | "closed" {
  void _tick;
  return stream?.state() ?? "closed";
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
