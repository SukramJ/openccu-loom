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
  close(): void;
  readonly state: () => "connecting" | "open" | "closed";
};

// Wire shape the daemon sends. Distinct from the SPA-facing
// EventEnvelope so the normaliser below is the single place that
// reshapes server events into the discriminator the rest of the SPA
// already consumes ("data_point", "device_available", "sysvar", …).
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

/**
 * Map a wire envelope to the SPA's internal `EventEnvelope` shape.
 * Returns null when the frame is a control op (`ping`/`pong`) or not
 * recognisable. Centralising the mapping keeps consumer components
 * (QuickControlTab, SensorChannelList, ChannelPanel, maintenanceStore)
 * decoupled from the wire format — they keep using
 * `env.type === "data_point"` and reading `payload.channel_address`.
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
  // Pass-through for events the SPA doesn't actively normalise yet —
  // consumers branch on `env.type` and silently ignore unknown ones.
  return { type, payload: wire.payload } as EventEnvelope;
}

export function connectEvents(): EventStream {
  let socket: WebSocket | null = null;
  let closed = false;
  let attempt = 0;
  const handlers = new Set<(ev: EventEnvelope) => void>();
  let current: "connecting" | "open" | "closed" = "connecting";

  const url = buildURL();

  function buildURL(): string {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${location.host}${apiBase()}/events`;
  }

  function connect() {
    if (closed) return;
    current = "connecting";
    socket = new WebSocket(url);
    socket.addEventListener("open", () => {
      attempt = 0;
      current = "open";
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
      const env = normalizeEvent(parsed);
      if (!env) return;
      for (const h of handlers) h(env);
    });
    socket.addEventListener("close", () => {
      current = "closed";
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
    close() {
      closed = true;
      socket?.close();
    },
    state: () => current,
  };
}
