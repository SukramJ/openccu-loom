// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import type { DeviceSummary } from "$lib/api/types";
import {
  buildOverviewGroups,
  defaultOverviewFilters,
  distinctCentrals,
  distinctFunctions,
  distinctRooms,
  filterDevices,
  groupDevices,
} from "./overview-grouping";

function device(partial: Partial<DeviceSummary> & { address: string }): DeviceSummary {
  return {
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-PSM",
    name: partial.address,
    available: true,
    channels_count: 3,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...partial,
  } as DeviceSummary;
}

const fleet: DeviceSummary[] = [
  device({
    address: "AAA1",
    name: "Lamp Living",
    central: "ccu1",
    rooms: ["Living room"],
    functions: ["Lighting"],
  }),
  device({
    address: "AAA2",
    name: "Lamp Kitchen",
    central: "ccu1",
    rooms: ["Kitchen"],
    functions: ["Lighting"],
  }),
  device({
    address: "AAA3",
    name: "Multi-room sensor",
    central: "ccu1",
    rooms: ["Living room", "Hallway"],
    functions: ["Climate"],
  }),
  device({
    address: "AAA4",
    name: "No room device",
    central: "ccu1",
    rooms: [],
    functions: [],
  }),
  device({
    address: "BBB1",
    name: "Second CCU lamp",
    central: "ccu2",
    rooms: ["Living room"],
    functions: ["Lighting"],
  }),
];

describe("distinctCentrals/distinctRooms/distinctFunctions", () => {
  it("collects sorted distinct values", () => {
    expect(distinctCentrals(fleet)).toEqual(["ccu1", "ccu2"]);
    expect(distinctRooms(fleet)).toEqual(["Hallway", "Kitchen", "Living room"]);
    expect(distinctFunctions(fleet)).toEqual(["Climate", "Lighting"]);
  });

  it("scopes rooms/functions to a single central", () => {
    expect(distinctRooms(fleet, "ccu2")).toEqual(["Living room"]);
    expect(distinctRooms(fleet, "ccu1")).toEqual(["Hallway", "Kitchen", "Living room"]);
  });
});

describe("groupDevices", () => {
  it("groups by room, nested per central, with multi-room fan-out", () => {
    const groups = groupDevices(fleet, "room");
    const key = (central: string, room: string) => `${central}::${room}`;

    const byKey = new Map(groups.map((g) => [g.key, g]));
    expect(byKey.get(key("ccu1", "Living room"))?.devices.map((d) => d.address)).toEqual([
      "AAA1",
      "AAA3",
    ]);
    expect(byKey.get(key("ccu1", "Hallway"))?.devices.map((d) => d.address)).toEqual(["AAA3"]);
    expect(byKey.get(key("ccu1", "Kitchen"))?.devices.map((d) => d.address)).toEqual(["AAA2"]);
    // "No room device" lands in the per-central unassigned bucket.
    expect(byKey.get(key("ccu1", ""))?.devices.map((d) => d.address)).toEqual(["AAA4"]);
    // The multi-CCU case: "Living room" is never merged across centrals.
    expect(byKey.get(key("ccu2", "Living room"))?.devices.map((d) => d.address)).toEqual(["BBB1"]);
    expect(byKey.has(key("ccu2", "Living room"))).toBe(true);
    expect(byKey.get(key("ccu1", "Living room"))).not.toBe(byKey.get(key("ccu2", "Living room")));
  });

  it("groups by function the same way", () => {
    const groups = groupDevices(fleet, "function");
    const lighting = groups.find((g) => g.central === "ccu1" && g.groupValue === "Lighting");
    expect(lighting?.devices.map((d) => d.address)).toEqual(["AAA1", "AAA2"]);
  });

  it("groups by central with one group per CCU", () => {
    const groups = groupDevices(fleet, "central");
    expect(groups.map((g) => g.groupValue)).toEqual(["ccu1", "ccu2"]);
    expect(groups.find((g) => g.groupValue === "ccu1")?.devices).toHaveLength(4);
    expect(groups.find((g) => g.groupValue === "ccu2")?.devices).toHaveLength(1);
  });

  it("sorts the unassigned bucket last within its central", () => {
    const groups = groupDevices(fleet, "room").filter((g) => g.central === "ccu1");
    expect(groups.at(-1)?.groupValue).toBe("");
  });
});

describe("filterDevices", () => {
  it("filters by central", () => {
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, central: "ccu2" });
    expect(filtered.map((d) => d.address)).toEqual(["BBB1"]);
  });

  it("filters by room membership (array field)", () => {
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, room: "Hallway" });
    expect(filtered.map((d) => d.address)).toEqual(["AAA3"]);
  });

  it("filters by function membership", () => {
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, function: "Climate" });
    expect(filtered.map((d) => d.address)).toEqual(["AAA3"]);
  });

  it("filters by free-text search across name/address/model", () => {
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, search: "kitchen" });
    expect(filtered.map((d) => d.address)).toEqual(["AAA2"]);
  });

  it("combines multiple filters (central + search)", () => {
    const filtered = filterDevices(fleet, {
      ...defaultOverviewFilters,
      central: "ccu1",
      search: "lamp",
    });
    expect(filtered.map((d) => d.address)).toEqual(["AAA1", "AAA2"]);
  });

  it("filters by area membership via areaIdOf, scoped per central", () => {
    // "Living room" maps to area "up" on ccu1 but is unassigned on ccu2 —
    // the area filter must not merge the two centrals' rooms.
    const areaIdOf = (central: string, room: string) =>
      central === "ccu1" && room === "Living room" ? "up" : undefined;
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, area: "up" }, areaIdOf);
    expect(filtered.map((d) => d.address)).toEqual(["AAA1", "AAA3"]);
  });

  it("area filter with no areaIdOf callback matches nothing (fails closed)", () => {
    const filtered = filterDevices(fleet, { ...defaultOverviewFilters, area: "up" });
    expect(filtered).toEqual([]);
  });
});

describe("buildOverviewGroups — empty-group collapse", () => {
  it("drops a group entirely once a filter removes all its members", () => {
    // Without a filter, ccu2's "Living room" group exists.
    const unfiltered = buildOverviewGroups(fleet, "room", defaultOverviewFilters);
    expect(unfiltered.some((g) => g.central === "ccu2")).toBe(true);

    // Scoping to ccu1 must not leave a dangling empty ccu2 group.
    const filtered = buildOverviewGroups(fleet, "room", {
      ...defaultOverviewFilters,
      central: "ccu1",
    });
    expect(filtered.some((g) => g.central === "ccu2")).toBe(false);
    expect(filtered.every((g) => g.devices.length > 0)).toBe(true);
  });

  it("collapses a room group when a search term excludes every member", () => {
    const filtered = buildOverviewGroups(fleet, "room", {
      ...defaultOverviewFilters,
      search: "kitchen",
    });
    // Only the Kitchen group should remain.
    expect(filtered).toHaveLength(1);
    expect(filtered[0].groupValue).toBe("Kitchen");
  });
});
