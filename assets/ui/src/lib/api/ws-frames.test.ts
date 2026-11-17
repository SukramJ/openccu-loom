import { describe, expect, it } from "vitest";
import type { EventEnvelope } from "./types";
import { streamFromFrames } from "./ws";

function source() {
  let deliver: (raw: unknown) => void = () => {};
  let closed = false;
  return {
    open: (onFrame: (raw: unknown) => void) => {
      deliver = onFrame;
      return () => {
        closed = true;
      };
    },
    push: (raw: unknown) => deliver(raw),
    closed: () => closed,
  };
}

describe("streamFromFrames", () => {
  it("delivers relayed events in the shape the socket path delivers them", () => {
    const src = source();
    const stream = streamFromFrames(src.open);
    const got: EventEnvelope[] = [];
    stream.onMessage((ev) => got.push(ev));
    src.push({
      type: "datapoint.value_changed",
      payload: { central: "ccu1", device_address: "A", channel: 1, parameter: "STATE", value: true },
    });
    expect(got).toHaveLength(1);
    expect(got[0].type).toBe("data_point");
    expect(stream.state()).toBe("open");
  });

  it("turns the resync signal into a reload and drops control frames", () => {
    const src = source();
    const stream = streamFromFrames(src.open);
    const got: EventEnvelope[] = [];
    let resyncs = 0;
    stream.onMessage((ev) => got.push(ev));
    stream.onResync(() => resyncs++);
    src.push({ op: "ping", echo: "x" });
    src.push({ op: "replay_lost", oldest_seq: 3 });
    expect(got).toEqual([]);
    expect(resyncs).toBe(1);
  });

  it("closes the relayed source once and reports closed", () => {
    const src = source();
    const stream = streamFromFrames(src.open);
    const states: string[] = [];
    stream.onStateChange((s) => states.push(s));
    stream.close();
    stream.close();
    expect(src.closed()).toBe(true);
    expect(states).toEqual(["closed"]);
  });

  it("without a source stays closed and delivers nothing", () => {
    const stream = streamFromFrames(null);
    expect(stream.state()).toBe("closed");
    expect(stream.latencyMs()).toBeNull();
  });
});
