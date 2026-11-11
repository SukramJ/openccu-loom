// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Filter options for the room and function pickers. No DOM, no Svelte
// imports — kept unit-testable without a component harness. Rooms and
// functions are per-central, and a nested taxonomy node is its own option
// labelled by its path, so equal names under different parents (or on
// different CCUs) are never merged into one choice.

import type { DeviceSummary } from "$lib/api/types";
import { nestedAssignments, nodeFilterValue } from "$lib/taxonomy/tree";

/** Names a nested taxonomy node by its whole path ("EG › Küche"); the
 *  shape the taxonomy store's `enumOf` + `labelOf` answer. */
export type PathLabel = (central: string, enumId: string, path: string) => string | undefined;

/**
 * Filter options for rooms or functions: one per name, except that a
 * nested node is its own option labelled with its path, so "Küche" on
 * two floors are two choices.
 */
export function distinctNodeOptions(
  devices: DeviceSummary[],
  enumId: "room" | "function",
  central?: string,
  pathLabel?: PathLabel,
): { value: string; label: string }[] {
  const byValue = new Map<string, string>();
  for (const d of devices) {
    if (central && d.central !== central) continue;
    const nested = nestedAssignments(d, enumId);
    const nestedNames = new Set(nested.map((a) => a.name));
    for (const a of nested) {
      const c = d.central ?? "";
      byValue.set(nodeFilterValue(c, enumId, a.path), pathLabel?.(c, enumId, a.path) ?? a.name);
    }
    for (const n of (enumId === "room" ? d.rooms : d.functions) ?? []) {
      if (!nestedNames.has(n)) byValue.set(n, n);
    }
  }
  return [...byValue.entries()]
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: "base" }));
}
