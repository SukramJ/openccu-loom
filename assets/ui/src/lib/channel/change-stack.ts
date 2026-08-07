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
  if (keys.length === 0) return state;
  const allNoop = keys.every(
    (k) =>
      JSON.stringify(entry.changes[k].before) ===
      JSON.stringify(entry.changes[k].after),
  );
  if (allNoop) return state;
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
): ChangeEntry {
  const changes: ChangeEntry["changes"] = {};
  for (const [name, after] of Object.entries(patch)) {
    changes[name] = { before: current[name], after };
  }
  return { changes, label, lockedParams };
}
