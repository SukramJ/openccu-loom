// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

import { describe, it, expect } from "vitest";
import {
  canRedo,
  canUndo,
  emptyStack,
  entryFromPatch,
  pushEntry,
  redo,
  undo,
  type ChangeStackState,
} from "./change-stack";
import type { ParamValues } from "./validate";

// The command-stack semantics mirror aiohomematic-config's ConfigSession
// undo/redo model: a fresh stack has index -1 ("nothing to undo"); every
// push moves the cursor forward one slot and truncates any redo branch.

describe("emptyStack", () => {
  it("starts with no entries and index -1", () => {
    const state = emptyStack();
    expect(state.entries).toEqual([]);
    expect(state.index).toBe(-1);
    expect(canUndo(state)).toBe(false);
    expect(canRedo(state)).toBe(false);
  });
});

describe("pushEntry", () => {
  it("appends an entry and advances the index", () => {
    const state = pushEntry(emptyStack(), {
      changes: { LEVEL: { before: 0, after: 50 } },
    });
    expect(state.entries).toHaveLength(1);
    expect(state.index).toBe(0);
    expect(canUndo(state)).toBe(true);
    expect(canRedo(state)).toBe(false);
  });

  it("collapses a no-op push where every changed value is unchanged", () => {
    const start = emptyStack();
    const state = pushEntry(start, {
      changes: { LEVEL: { before: 50, after: 50 } },
    });
    // A same-value edit (e.g. re-selecting the current ENUM option) must
    // not create an undo-able entry, mirroring how the CCU WebUI ignores
    // no-op form submits.
    expect(state).toBe(start);
    expect(state.entries).toHaveLength(0);
  });

  it("does not collapse when only some touched parameters changed", () => {
    const state = pushEntry(emptyStack(), {
      changes: {
        LEVEL: { before: 50, after: 50 },
        ON_TIME: { before: 0, after: 30 },
      },
    });
    expect(state.entries).toHaveLength(1);
  });

  it("collapses a push with an empty changes map", () => {
    const start = emptyStack();
    const state = pushEntry(start, { changes: {} });
    expect(state).toBe(start);
  });

  it("compares before/after by deep value, not reference, for no-op detection", () => {
    const start = emptyStack();
    // Two structurally-equal but distinct array instances must still
    // collapse — JSON.stringify equality, not ===.
    const state = pushEntry(start, {
      changes: { VALUE_LIST: { before: [1, 2], after: [1, 2] } },
    });
    expect(state).toBe(start);
  });

  it("truncates any redo branch when pushing after an undo", () => {
    let state = emptyStack();
    state = pushEntry(state, { changes: { A: { before: 0, after: 1 } } });
    state = pushEntry(state, { changes: { A: { before: 1, after: 2 } } });
    state = pushEntry(state, { changes: { A: { before: 2, after: 3 } } });
    expect(state.entries).toHaveLength(3);

    const afterUndo = undo(state, { A: 3 });
    expect(afterUndo.state.index).toBe(1);
    expect(canRedo(afterUndo.state)).toBe(true);

    // Pushing a brand-new edit from this rolled-back position must drop
    // the discarded "A: 2 -> 3" entry rather than keeping it reachable
    // via redo. The two entries below the cursor (A:0->1, A:1->2) are
    // kept; the new entry replaces the truncated redo branch.
    const afterNewPush = pushEntry(afterUndo.state, {
      changes: { A: { before: 2, after: 9 } },
    });
    expect(afterNewPush.entries).toHaveLength(3);
    expect(afterNewPush.index).toBe(2);
    expect(canRedo(afterNewPush)).toBe(false);
    expect(afterNewPush.entries[2].changes.A.after).toBe(9);
  });
});

