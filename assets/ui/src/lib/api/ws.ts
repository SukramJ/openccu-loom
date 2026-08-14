import type { EventEnvelope } from "./types";
import { apiBase } from "./base";

/**
 * Small WebSocket client with exponential-backoff reconnect. Auth is
 * inherited from the session cookie the browser sends on the upgrade
 * request.
 *
 * Usage:
 *
 *   const ws = connectEvents();
 *   ws.onMessage((ev) => { ... });
 *   onDestroy(() => ws.close());
 */
export type EventStream = {
  onMessage(handler: (ev: EventEnvelope) => void): () => void;
  /** Register a callback that fires whenever the socket transitions between
   * connecting / open / closed. Returns an unsubscribe function. */
  onStateChange(
    handler: (s: "connecting" | "open" | "closed") => void,
  ): () => void;
  /**
   * Register a callback for the daemon's resync signal
   * (`{op:"replay_lost"}`): the stream cannot bring this client to the
   * current state, so whatever it holds must be reloaded over REST.
   *
   * The daemon sends it after a boot snapshot — it writes the model to
   * MQTT's retained topics and tells stream subscribers to reload rather
   * than replaying tens of thousands of frames at them — and when a
   * connection fell so far behind that queued events had to be dropped.
   *
   * Returns an unsubscribe function.
   */
  onResync(handler: () => void): () => void;
  close(): void;
  readonly state: () => "connecting" | "open" | "closed";
};

// Wire shape the daemon sends. Distinct from the SPA-facing
// EventEnvelope so the normaliser below is the single place that
// reshapes server events into the discriminator the rest of the SPA
// already consumes ("data_point", "custom_data_point", "sysvar").
type WireEnvelope = {
  topic?: string;
  type?: string;
  ts?: string;
  payload?: unknown;
  // Control frames (`{op:"ping"}`) reuse the same channel.
  op?: string;
};

type WireDataPointPayload = {
  central?: string;
  interface?: string;
  device_address?: string;
  channel?: number;
  parameter?: string;
  paramset_key?: string;
  value?: unknown;
};

/** True for the server heartbeat frame `{op:"ping"}` the client must pong. */
function isControlPing(raw: unknown): boolean {
  return (
    typeof raw === "object" &&
    raw !== null &&
    (raw as { op?: unknown }).op === "ping"
  );
}

/**
 * True for the daemon's resync signal `{op:"replay_lost"}` — the stream
 * has a gap this client cannot reconstruct and must reload from REST.
 */
function isResyncSignal(raw: unknown): boolean {
  return (
    typeof raw === "object" &&
    raw !== null &&
    (raw as { op?: unknown }).op === "replay_lost"
  );
}

/**
 * Map a wire envelope to the SPA's internal `EventEnvelope` shape.
 * Returns null when the frame is a control op (`ping`/`pong`) or not
 * recognisable. Centralising the mapping keeps consumer components
 * (QuickControlTab, SensorChannelList, ChannelPanel, maintenanceStore)
 * decoupled from the wire format — they keep using
 * `env.type === "data_point"` and reading `payload.channel_address`.
 *
 * Every discriminator the `EventEnvelope` union names must be produced
 * here for some wire type; a declared shape nothing emits is a live
 * feature that has never fired. Frames without a normalized shape fall
 * through with their wire type and payload untouched.
 */
function normalizeEvent(raw: unknown): EventEnvelope | null {
  if (!raw || typeof raw !== "object") return null;
  const wire = raw as WireEnvelope;
  if (wire.op) return null; // ping/pong/etc.
  const type = wire.type ?? "";
  if (type === "datapoint.value_changed" && wire.payload) {
    const p = wire.payload as WireDataPointPayload;
    if (
      typeof p.device_address !== "string" ||
      typeof p.channel !== "number" ||
      typeof p.parameter !== "string"
    ) {
      return null;
    }
    return {
      type: "data_point",
      payload: {
        central: p.central ?? "",
        interface: p.interface ?? "",
        channel_address: `${p.device_address}:${p.channel}`,
        parameter: p.parameter,
        value: p.value,
      },
    };
  }
  if (type === "custom_data_point.state_changed" && wire.payload) {
    const p = wire.payload as {
      central?: string;
      device_address?: string;
      channel?: number;
      name?: string;
      kind?: string;
      state?: Record<string, unknown>;
    };
    if (
      typeof p.device_address !== "string" ||
      typeof p.name !== "string" ||
      typeof p.channel !== "number"
    ) {
      return null;
    }
    return {
      type: "custom_data_point",
      payload: {
        central: p.central ?? "",
        device_address: p.device_address,
        channel: p.channel,
        name: p.name,
        kind: p.kind,
        state: p.state ?? {},
      },
    };
  }
  if (type === "hub.sysvar_changed" && wire.payload) {
    const p = wire.payload as {
      central?: string;
      name?: string;
      value?: unknown;
    };
    if (typeof p.name !== "string") return null;
    return {
      type: "sysvar",
      payload: {
        central: p.central ?? "",
        name: p.name,
        value: p.value,
      },
    };
  }
  // Pass-through for events the SPA doesn't actively normalise yet —
  // consumers branch on `env.type` and silently ignore unknown ones.
  return { type, payload: wire.payload } as EventEnvelope;
}

