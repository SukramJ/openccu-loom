// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Pure grouping/filtering helper for the fleet-wide Overview route
// (roadmap B8). No DOM, no Svelte imports — kept unit-testable without
// a component harness. Rooms/functions are per-central (see
// RoomsFunctionsAdmin.svelte), so grouping always nests inside the
// owning central; a room/function name is never merged across two
// different CCUs (roadmap D4 tracks that merge as a separate,
// later decision).

import { makeTextMatcher } from "$lib/utils";
import type { DeviceSummary } from "$lib/api/types";

export type OverviewGroupMode = "room" | "function" | "central";

export type OverviewFilters = {
  central: string;
  room: string;
  function: string;
  /** Selected Area id (settings/RoomsFunctionsAdmin.svelte — an
   *  operator-defined grouping ABOVE CCU rooms). Empty = no filter. */
  area: string;
  search: string;
};

export const defaultOverviewFilters: OverviewFilters = {
  central: "",
  room: "",
  function: "",
  area: "",
  search: "",
};

/** Resolves which Area id owns a (central, room) pair — the shape
 *  `areasStore.areaIdOf` implements. Passed in rather than imported so
 *  this module stays store-free and unit-testable with a plain stub. */
export type AreaIdOf = (central: string, room: string) => string | undefined;

export type DeviceOverviewGroup = {
  /** Stable identity across renders — `${central}::${groupValue}`. */
  key: string;
  /** Owning central. Never merged across centrals (multi-CCU rule). */
  central: string;
  /** Raw room/function value, or `""` for central-mode groups and the
   *  per-central "unassigned" bucket. Callers resolve `""` to a
   *  localized label rather than baking one in here. */
  groupValue: string;
  devices: DeviceSummary[];
};

function textMatches(device: DeviceSummary, search: string): boolean {
  const match = makeTextMatcher(search);
  return (
    match(device.address) ||
    match(device.name ?? "") ||
    match(device.model) ||
    match(device.model_label ?? "")
  );
}

/** Distinct, sorted central names across the fleet. Empty when every
 *  device reports no owning central (single-CCU backends may omit it). */
export function distinctCentrals(devices: DeviceSummary[]): string[] {
  const set = new Set<string>();
  for (const d of devices) if (d.central) set.add(d.central);
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
}

/** Distinct, sorted room names. Scoped to `central` when given — rooms
 *  are per-central, so an unscoped list would blur that boundary. */
export function distinctRooms(devices: DeviceSummary[], central?: string): string[] {
  const set = new Set<string>();
  for (const d of devices) {
    if (central && d.central !== central) continue;
    for (const r of d.rooms ?? []) set.add(r);
  }
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
}

/** Distinct, sorted function ("Gewerk") names, scoped like `distinctRooms`. */
export function distinctFunctions(devices: DeviceSummary[], central?: string): string[] {
  const set = new Set<string>();
  for (const d of devices) {
    if (central && d.central !== central) continue;
    for (const f of d.functions ?? []) set.add(f);
  }
  return [...set].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
}

/** Applies the central/room/function/area/search filters. A device passes
 *  the room/function filter when the (array) rooms/functions field
 *  contains that value; it passes the area filter when ANY of its rooms
 *  is assigned to that area on its owning central (`areaIdOf`). */
export function filterDevices(
  devices: DeviceSummary[],
  filters: OverviewFilters,
  areaIdOf?: AreaIdOf,
): DeviceSummary[] {
  return devices.filter((d) => {
    if (filters.central && d.central !== filters.central) return false;
    if (filters.room && !(d.rooms ?? []).includes(filters.room)) return false;
    if (filters.function && !(d.functions ?? []).includes(filters.function)) return false;
    if (filters.area) {
      const central = d.central ?? "";
      const inArea = (d.rooms ?? []).some((r) => areaIdOf?.(central, r) === filters.area);
      if (!inArea) return false;
    }
    if (filters.search && !textMatches(d, filters.search)) return false;
    return true;
  });
}

/**
 * Groups devices per `mode`, always nested within their owning central
 * (never merges e.g. "Living room" from two different CCUs into one
 * group). Devices with no room/function land in a per-central
 * "unassigned" bucket (`groupValue === ""`); a device that carries
 * multiple rooms/functions appears in each matching group, mirroring
 * how the CCU itself allows multi-room assignment. Groups are only
 * ever created from an actual device, so filtering down to zero
 * members makes the group disappear rather than rendering an empty
 * shell.
 */
export function groupDevices(
  devices: DeviceSummary[],
  mode: OverviewGroupMode,
): DeviceOverviewGroup[] {
  const byKey = new Map<string, DeviceOverviewGroup>();

  function add(central: string, groupValue: string, device: DeviceSummary) {
    const key = `${central}::${groupValue}`;
    let group = byKey.get(key);
    if (!group) {
      group = { key, central, groupValue, devices: [] };
      byKey.set(key, group);
    }
    group.devices.push(device);
  }

  for (const d of devices) {
    const central = d.central ?? "";
    if (mode === "central") {
      add(central, central, d);
      continue;
    }
    const values = mode === "room" ? d.rooms : d.functions;
    if (!values || values.length === 0) {
      add(central, "", d);
    } else {
      for (const v of values) add(central, v, d);
    }
  }

  return [...byKey.values()].sort((a, b) => {
    const centralCmp = a.central.localeCompare(b.central, undefined, { sensitivity: "base" });
    if (centralCmp !== 0) return centralCmp;
    // The unassigned bucket ("") always sorts last within its central.
    if (a.groupValue === "" && b.groupValue !== "") return 1;
    if (b.groupValue === "" && a.groupValue !== "") return -1;
    return a.groupValue.localeCompare(b.groupValue, undefined, {
      sensitivity: "base",
      numeric: true,
    });
  });
}

/** Convenience: filter then group in one call — the shape the Overview
 *  route actually consumes on every reactive recompute. */
export function buildOverviewGroups(
  devices: DeviceSummary[],
  mode: OverviewGroupMode,
  filters: OverviewFilters,
  areaIdOf?: AreaIdOf,
): DeviceOverviewGroup[] {
  return groupDevices(filterDevices(devices, filters, areaIdOf), mode);
}