describe("undo / redo", () => {
  function seedStack(): { state: ChangeStackState; values: ParamValues } {
    let state = emptyStack();
    const values: ParamValues = { LEVEL: 0, ON_TIME: 0 };
    state = pushEntry(state, {
      changes: { LEVEL: { before: 0, after: 50 } },
    });
    state = pushEntry(state, {
      changes: { ON_TIME: { before: 0, after: 30 } },
    });
    return { state, values };
  }

  it("is a no-op when there is nothing to undo", () => {
    const values: ParamValues = { LEVEL: 50 };
    const result = undo(emptyStack(), values);
    expect(result.values).toBe(values);
    expect(result.state).toEqual(emptyStack());
  });

  it("is a no-op when there is nothing to redo", () => {
    const state = pushEntry(emptyStack(), {
      changes: { LEVEL: { before: 0, after: 50 } },
    });
    const values: ParamValues = { LEVEL: 50 };
    const result = redo(state, values);
    expect(result.values).toBe(values);
    expect(result.state).toEqual(state);
  });

  it("rolls back the most recent entry's values and decrements the index", () => {
    const { state } = seedStack();
    const values: ParamValues = { LEVEL: 50, ON_TIME: 30 };

    const step1 = undo(state, values);
    expect(step1.values).toEqual({ LEVEL: 50, ON_TIME: 0 });
    expect(step1.state.index).toBe(0);
    expect(canUndo(step1.state)).toBe(true);
    expect(canRedo(step1.state)).toBe(true);

    const step2 = undo(step1.state, step1.values);
    expect(step2.values).toEqual({ LEVEL: 0, ON_TIME: 0 });
    expect(step2.state.index).toBe(-1);
    expect(canUndo(step2.state)).toBe(false);
    expect(canRedo(step2.state)).toBe(true);

    // Undoing past the bottom of the stack is a no-op.
    const step3 = undo(step2.state, step2.values);
    expect(step3.values).toBe(step2.values);
    expect(step3.state).toBe(step2.state);
  });

  it("replays entries forward on redo, restoring the exact prior values", () => {
    const { state } = seedStack();
    const values: ParamValues = { LEVEL: 50, ON_TIME: 30 };
    const undone = undo(undo(state, values).state, undo(state, values).values);

    const redo1 = redo(undone.state, undone.values);
    expect(redo1.values).toEqual({ LEVEL: 50, ON_TIME: 0 });
    expect(redo1.state.index).toBe(0);

    const redo2 = redo(redo1.state, redo1.values);
    expect(redo2.values).toEqual({ LEVEL: 50, ON_TIME: 30 });
    expect(redo2.state.index).toBe(1);
    expect(canRedo(redo2.state)).toBe(false);
  });

  it("round-trips undo followed by redo back to the identical values", () => {
    const { state } = seedStack();
    const values: ParamValues = { LEVEL: 50, ON_TIME: 30 };
    const undone = undo(state, values);
    const redone = redo(undone.state, undone.values);
    expect(redone.values).toEqual(values);
    expect(redone.state).toEqual(state);
  });
});

describe("entryFromPatch", () => {
  it("snapshots the current value as `before` for every patched name", () => {
    const current: ParamValues = { LEVEL: 10, ON_TIME: 5 };
    const entry = entryFromPatch({ LEVEL: 90 }, current);
    expect(entry.changes).toEqual({ LEVEL: { before: 10, after: 90 } });
    expect(entry.label).toBeUndefined();
  });

  it("carries an optional label through for future history-list UI", () => {
    const entry = entryFromPatch({ LEVEL: 90 }, { LEVEL: 10 }, "profile.apply");
    expect(entry.label).toBe("profile.apply");
  });

  it("records `before: undefined` for a parameter absent from current values", () => {
    const entry = entryFromPatch({ NEW_PARAM: 1 }, {});
    expect(entry.changes.NEW_PARAM).toEqual({ before: undefined, after: 1 });
  });
});

describe("dirty tracking via push + undo (ChannelPanel integration contract)", () => {
  // ChannelPanel derives its `dirtyNames` list by diffing `values` against
  // the untouched `serverValues` snapshot (see ChannelPanel.svelte). This
  // exercises that a push makes the field dirty and a full undo clears it,
  // which is the invariant the Save-button gating depends on.
  function dirtyNames(values: ParamValues, serverValues: ParamValues): string[] {
    return Object.keys(values).filter((k) => values[k] !== serverValues[k]);
  }

  it("marks a parameter dirty after a push and clean again after undo", () => {
    const serverValues: ParamValues = { LEVEL: 0 };
    let values: ParamValues = { ...serverValues };
    let stack = emptyStack();

    const entry = entryFromPatch({ LEVEL: 50 }, values);
    stack = pushEntry(stack, entry);
    values = { ...values, LEVEL: 50 };
    expect(dirtyNames(values, serverValues)).toEqual(["LEVEL"]);

    const result = undo(stack, values);
    values = result.values;
    stack = result.state;
    expect(dirtyNames(values, serverValues)).toEqual([]);
    expect(canUndo(stack)).toBe(false);
  });

  it("keeps the field dirty across an undo/redo round trip that lands on a new value", () => {
    const serverValues: ParamValues = { LEVEL: 0 };
    let values: ParamValues = { ...serverValues };
    let stack = emptyStack();

    stack = pushEntry(stack, entryFromPatch({ LEVEL: 50 }, values));
    values = { ...values, LEVEL: 50 };
    stack = pushEntry(stack, entryFromPatch({ LEVEL: 75 }, values));
    values = { ...values, LEVEL: 75 };

    const afterUndo = undo(stack, values);
    values = afterUndo.values;
    stack = afterUndo.state;
    expect(values.LEVEL).toBe(50);
    expect(dirtyNames(values, serverValues)).toEqual(["LEVEL"]);

    const afterRedo = redo(stack, values);
    values = afterRedo.values;
    expect(values.LEVEL).toBe(75);
    expect(dirtyNames(values, serverValues)).toEqual(["LEVEL"]);
  });
});
