import { describe, expect, it } from "vitest";
import { matterRoutes, nodeEventFromEnvelope } from "./fabric-node.svelte";

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
  it("puts the six node pages under loom's #/matter routes", () => {
    expect(Object.values(matterRoutes).every((href) => href.startsWith("#/matter"))).toBe(true);
    expect(Object.keys(matterRoutes).sort()).toEqual(
      ["diagnostics", "endpoints", "fabrics", "pair", "selection", "status"],
    );
  });
});
