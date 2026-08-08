// Surface profiles: which views this daemon serves, and the editor
// state behind Settings → Navigation & views.
//
// One store backs both consumers on purpose. The navigation needs the
// resolved map, the editor needs the registry metadata, and deriving
// either of them twice is how "what is hidden" and "what is explained
// as hidden" drift apart. See notes/concepts/ui-surface-profiles.md.

import { api } from "$lib/api/client";
import type {
  ProfileName,
  SurfaceInfo,
  SurfaceState,
  SurfacesResponse,
} from "$lib/api/surface-types";
import { dirty } from "./dirty.svelte";

const DIRTY_KEY = "surfaces:profiles";

type Overrides = Record<string, SurfaceState>;

function cloneProfiles(
  src: Partial<Record<ProfileName, Overrides>> | undefined,
): Record<ProfileName, Overrides> {
  return {
    standalone: { ...(src?.standalone ?? {}) },
    embedded: { ...(src?.embedded ?? {}) },
  };
}

function sameOverrides(a: Overrides, b: Overrides): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const k of keys) {
    if (a[k] !== b[k]) return false;
  }
  return true;
}

function createSurfacesStore() {
  let loaded = $state(false);
  let loading = $state(false);
  let error = $state<string | null>(null);

  let embedded = $state(false);
  let profile = $state<ProfileName>("standalone");
  let surfaces = $state<SurfaceInfo[]>([]);
  let effective = $state<Record<string, boolean>>({});
  let centrals = $state(0);

  // Saved state as the daemon reports it, and the working copy the
  // editor mutates. Keeping both is what makes Discard possible and
  // what the unsaved-changes count is computed from.
  let saved = $state<Record<ProfileName, Overrides>>(cloneProfiles(undefined));
  let draft = $state<Record<ProfileName, Overrides>>(cloneProfiles(undefined));

  // The profile the editor is showing, which need not be the live one:
  // preparing the Home Assistant layout before switching to it is a
  // normal thing to want.
  let editing = $state<ProfileName>("standalone");

  const byId = $derived(new Map(surfaces.map((s) => [s.id, s])));

  function apply(resp: SurfacesResponse) {
    embedded = resp.embedded;
    profile = resp.profile;
    surfaces = resp.surfaces ?? [];
    effective = resp.effective ?? {};
    centrals = resp.centrals ?? 0;
    saved = cloneProfiles(resp.profiles);
    draft = cloneProfiles(resp.profiles);
    editing = resp.profile;
    loaded = true;
    markDirty();
  }

  function markDirty() {
    dirty.set(DIRTY_KEY, changeCount() > 0);
  }

  function changeCount(): number {
    let n = 0;
    for (const p of ["standalone", "embedded"] as ProfileName[]) {
      const keys = new Set([...Object.keys(saved[p]), ...Object.keys(draft[p])]);
      for (const k of keys) {
        if (saved[p][k] !== draft[p][k]) n++;
      }
    }
    return n;
  }

  async function load(): Promise<void> {
    if (loading) return;
    loading = true;
    error = null;
    try {
      apply(await api.getUISurfaces());
    } catch (e) {
      // A failed load must not blank the navigation: `visible()` falls
      // back to "everything is visible", which is the standalone
      // behaviour and the only safe direction to guess in.
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  /**
   * Whether a surface is visible in the live profile.
   *
   * Unknown ids and the not-yet-loaded state both answer true. The
   * navigation renders during the first paint, before this store has
   * resolved, and guessing "hidden" there would blank the sidebar of
   * every standalone daemon for a few hundred milliseconds.
   */
  function visible(id: string): boolean {
    if (!loaded) return true;
    const v = effective[id];
    return v === undefined ? true : v;
  }

  /** The shipped default of a surface in the edited profile. */
  function defaultOf(id: string, forProfile: ProfileName = editing): boolean {
    const s = byId.get(id);
    if (!s) return true;
    const v = s.defaults?.[forProfile];
    return v === undefined ? true : v;
  }

  /** Whether a surface can never be hidden in the given profile. */
  function isFloor(id: string, forProfile: ProfileName = editing): boolean {
    const s = byId.get(id);
    if (!s) return false;
    if (s.floor === "always") return true;
    return s.floor === "standalone" && forProfile === "standalone";
  }

  /** The edited (unsaved) visibility of a surface. */
  function draftVisible(id: string, forProfile: ProfileName = editing): boolean {
    if (isFloor(id, forProfile)) return true;
    const own = draft[forProfile][id];
    const self = own === undefined ? defaultOf(id, forProfile) : own === "visible";
    if (!self) return false;
    // A child is never more visible than its parent — the same rule the
    // daemon applies, so the preview cannot promise something the save
    // would not deliver.
    const parent = byId.get(id)?.parent;
    return parent ? draftVisible(parent, forProfile) : true;
  }

  /** Whether the edited value deviates from the shipped default. */
  function isChanged(id: string, forProfile: ProfileName = editing): boolean {
    return draft[forProfile][id] !== undefined;
  }

  function set(id: string, next: boolean, forProfile: ProfileName = editing) {
    if (isFloor(id, forProfile)) return;
    const copy = { ...draft[forProfile] };
    if (next === defaultOf(id, forProfile)) {
      delete copy[id];
    } else {
      copy[id] = next ? "visible" : "hidden";
    }
    draft = { ...draft, [forProfile]: copy };
    markDirty();
  }

  function toggle(id: string, forProfile: ProfileName = editing) {
    set(id, !draftVisible(id, forProfile), forProfile);
  }

  function resetSurface(id: string, forProfile: ProfileName = editing) {
    const copy = { ...draft[forProfile] };
    delete copy[id];
    draft = { ...draft, [forProfile]: copy };
    markDirty();
  }

  /** Clears every override of the edited profile — editor state only. */
  function resetProfile(forProfile: ProfileName = editing) {
    draft = { ...draft, [forProfile]: {} };
    markDirty();
  }

  function discard() {
    draft = cloneProfiles(saved);
    markDirty();
  }

  function deviationCount(forProfile: ProfileName = editing): number {
    return Object.keys(draft[forProfile]).length;
  }

  async function save(): Promise<void> {
    apply(await api.putUISurfaces({ profiles: draft }));
  }

  /**
   * Flips the master toggle. Saved immediately and separately from the
   * row edits: it is a mode, not part of the diff, and leaving it in the
   * dirty set would let an operator "discard" a mode change they can
   * already see the effects of.
   */
  async function setEmbedded(next: boolean): Promise<void> {
    apply(await api.putUISurfaces({ embedded: next }));
  }

  function setEditing(next: ProfileName) {
    editing = next;
  }

  /** Whether the save/discard bar should show. */
  function hasChanges(): boolean {
    return changeCount() > 0;
  }

  /** Whether the edited profile differs from what is stored. */
  function profileDirty(forProfile: ProfileName = editing): boolean {
    return !sameOverrides(saved[forProfile], draft[forProfile]);
  }

  return {
    get loaded() {
      return loaded;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get embedded() {
      return embedded;
    },
    get centrals() {
      return centrals;
    },
    get profile() {
      return profile;
    },
    get editing() {
      return editing;
    },
    get surfaces() {
      return surfaces;
    },
    load,
    visible,
    defaultOf,
    isFloor,
    isChanged,
    draftVisible,
    set,
    toggle,
    resetSurface,
    resetProfile,
    discard,
    save,
    setEmbedded,
    setEditing,
    changeCount,
    deviationCount,
    hasChanges,
    profileDirty,
  };
}

export const surfacesStore = createSurfacesStore();
