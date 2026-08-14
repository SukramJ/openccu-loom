import { api } from "$lib/api/client";
import { authStore } from "./auth.svelte";

// A pinned item. `type` keeps favorites of different kinds apart so the
// same id can exist as e.g. both a device and a sysvar without clashing.
// `id` is the natural key; `label` is a cached display string so the
// favorites view can render a heading without re-fetching every
// referenced entity first.
//
// Natural keys per type:
//   device   device address        ABC0000001
//   channel  channel address       ABC0000001:4
//   sysvar   variable name
//   program  program id
//
// The value is opaque JSON in the per-user preferences store, so adding
// a type needs no migration: an older client simply ignores a kind it
// does not know, and this one tolerates entries it cannot resolve.
export type FavoriteType = "device" | "channel" | "sysvar" | "program";

export type Favorite = {
  type: FavoriteType;
  id: string;
  label: string;
};

const PREF_KEY = "favorites";

function createFavoritesStore() {
  let items = $state<Favorite[]>([]);
  // Subject the loaded items belong to; null while nothing is loaded.
  // Favorites are per-user server state, but the store outlives a session:
  // a plain "loaded" latch would keep the previous operator's pins visible
  // after a logout/login in the same tab, and the first star they toggle
  // would persist those pins onto their own preference key.
  let loadedFor = $state<string | null>(null);

  // "" stands for "no identity" (auth disabled or not yet probed) so the
  // anonymous case still reaches a loaded state.
  function subject(): string {
    return authStore.identity?.subject ?? "";
  }

  function isCurrent(): boolean {
    return loadedFor === subject();
  }

  // Whether a response started for `forSubject` still belongs to the
  // session on screen. A load issued before the boot probe answered ("" =
  // identity not known yet) was authenticated by the same session cookie
  // that the probe resolves, so it is adopted under whatever identity
  // arrived meanwhile; only a switch between two known operators
  // invalidates a response.
  function stillFor(forSubject: string): boolean {
    return forSubject === "" || forSubject === subject();
  }

  // Items belonging to the operator that is signed in right now. Anything
  // loaded for a different subject is not theirs and must not be shown or
  // written back.
  function ownItems(): Favorite[] {
    return isCurrent() ? items : [];
  }

  async function load() {
    const forSubject = subject();
    try {
      const stored = await api.getPreference<Favorite[]>(PREF_KEY);
      if (!stillFor(forSubject)) return;
      items = Array.isArray(stored) ? stored : [];
    } catch {
      // Preferences unavailable (e.g. persistence disabled) — favorites
      // degrade to empty rather than blocking the UI.
      if (!stillFor(forSubject)) return;
      items = [];
    } finally {
      // A response that arrives after the operator changed belongs to the
      // previous one and is dropped, not adopted by the new one.
      if (stillFor(forSubject)) loadedFor = subject();
    }
  }

  // Guarantees the list in memory is this operator's before it is written
  // back, so a star toggled on a page rendered before the load finished
  // cannot persist someone else's pins.
  async function ensureLoaded() {
    if (!isCurrent()) await load();
  }

  async function persist() {
    await api.putPreference(PREF_KEY, items);
  }

  function isPinned(type: FavoriteType, id: string): boolean {
    return ownItems().some((f) => f.type === type && f.id === id);
  }

  // Adds or removes the favorite and persists. Returns the new pinned
  // state so callers can toast appropriately.
  async function toggle(fav: Favorite): Promise<boolean> {
    await ensureLoaded();
    const pinned = isPinned(fav.type, fav.id);
    if (pinned) {
      items = items.filter((f) => !(f.type === fav.type && f.id === fav.id));
    } else {
      items = [...items, fav];
    }
    await persist();
    return !pinned;
  }

  async function remove(type: FavoriteType, id: string) {
    await ensureLoaded();
    items = items.filter((f) => !(f.type === type && f.id === id));
    await persist();
  }

  return {
    get items() {
      return ownItems();
    },
    /** True while `items` reflect the operator that is signed in now. */
    get loaded() {
      return isCurrent();
    },
    load,
    isPinned,
    toggle,
    remove,
  };
}

export const favoritesStore = createFavoritesStore();
