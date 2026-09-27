import { describe, it, expect } from "vitest";
import { assignedPaths, assignmentLabel, flatten, labelOf, moveTargets, nodeRef } from "./tree";

const rooms = {
  id: "room",
  nodes: [
    {
      id: "eg",
      path: "eg",
      name: "Erdgeschoss",
      children: [
        { id: "kueche", path: "eg/kueche", name: "Küche" },
        { id: "wohnzimmer", path: "eg/wohnzimmer", name: "Wohnzimmer" },
      ],
    },
    { id: "og", path: "og", name: "Obergeschoss", children: [{ id: "kueche", path: "og/kueche", name: "Küche" }] },
  ],
};

describe("taxonomy tree helpers", () => {
  it("flattens parents before children with depth and the full label", () => {
    expect(flatten(rooms.nodes).map((n) => [n.path, n.depth, n.label])).toEqual([
      ["eg", 0, "Erdgeschoss"],
      ["eg/kueche", 1, "Erdgeschoss › Küche"],
      ["eg/wohnzimmer", 1, "Erdgeschoss › Wohnzimmer"],
      ["og", 0, "Obergeschoss"],
      ["og/kueche", 1, "Obergeschoss › Küche"],
    ]);
  });

  it("tells two nodes of one name apart by their label", () => {
    expect(labelOf(rooms, "eg/kueche")).toBe("Erdgeschoss › Küche");
    expect(labelOf(rooms, "og/kueche")).toBe("Obergeschoss › Küche");
    expect(labelOf(rooms, "nowhere")).toBeUndefined();
  });

  it("never offers a node's own subtree as a move target", () => {
    expect(moveTargets(rooms, "eg").map((n) => n.path)).toEqual(["og", "og/kueche"]);
    // A prefix that is not a parent ("eg" vs "egx") stays offered.
    const withPrefix = { id: "room", nodes: [...rooms.nodes, { id: "egx", path: "egx", name: "EGX" }] };
    expect(moveTargets(withPrefix, "eg").map((n) => n.path)).toContain("egx");
  });

  it("writes references and reads assignments per enum", () => {
    expect(nodeRef("room", "eg/kueche")).toBe("room/eg/kueche");
    const a = [
      { enum: "room", path: "og/kueche", name: "Küche", parent_path: "og" },
      { enum: "function", path: "licht", name: "Licht" },
    ];
    expect(assignedPaths(a, "room")).toEqual(["og/kueche"]);
    expect(assignmentLabel(a[0], rooms)).toBe("Obergeschoss › Küche");
    expect(assignmentLabel(a[1], undefined)).toBe("Licht");
  });
});
