// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Minimal WebSocket stand-in: records sent frames and lets a test drive the
// open/message events connectEvents subscribes to.
type Listener = (e: unknown) => void;
class MockWebSocket {
  static instances: MockWebSocket[] = [];
  sent: string[] = [];
  private listeners: Record<string, Listener[]> = {};
  readyState = 1;
  constructor(public url: string) {
    MockWebSocket.instances.push(this);
  }
  addEventListener(type: string, cb: Listener) {
    (this.listeners[type] ??= []).push(cb);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {}
  emit(type: string, e?: unknown) {
    for (const cb of this.listeners[type] ?? []) cb(e);
  }
}

beforeEach(() => {
  MockWebSocket.instances = [];
  vi.stubGlobal("WebSocket", MockWebSocket as unknown as typeof WebSocket);
});
afterEach(() => vi.unstubAllGlobals());

import { connectEvents } from "./ws";

describe("connectEvents heartbeat", () => {
  it("replies pong to the server ping so an idle socket is not torn down", () => {
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    expect(sock).toBeTruthy();

    sock.emit("open");
    sock.sent.length = 0; // drop the initial subscribe frame
    sock.emit("message", { data: JSON.stringify({ op: "ping" }) });

    expect(sock.sent).toContain(JSON.stringify({ op: "pong" }));
    stream.close();
  });

  it("does not surface a ping frame as a data event", () => {
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    const received: unknown[] = [];
    stream.onMessage((e) => received.push(e));

    sock.emit("open");
    sock.emit("message", { data: JSON.stringify({ op: "ping" }) });

    expect(received).toHaveLength(0);
    stream.close();
  });
});

describe("connectEvents resync signal", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("invokes the resync handler on replay_lost", () => {
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    let resyncs = 0;
    stream.onResync(() => resyncs++);

    sock.emit("open");
    sock.emit("message", {
      data: JSON.stringify({ op: "replay_lost", oldest_seq: 42 }),
    });
    vi.advanceTimersByTime(300);

    expect(resyncs).toBe(1);
    stream.close();
  });

  it("coalesces a burst into one resync", () => {
    // A multi-CCU daemon signals once per central as each finishes its
    // boot snapshot. Reloading once per signal would refetch the whole
    // SPA state several times in a row.
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    let resyncs = 0;
    stream.onResync(() => resyncs++);

    sock.emit("open");
    for (let i = 0; i < 5; i++) {
      sock.emit("message", { data: JSON.stringify({ op: "replay_lost" }) });
    }
    vi.advanceTimersByTime(300);

    expect(resyncs).toBe(1);
    stream.close();
  });

  it("does not surface replay_lost as a data event", () => {
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    const received: unknown[] = [];
    stream.onMessage((e) => received.push(e));

    sock.emit("open");
    sock.emit("message", { data: JSON.stringify({ op: "replay_lost" }) });
    vi.advanceTimersByTime(300);

    expect(received).toHaveLength(0);
    stream.close();
  });

  it("drops a pending resync when the stream is closed", () => {
    const stream = connectEvents();
    const sock = MockWebSocket.instances[0];
    let resyncs = 0;
    stream.onResync(() => resyncs++);

    sock.emit("open");
    sock.emit("message", { data: JSON.stringify({ op: "replay_lost" }) });
    stream.close();
    vi.advanceTimersByTime(300);

    expect(resyncs).toBe(0);
  });
});