export function connectEvents(): EventStream {
  let socket: WebSocket | null = null;
  let closed = false;
  let attempt = 0;
  const handlers = new Set<(ev: EventEnvelope) => void>();
  const stateHandlers = new Set<
    (s: "connecting" | "open" | "closed") => void
  >();
  const resyncHandlers = new Set<() => void>();
  let current: "connecting" | "open" | "closed" = "connecting";
  // A boot snapshot signals once per central, so a multi-CCU daemon
  // emits a short burst. Coalesce it into one reload.
  let resyncTimer: ReturnType<typeof setTimeout> | null = null;

  const url = buildURL();

  function buildURL(): string {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${location.host}${apiBase()}/events`;
  }

  function setState(next: "connecting" | "open" | "closed") {
    if (current === next) return;
    current = next;
    for (const h of stateHandlers) h(current);
  }

  function connect() {
    if (closed) return;
    setState("connecting");
    socket = new WebSocket(url);
    socket.addEventListener("open", () => {
      attempt = 0;
      setState("open");
      // Subscribe broadly. The hub uses topic-based fan-out — without
      // an explicit subscription the server would drop every event on
      // the floor for this client. The SPA wants the full firehose
      // (devices, hub, central, interfaces) so the wildcard catches
      // future event topics too.
      try {
        socket?.send(
          JSON.stringify({ op: "subscribe", topics: ["*"] }),
        );
      } catch {
        // Subscribing is best-effort: a transient send error here is
        // recovered by the close→reconnect path.
      }
    });
    socket.addEventListener("message", (msg) => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(msg.data);
      } catch {
        return;
      }
      // The server heartbeats with {op:"ping"} every 30s and closes the socket
      // when no client frame arrives within its 60s read deadline. On an idle
      // page nothing else is sent, so without this pong the connection is torn
      // down and re-established every minute — the live indicator flickers.
      // (Browsers do not auto-answer application-level pings, only protocol
      // PING control frames, which this server does not use.)
      if (isControlPing(parsed)) {
        try {
          socket?.send(JSON.stringify({ op: "pong" }));
        } catch {
          // A transient send failure is recovered by the close→reconnect path.
        }
        return;
      }
      if (isResyncSignal(parsed)) {
        if (resyncTimer !== null) clearTimeout(resyncTimer);
        resyncTimer = setTimeout(() => {
          resyncTimer = null;
          for (const h of resyncHandlers) h();
        }, 250);
        return;
      }
      const env = normalizeEvent(parsed);
      if (!env) return;
      for (const h of handlers) h(env);
    });
    socket.addEventListener("close", () => {
      setState("closed");
      if (closed) return;
      // Exponential backoff capped at 15 s.
      const delay = Math.min(15000, 500 * 2 ** Math.min(attempt, 5));
      attempt += 1;
      setTimeout(connect, delay);
    });
    socket.addEventListener("error", () => {
      // The `close` listener drives the retry — silence errors here.
    });
  }

  connect();

  return {
    onMessage(handler) {
      handlers.add(handler);
      return () => handlers.delete(handler);
    },
    onStateChange(handler) {
      stateHandlers.add(handler);
      return () => stateHandlers.delete(handler);
    },
    onResync(handler) {
      resyncHandlers.add(handler);
      return () => resyncHandlers.delete(handler);
    },
    close() {
      closed = true;
      if (resyncTimer !== null) {
        clearTimeout(resyncTimer);
        resyncTimer = null;
      }
      socket?.close();
    },
    state: () => current,
  };
}
