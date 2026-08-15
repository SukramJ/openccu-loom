// Global "unsaved changes" counter. Editors register/unregister on
// mount/unmount and update their flag while editing. The App attaches
// a single beforeunload listener that warns if any registered editor
// reports dirty state, so leaving the tab in the middle of an unsaved
// paramset edit triggers the browser confirm dialog.
//
// An editor whose draft outlives its component - every module-level
// store - also passes a rollback with its flag. The leave-confirm
// dialog promises the changes are lost, so `discardAll()` has to make
// that true: without a rollback the draft survives the confirmation,
// the flag is raised again on the next read, and the dialog re-fires on
// every later navigation for the rest of the tab's life.

const flags = $state<Record<string, boolean>>({});
const rollbacks = new Map<string, () => void>();

export const dirty = {
  /**
   * Reports (or clears) an editor's unsaved state. `onDiscard` rolls the
   * editor's draft back to its saved state and is required from editors
   * whose draft is not destroyed when the component unmounts.
   */
  set(id: string, isDirty: boolean, onDiscard?: () => void): void {
    if (isDirty) {
      flags[id] = true;
      if (onDiscard) rollbacks.set(id, onDiscard);
    } else {
      delete flags[id];
      rollbacks.delete(id);
    }
  },
  clear(id: string): void {
    delete flags[id];
    rollbacks.delete(id);
  },
  any(): boolean {
    return Object.keys(flags).length > 0;
  },
  /**
   * Rolls every registered editor back and clears the dirty set. Called
   * once the operator confirmed they want to leave and lose the edits.
   */
  discardAll(): void {
    for (const rollback of [...rollbacks.values()]) rollback();
    rollbacks.clear();
    for (const id of Object.keys(flags)) delete flags[id];
  },
};
