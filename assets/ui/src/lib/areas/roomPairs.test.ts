// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

import { describe, it, expect } from "vitest";
import type { Area, DeviceSummary } from "$lib/api/types";
import { collectRoomPairs, roomPairKey } from "./roomPairs";

function device(partial: Partial<DeviceSummary> & { address: string }): DeviceSummary {
  return {
    interface: "HmIP-RF",
    interface_id: "HmIP-RF",
    model: "HmIP-PSM",
    name: partial.address,
    available: true,
    channels_count: 1,
    updatable: false,
    update_available: false,
    master_pushes_config_pending: false,
    has_sub_devices: false,
    ...partial,
  } as DeviceSummary;
}

describe("roomPairKey", () => {
  it("does not collide across a central/room boundary shift", () => {
    expect(roomPairKey("Office A", "B")).not.toBe(roomPairKey("Office", "A B"));
  });
});

describe("collectRoomPairs", () => {
  it("collects the union of each device's central × rooms", () => {
    const devices = [
      device({ address: "AAA1", central: "ccu1", rooms: ["Living room", "Hallway"] }),
      device({ address: "AAA2", central: "ccu1", rooms: ["Kitchen"] }),
      device({ address: "BBB1", central: "ccu2", rooms: ["Living room"] }),
    ];
    const pairs = collectRoomPairs(devices, []);
    expect(pairs).toEqual([
      { central: "ccu1", room: "Hallway" },
      { central: "ccu1", room: "Kitchen" },
      { central: "ccu1", room: "Living room" },
      { central: "ccu2", room: "Living room" },
    ]);
  });

  it("dedupes a room shared by multiple devices on the same central", () => {
    const devices = [
      device({ address: "AAA1", central: "ccu1", rooms: ["Living room"] }),
      device({ address: "AAA2", central: "ccu1", rooms: ["Living room"] }),
    ];
    expect(collectRoomPairs(devices, [])).toEqual([{ central: "ccu1", room: "Living room" }]);
  });

  it("never merges the same room name across two different centrals", () => {
    const devices = [
      device({ address: "AAA1", central: "ccu1", rooms: ["Living room"] }),
      device({ address: "BBB1", central: "ccu2", rooms: ["Living room"] }),
    ];
    const pairs = collectRoomPairs(devices, []);
    expect(pairs).toHaveLength(2);
  });

  it("keeps a pair already assigned to an area even with no backing device", () => {
    const areas: Area[] = [
      { id: "a1", name: "Upstairs", rooms: [{ central: "ccu1", room: "Attic" }] },
    ];
    const pairs = collectRoomPairs([], areas);
    expect(pairs).toEqual([{ central: "ccu1", room: "Attic" }]);
  });

  it("ignores devices/areas with no room or empty central", () => {
    const devices = [device({ address: "AAA1", central: "", rooms: ["Orphan"] })];
    expect(collectRoomPairs(devices, [])).toEqual([]);
  });
});
