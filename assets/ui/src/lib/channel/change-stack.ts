import type { ParamValues } from "./validate";

/**
 * One recorded edit. A single user action (typing a value, selecting
 * an ENUM, applying a profile preset) may touch multiple parameters
 * — we store the whole set as one entry so undo rolls them back
 * atomically, matching the UX in aiohomematic-config's ConfigSession.
 */
export type ChangeEntry = {
  /** Parameter names touched by this edit, mapped to before/after. */
  changes: Record<string, { before: unknown; after: unknown }>;
  /** Optional label for debugging / future history-list UI. */
  label?: string;
  /**
   * Locked-parameter set before/after this entry, when the edit
   * changes which fields a profile preset holds fixed (a profile
   * apply). Undo/redo replay this alongside `changes` so the
   * disabled-field state rolls back atomically with the values a
   * preset staged, instead of the fields staying disabled (or
   * becoming editable) after the values themselves were reverted.
   */
  lockedParams?: { before: string[]; after: string[] };
  /**
   * Link profile selection before/after this entry, when the edit is a
   * profile switch. Undo/redo hand it back so the profile picker moves
   * with the values the switch staged.
   */
  profile?: { before: number | null; after: number | null };
};

/** Stack state. Index −1 means "no entry active" (fresh/empty). */
export type ChangeStackState = {
  entries: ChangeEntry[];
  /** Position of the most recently applied entry; −1 when empty. */
  index: number;
};

export function emptyStack(): ChangeStackState {
  return { entries: [], index: -1 };
}

/**
 * Append a new entry at the current position, discarding any redo
 * branch. Mirrors the classic command-stack pattern.
 */
export function pushEntry(
  state: ChangeStackState,
  entry: ChangeEntry,
): ChangeStackState {
  // If entry is a no-op, drop it (e.g., user set the same value).
  const keys = Object.keys(entry.changes);
  // A profile switch is an edit of its own even when every value it
  // stages already matches, otherwise undo could not move the picker back.
  const profileMoved =
    entry.profile !== undefined && entry.profile.before !== entry.profile.after;
  if (keys.length === 0 && !profileMoved) return state;
  const allNoop = keys.every(
    (k) =>
      JSON.stringify(entry.changes[k].before) ===
      JSON.stringify(entry.changes[k].after),
  );
  if (allNoop && !profileMoved) return state;
  const head = state.entries.slice(0, state.index + 1);
  head.push(entry);
  return { entries: head, index: head.length - 1 };
}

export function canUndo(state: ChangeStackState): boolean {
  return state.index >= 0;
}

export function canRedo(state: ChangeStackState): boolean {
  return state.index < state.entries.length - 1;
}

/**
 * Apply the current entry's reverse to `values`, returning the new
 * values map plus the decremented state. No-op when there is nothing
 * to undo. `lockedParams` is the caller's current locked-field set;
 * it is only replaced when the entry being undone recorded a
 * before/after locked-set snapshot (profile applies do; plain field
 * edits don't), otherwise it passes through unchanged.
 */
export function undo(
  state: ChangeStackState,
  values: ParamValues,
  lockedParams: ReadonlySet<string> = new Set(),
): {
  values: ParamValues;
  state: ChangeStackState;
  lockedParams: Set<string>;
  /** Present only when the replayed entry was a profile switch. */
  profile?: number | null;
} {
  if (!canUndo(state)) {
    return { values, state, lockedParams: new Set(lockedParams) };
  }
  const entry = state.entries[state.index];
  const next: ParamValues = { ...values };
  for (const [name, { before }] of Object.entries(entry.changes)) {
    next[name] = before;
  }
  const nextLocked = entry.lockedParams
    ? new Set(entry.lockedParams.before)
    : new Set(lockedParams);
  return {
    values: next,
    state: { ...state, index: state.index - 1 },
    lockedParams: nextLocked,
    ...(entry.profile ? { profile: entry.profile.before } : {}),
  };
}

/**
 * Apply the next entry's forward patch. No-op when there is nothing
 * to redo. See `undo` for the `lockedParams` pass-through rule.
 */
export function redo(
  state: ChangeStackState,
  values: ParamValues,
  lockedParams: ReadonlySet<string> = new Set(),
): {
  values: ParamValues;
  state: ChangeStackState;
  lockedParams: Set<string>;
  /** Present only when the replayed entry was a profile switch. */
  profile?: number | null;
} {
  if (!canRedo(state)) {
    return { values, state, lockedParams: new Set(lockedParams) };
  }
  const entry = state.entries[state.index + 1];
  const next: ParamValues = { ...values };
  for (const [name, { after }] of Object.entries(entry.changes)) {
    next[name] = after;
  }
  const nextLocked = entry.lockedParams
    ? new Set(entry.lockedParams.after)
    : new Set(lockedParams);
  return {
    values: next,
    state: { ...state, index: state.index + 1 },
    lockedParams: nextLocked,
    ...(entry.profile ? { profile: entry.profile.after } : {}),
  };
}

/**
 * Build a ChangeEntry from a patch (`{name: newValue}`) by looking
 * up the current values so the `before` snapshots are accurate.
 */
export function entryFromPatch(
  patch: Record<string, unknown>,
  current: ParamValues,
  label?: string,
  lockedParams?: { before: string[]; after: string[] },
  profile?: { before: number | null; after: number | null },
): ChangeEntry {
  const changes: ChangeEntry["changes"] = {};
  for (const [name, after] of Object.entries(patch)) {
    changes[name] = { before: current[name], after };
  }
  return { changes, label, lockedParams, ...(profile ? { profile } : {}) };
}
