// Singleton state for the global confirm dialog. Component scripts
// can't host module-shared $state, so the rune object lives here and
// the ConfirmDialog component renders against it.

export type ConfirmOptions = {
  title: string;
  body?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
};

type Pending = {
  options: ConfirmOptions;
  resolve: (ok: boolean) => void;
};

const state = $state<{ pending: Pending | null }>({ pending: null });

export const confirmStore = {
  ask(options: ConfirmOptions): Promise<boolean> {
    return new Promise((resolve) => {
      // If something was pending, treat the new ask as a replace
      // (resolve the old one as cancelled). Better than queueing two
      // dialogs invisibly.
      if (state.pending) {
        state.pending.resolve(false);
      }
      state.pending = { options, resolve };
    });
  },
  // Read accessor for the dialog component.
  get pending(): Pending | null {
    return state.pending;
  },
  resolve(ok: boolean): void {
    if (!state.pending) return;
    const p = state.pending;
    state.pending = null;
    p.resolve(ok);
  },
};
