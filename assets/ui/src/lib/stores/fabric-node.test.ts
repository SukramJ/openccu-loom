import { describe, expect, it, vi } from "vitest";
import type { EventEnvelope } from "$lib/api/types";
import { matterRoutes, nodeEventFromEnvelope, nodeSubscription } from "./fabric-node.svelte";

// The hub is replaced by a recorder, so a test can deliver envelopes and
// the resync signal and see what the subscription hands the node store.
const hub = vi.hoisted(() => ({
  events: new Set<(ev: EventEnvelope) => void>(),
  resyncs: new Set<() => void>(),
}));
vi.mock("./events.svelte", () => ({
  subscribe: (h: (ev: EventEnvelope) => void) => {
    hub.events.add(h);
    return () => hub.events.delete(h);
  },
  onResync: (h: () => void) => {
    hub.resyncs.add(h);
    return () => hub.resyncs.delete(h);
  },
}));

describe("nodeEventFromEnvelope", () => {
  it("maps a node.* hub envelope onto the package's stream event", () => {
    const payload = { fabric_index: 1, label: "Apple Home" };
    expect(nodeEventFromEnvelope({ type: "node.fabric_added", payload })).toEqual({
      type: "fabric_added",
      data: payload,
    });
    expect(nodeEventFromEnvelope({ type: "node.endpoints_changed", payload: {} })).toEqual({
      type: "endpoints_changed",
      data: {},
    });
  });

  it("ignores every envelope outside the node topics", () => {
    expect(nodeEventFromEnvelope({ type: "matter.fabric_added", payload: {} })).toBeNull();
    expect(nodeEventFromEnvelope({ type: "hub.sysvar_changed", payload: {} })).toBeNull();
  });
});

describe("matterRoutes", () => {
  it("puts the eight node pages and the endpoint page under loom's #/matter routes", () => {
    expect(Object.values(matterRoutes).every((href) => href.startsWith("#/matter"))).toBe(true);
    expect(Object.keys(matterRoutes).sort()).toEqual(
      ["diagnostics", "endpoint", "endpoints", "fabrics", "pair", "selection", "status", "surface", "topology"],
    );
    // The endpoints page appends the number to this prefix.
    expect(matterRoutes.endpoint + 7).toBe("#/matter/endpoint/7");
  });
});

describe("nodeSubscription", () => {
  it("hands node events and the hub's resync signal to the node store, and unsubscribes both", () => {
    const events: unknown[] = [];
    let resyncs = 0;
    const off = nodeSubscription((ev) => events.push(ev), () => (resyncs += 1));

    for (const h of hub.events) h({ type: "node.fabric_removed", payload: { fabric_index: 2 } });
    for (const h of hub.events) h({ type: "hub.sysvar_changed", payload: {} });
    for (const h of hub.resyncs) h();
    expect(events).toEqual([{ type: "fabric_removed", data: { fabric_index: 2 } }]);
    expect(resyncs).toBe(1);

    off();
    expect(hub.events.size).toBe(0);
    expect(hub.resyncs.size).toBe(0);
  });

  it("registers no resync handler when the store passes none", () => {
    const off = nodeSubscription(() => {});
    expect(hub.resyncs.size).toBe(0);
    off();
  });
});
