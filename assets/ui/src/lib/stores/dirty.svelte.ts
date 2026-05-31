// Global "unsaved changes" counter. Editors register/unregister on
// mount/unmount and update their flag while editing. The App attaches
// a single beforeunload listener that warns if any registered editor
// reports dirty state, so leaving the tab in the middle of an unsaved
// paramset edit triggers the browser confirm dialog.

const flags = $state<Record<string, boolean>>({});

export const dirty = {
  set(id: string, isDirty: boolean): void {
    if (isDirty) flags[id] = true;
    else delete flags[id];
  },
  clear(id: string): void {
    delete flags[id];
  },
  any(): boolean {
    return Object.keys(flags).length > 0;
  },
};
