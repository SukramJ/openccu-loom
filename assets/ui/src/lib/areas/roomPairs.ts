// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Pure helper for the Areas admin section's room-assignment picker
// (RoomsFunctionsAdmin.svelte). No DOM, no Svelte imports — kept
// unit-testable independent of the component, mirroring the style of
// overview-grouping.ts / sensorCandidates.ts.

import type { Area, AreaRoomRef, DeviceSummary } from "$lib/api/types";

/** Stable key for a (central, room) pair — used to key Sets/Maps in the
 *  admin picker. A NUL separator avoids collisions between e.g.
 *  central="Office A", room="B" and central="Office", room="A B". */
export function roomPairKey(central: string, room: string): string {
  return `${central}\u0000${room}`;
}

/**
 * Every (central, room) pair worth offering in an area's room-assignment
 * checklist: the union of each device's central × rooms, plus any pair
 * already assigned to an area. The second half matters so a room whose
 * last device was removed, or a room from a central the operator has not
 * loaded devices for yet, does not silently vanish from an area's roster.
 * Sorted by central then room.
 */
export function collectRoomPairs(devices: DeviceSummary[], areas: Area[]): AreaRoomRef[] {
  const seen = new Set<string>();
  const out: AreaRoomRef[] = [];

  function add(central: string, room: string) {
    if (!central || !room) return;
    const key = roomPairKey(central, room);
    if (seen.has(key)) return;
    seen.add(key);
    out.push({ central, room });
  }

  for (const d of devices) {
    const central = d.central ?? "";
    for (const r of d.rooms ?? []) add(central, r);
  }
  for (const a of areas) {
    for (const r of a.rooms ?? []) add(r.central, r.room);
  }

  out.sort((a, b) => {
    const c = a.central.localeCompare(b.central, undefined, { sensitivity: "base" });
    if (c !== 0) return c;
    return a.room.localeCompare(b.room, undefined, { sensitivity: "base", numeric: true });
  });
  return out;
}
