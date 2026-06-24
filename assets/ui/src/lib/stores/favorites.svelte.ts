import { api } from "$lib/api/client";

// A pinned item. `type` keeps favorites of different kinds apart so the
// same id can exist as e.g. both a device and a sysvar without clashing.
// `id` is the natural key (device address, sysvar name); `label` is a
// cached display string so the favorites view can render without
// re-fetching every referenced entity.
export type Favorite = {
  type: "device" | "sysvar";
  id: string;
  label: string;
};

const PREF_KEY = "favorites";

function createFavoritesStore() {
  let items = $state<Favorite[]>([]);
  let loaded = $state(false);

  async function load() {
    try {
      const stored = await api.getPreference<Favorite[]>(PREF_KEY);
      items = Array.isArray(stored) ? stored : [];
    } catch {
      // Preferences unavailable (e.g. persistence disabled) — favorites
      // degrade to empty rather than blocking the UI.
      items = [];
    } finally {
      loaded = true;
    }
  }

  async function persist() {
    await api.putPreference(PREF_KEY, items);
  }

  function isPinned(type: Favorite["type"], id: string): boolean {
    return items.some((f) => f.type === type && f.id === id);
  }

  // Adds or removes the favorite and persists. Returns the new pinned
  // state so callers can toast appropriately.
  async function toggle(fav: Favorite): Promise<boolean> {
    const pinned = isPinned(fav.type, fav.id);
    if (pinned) {
      items = items.filter((f) => !(f.type === fav.type && f.id === fav.id));
    } else {
      items = [...items, fav];
    }
    await persist();
    return !pinned;
  }

  async function remove(type: Favorite["type"], id: string) {
    items = items.filter((f) => !(f.type === type && f.id === id));
    await persist();
  }

  return {
    get items() {
      return items;
    },
    get loaded() {
      return loaded;
    },
    load,
    isPinned,
    toggle,
    remove,
  };
}

export const favoritesStore = createFavoritesStore();
