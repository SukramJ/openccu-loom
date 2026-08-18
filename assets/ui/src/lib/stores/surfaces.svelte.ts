// Surface profiles: which views this daemon serves, and the editor
// state behind Settings → Navigation & views.
//
// One store backs both consumers on purpose. The navigation needs the
// resolved map, the editor needs the registry metadata, and deriving
// either of them twice is how "what is hidden" and "what is explained
// as hidden" drift apart. See notes/concepts/ui-surface-profiles.md.

import { api } from "$lib/api/client";
import type {
  EmbeddedScope,
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
  let embeddedScope = $state<EmbeddedScope>("inside_ha");
  // Whether THIS browser reached the daemon through Home Assistant. With
  // the default scope it is what decides the live profile, so the editor
  // cannot explain the profile without it.
  let insideHA = $state(false);
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

  /**
   * `resetDraft: false` is for the mode/scope PUTs (`setEmbedded`,
   * `setEmbeddedScope`): those calls don't carry the row edits at all,
   * so overwriting `draft` from their response would silently discard
   * whatever the operator has changed but not saved yet — the save bar
   * would vanish along with it. `saved` still refreshes either way,
   * since the response is the daemon's current stored profiles.
   */
  function apply(resp: SurfacesResponse, opts: { resetDraft?: boolean } = {}) {
    embedded = resp.embedded;
    embeddedScope = resp.embedded_scope ?? "inside_ha";
    insideHA = resp.inside_ha ?? false;
    profile = resp.profile;
    surfaces = resp.surfaces ?? [];
    effective = resp.effective ?? {};
    centrals = resp.centrals ?? 0;
    saved = cloneProfiles(resp.profiles);
    if (opts.resetDraft ?? true) {
      draft = cloneProfiles(resp.profiles);
    }
    // Only seed the editor's profile selection on the very first load.
    // load(), save(), setEmbedded() and setEmbeddedScope() all round-trip
    // through apply(); snapping `editing` back to the live profile on
    // every one of those responses would switch the editor out from
    // under an operator who is preparing the OTHER profile (setEditing)
    // while they save it or flip the embedded mode.
    if (!loaded) {
      editing = resp.profile;
    }
    loaded = true;
    markDirty();
  }

  function markDirty() {
    // The draft is module state, so it survives the editor's unmount:
    // leaving the view has to roll it back explicitly.
    dirty.set(DIRTY_KEY, changeCount() > 0, discard);
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

  /** The editor a read-only overview hands off to, when it declares one. */
  function opens(id: string): string | undefined {
    return byId.get(id)?.opens;
  }

  /**
   * Whether a view's rows may link into the editor they belong to.
   *
   * A view without a declared target always may. One whose target the
   * live profile hides keeps its listing — a fleet-wide catalogue
   * answers a question the device detail cannot — but the jump has to
   * go, because following it would open a device whose tab is not there.
   */
  function opensVisible(id: string): boolean {
    const target = opens(id);
    return target === undefined || visible(target);
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
    apply(await api.putUISurfaces({ embedded: next }), { resetDraft: false });
  }

  /**
   * Moves where the embedded declaration applies. Saved immediately for
   * the same reason as the master toggle: it is a mode, and letting it
   * sit in the dirty set would offer to "discard" a change whose effect
   * is already on screen.
   */
  async function setEmbeddedScope(next: EmbeddedScope): Promise<void> {
    apply(await api.putUISurfaces({ embedded_scope: next }), { resetDraft: false });
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
    get embeddedScope() {
      return embeddedScope;
    },
    get insideHA() {
      return insideHA;
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
    opens,
    opensVisible,
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
    setEmbeddedScope,
    setEditing,
    changeCount,
    deviationCount,
    hasChanges,
    profileDirty,
  };
}

export const surfacesStore = createSurfacesStore();
