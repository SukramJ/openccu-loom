// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Fleet Overview filter/group-mode state (roadmap B8). Held at module
// scope so it survives the route unmounting/remounting, and persisted
// to localStorage so the operator's grouping choice and filters
// survive a full page reload — mirrors the DeviceList pattern in
// `deviceListFilters.svelte.ts`.

import type { OverviewFilters, OverviewGroupMode } from "$lib/overview/overview-grouping";
import { defaultOverviewFilters } from "$lib/overview/overview-grouping";

type OverviewPrefs = {
  groupMode: OverviewGroupMode;
  filters: OverviewFilters;
};

const STORAGE_KEY = "openccu-loom.overview-prefs.v1";

const defaults: OverviewPrefs = {
  groupMode: "room",
  filters: { ...defaultOverviewFilters },
};

function load(): OverviewPrefs {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<OverviewPrefs>;
      return {
        groupMode: parsed.groupMode ?? defaults.groupMode,
        filters: { ...defaults.filters, ...parsed.filters },
      };
    }
  } catch {
    // storage unavailable / malformed — fall back to defaults.
  }
  return { groupMode: defaults.groupMode, filters: { ...defaults.filters } };
}

export const overviewPrefs = $state<OverviewPrefs>(load());

// Persist the current grouping + filter state. Best-effort; ignores a
// disabled or full localStorage.
export function persistOverviewPrefs(): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(overviewPrefs));
  } catch {
    // storage unavailable — settings simply do not persist this session.
  }
}
