// Runed store for the visibility / un_ignore feature. Manages the
// per-central pattern lists, the candidate set for the multi-select
// picker, and pending edits.
//
// REST surface: /api/v1/visibility/unignore (GET, PUT) and
// /api/v1/visibility/unignore/candidates (GET). See
// notes/concepts/ui/unignore-concept.md.

import { api } from "$lib/api/client";
import { dirty } from "./dirty.svelte";
import type {
  UnIgnoreCandidateGroup,
  UnIgnoreCentralPatterns,
  UnIgnoreUpdateResponse,
} from "$lib/api/visibility-types";

const DIRTY_KEY = "visibility:unignore";

function createVisibilityStore() {
  let centrals = $state<UnIgnoreCentralPatterns[]>([]);
  let centralsLoading = $state(false);
  let centralsError = $state<string | null>(null);

  let candidates = $state<string[]>([]);
  let candidateSet = $state<Set<string>>(new Set());
  let groups = $state<UnIgnoreCandidateGroup[]>([]);
  let reasonVocabulary = $state<string[]>([]);
  let candidatesLoading = $state(false);
  let candidatesError = $state<string | null>(null);
  let includeMaster = $state(false);

  // Pending per-central pattern set; key = central_name, value = the
  // patterns the operator wants to save. Cleared by save / discard.
  let pending = $state<Map<string, string[]>>(new Map());

  // Last save result (parse errors, applied count) so the view can
  // show a banner.
  let lastSave = $state<UnIgnoreUpdateResponse | null>(null);

  function markDirty() {
    dirty.set(DIRTY_KEY, pending.size > 0);
  }

  async function loadCentrals() {
    centralsLoading = true;
    centralsError = null;
    try {
      const resp = await api.listVisibilityUnIgnore();
      centrals = resp.centrals ?? [];
    } catch (e) {
      centralsError = e instanceof Error ? e.message : String(e);
    } finally {
      centralsLoading = false;
    }
  }

  async function loadCandidates(withMaster = includeMaster) {
    candidatesLoading = true;
    candidatesError = null;
    includeMaster = withMaster;
    try {
      const resp = await api.listVisibilityUnIgnoreCandidates(withMaster);
      candidates = resp.candidates ?? [];
      // Membership is asked once per rendered row; a Set keeps that O(1).
      // As an array scan it was O(n) inside an O(n) render — ~5M string
      // comparisons per keystroke on a 399-device fleet.
      candidateSet = new Set(candidates);
      groups = resp.groups ?? [];
      reasonVocabulary = resp.reasons ?? [];
    } catch (e) {
      candidatesError = e instanceof Error ? e.message : String(e);
    } finally {
      candidatesLoading = false;
    }
  }

  /** O(1) membership test against the candidate set. */
  function isCandidate(pattern: string): boolean {
    return candidateSet.has(pattern);
  }

  /** Toggle a single pattern in the pending set for `central`. */
  function togglePattern(central: string, pattern: string) {
    const current = pending.get(central) ?? activePatterns(central);
    const next = current.includes(pattern)
      ? current.filter((p) => p !== pattern)
      : [...current, pattern].sort();
    const newMap = new Map(pending);
    newMap.set(central, next);
    pending = newMap;
    markDirty();
  }

  /** Replace the pending pattern list for `central` wholesale. The
      group-level toggles compute the next list in one step (enabling a
      narrower scope drops the fleet-wide form, clearing a group drops
      every scope it owns), so they hand the result over rather than
      replaying it as a sequence of single-pattern toggles. */
  function setPatterns(central: string, patterns: string[]) {
    const newMap = new Map(pending);
    newMap.set(central, [...patterns].sort());
    pending = newMap;
    markDirty();
  }

  /** Add a free-form pattern. The server validates and surfaces any
      parse error in the save response. */
  function addPattern(central: string, pattern: string) {
    const trimmed = pattern.trim();
    if (!trimmed) return;
    const current = pending.get(central) ?? activePatterns(central);
    if (current.includes(trimmed)) return;
    const newMap = new Map(pending);
    newMap.set(central, [...current, trimmed].sort());
    pending = newMap;
    markDirty();
  }

  function discardPending(central?: string) {
    if (central) {
      const newMap = new Map(pending);
      newMap.delete(central);
      pending = newMap;
    } else {
      pending = new Map();
    }
    markDirty();
  }

  /** Persist the pending set for one central via PUT. */
  async function save(central: string): Promise<UnIgnoreUpdateResponse | null> {
    const patterns = pending.get(central) ?? activePatterns(central);
    try {
      const resp = await api.putVisibilityUnIgnore({
        central_name: central,
        patterns,
      });
      lastSave = resp;
      // Discard the pending entry — the server is authoritative now.
      discardPending(central);
      await loadCentrals();
      await loadCandidates();
      return resp;
    } catch (e) {
      lastSave = null;
      throw e;
    }
  }

  /** Return the patterns currently active server-side for `central`. */
  function activePatterns(central: string): string[] {
    const entry = centrals.find((c) => c.central_name === central);
    if (!entry) return [];
    return entry.patterns.map((p) => p.pattern);
  }

  /** Return the effective view for the UI: pending if present,
      otherwise the persisted set. */
  function effectivePatterns(central: string): string[] {
    return pending.get(central) ?? activePatterns(central);
  }

  function hasPending(central: string): boolean {
    return pending.has(central);
  }

  return {
    get centrals() {
      return centrals;
    },
    get centralsLoading() {
      return centralsLoading;
    },
    get centralsError() {
      return centralsError;
    },
    get candidates() {
      return candidates;
    },
    get groups() {
      return groups;
    },
    get reasonVocabulary() {
      return reasonVocabulary;
    },
    isCandidate,
    get candidatesLoading() {
      return candidatesLoading;
    },
    get candidatesError() {
      return candidatesError;
    },
    get includeMaster() {
      return includeMaster;
    },
    get lastSave() {
      return lastSave;
    },
    loadCentrals,
    loadCandidates,
    togglePattern,
    setPatterns,
    addPattern,
    discardPending,
    save,
    activePatterns,
    effectivePatterns,
    hasPending,
  };
}

export const visibilityStore = createVisibilityStore();
