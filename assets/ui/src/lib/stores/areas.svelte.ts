// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Operator-defined Areas: room groupings ABOVE CCU rooms (a floor, a
// shed, a terrace roof) — distinct from alarm zones. One shared,
// lazily-loaded singleton so every filter surface (DeviceList,
// Overview, the alarm sensor/output pickers, the wizard, GroupEditor)
// and the Settings admin section read the same authoritative list: the
// admin section mutates via the REST API directly, then calls
// `refresh()` so every other consumer picks up the change without its
// own fetch. Structure mirrors centrals.svelte.ts / devices.svelte.ts.

import { api, ApiError, friendlyError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { authStore } from "./auth.svelte";
import type { Area, AreaRoomRef } from "$lib/api/types";

function createAreasStore() {
  let items = $state<Area[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let loaded = $state(false);

  async function refresh() {
    loading = true;
    error = null;
    try {
      items = await api.listAreas();
      loaded = true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // Session expired mid-flight; let the auth probe reset so the
        // router re-renders the login page.
        await authStore.probe();
        error = t("api.error.unauthorized");
      } else {
        error = friendlyError(err, t);
      }
    } finally {
      loading = false;
    }
  }

  // Lazy load for filter consumers: called on mount instead of an
  // unconditional refresh(), so a surface that never touches Areas pays
  // the round trip at most once per session.
  function ensureLoaded() {
    if (!loaded && !loading) void refresh();
  }

  // Sorted by position (ascending, undefined last), ties by name —
  // mirrors the server's own `listAreas` ordering (assets/openapi.yaml).
  // A getter (not $derived) to match the rest of this store family
  // (centrals.svelte.ts, devices.svelte.ts): cheap enough to recompute
  // per access given the small size of an operator's area list.
  function sortedAreas(): Area[] {
    return [...items].sort((a, b) => {
      const pa = a.position ?? Number.MAX_SAFE_INTEGER;
      const pb = b.position ?? Number.MAX_SAFE_INTEGER;
      if (pa !== pb) return pa - pb;
      return a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    });
  }

  // Which area a (central, room) pair currently belongs to — one area
  // per room is a server-enforced invariant, so this is a 1:1 lookup.
  function areaIdOf(central: string, room: string): string | undefined {
    for (const a of items) {
      if ((a.rooms ?? []).some((r) => r.central === central && r.room === room)) {
        return a.id;
      }
    }
    return undefined;
  }

  function roomsOf(areaId: string): AreaRoomRef[] {
    return items.find((a) => a.id === areaId)?.rooms ?? [];
  }

  return {
    get areas() {
      return sortedAreas();
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get loaded() {
      return loaded;
    },
    refresh,
    ensureLoaded,
    areaIdOf,
    roomsOf,
  };
}

export const areasStore = createAreasStore();
