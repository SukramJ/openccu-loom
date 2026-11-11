// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

import { describe, it, expect } from "vitest";
import type { DeviceSummary } from "$lib/api/types";
import { nodeFilterValue } from "$lib/taxonomy/tree";
import { distinctNodeOptions } from "./overview-grouping";

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

describe("distinctNodeOptions", () => {
  // A box with a kitchen on each floor: the names collide, the paths do not.
  const box: DeviceSummary[] = [
    device({
      address: "L1",
      central: "box",
      rooms: ["Küche"],
      taxonomy: [{ enum: "room", path: "eg/kueche", name: "Küche", parent_path: "eg" }],
    }),
    device({
      address: "L2",
      central: "box",
      rooms: ["Küche"],
      taxonomy: [{ enum: "room", path: "og/kueche", name: "Küche", parent_path: "og" }],
    }),
    device({
      address: "L3",
      central: "box",
      rooms: ["Flur"],
      taxonomy: [{ enum: "room", path: "flur", name: "Flur" }],
    }),
  ];
  const labels: Record<string, string> = { "eg/kueche": "EG › Küche", "og/kueche": "OG › Küche" };
  const pathLabel = (_c: string, _e: string, path: string) => labels[path];

  it("offers each nested node as its own option labelled by path", () => {
    const opts = distinctNodeOptions(box, "room", undefined, pathLabel);
    expect(opts).toEqual([
      { value: nodeFilterValue("box", "room", "eg/kueche"), label: "EG › Küche" },
      { value: "Flur", label: "Flur" },
      { value: nodeFilterValue("box", "room", "og/kueche"), label: "OG › Küche" },
    ]);
  });

  it("offers a flat room name as one option", () => {
    const flat = [
      device({ address: "F1", central: "ccu1", rooms: ["Living room"] }),
      device({ address: "F2", central: "ccu1", rooms: ["Living room"] }),
    ];
    expect(distinctNodeOptions(flat, "room")).toEqual([{ value: "Living room", label: "Living room" }]);
  });

  it("scopes options to one central", () => {
    const two = [
      device({ address: "F1", central: "ccu1", rooms: ["Kitchen"] }),
      device({ address: "F2", central: "ccu2", rooms: ["Hallway"] }),
    ];
    expect(distinctNodeOptions(two, "room", "ccu2")).toEqual([{ value: "Hallway", label: "Hallway" }]);
  });
});
