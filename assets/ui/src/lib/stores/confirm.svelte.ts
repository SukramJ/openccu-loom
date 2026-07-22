// Singleton state for the global confirm dialog. Component scripts
// can't host module-shared $state, so the rune object lives here and
// the ConfirmDialog component renders against it.

// Optional labelled checkbox rendered inside the dialog. When present the
// dialog shows a toggle above the action buttons; read its final value via
// confirmStore.checkboxChecked after ask() resolves.
export type ConfirmCheckbox = {
  label: string;
  checked?: boolean;
};

export type ConfirmOptions = {
  title: string;
  body?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  checkbox?: ConfirmCheckbox;
};

type Pending = {
  options: ConfirmOptions;
  resolve: (ok: boolean) => void;
};

const state = $state<{ pending: Pending | null; checkboxChecked: boolean }>({
  pending: null,
  checkboxChecked: false,
});

export const confirmStore = {
  ask(options: ConfirmOptions): Promise<boolean> {
    return new Promise((resolve) => {
      // If something was pending, treat the new ask as a replace
      // (resolve the old one as cancelled). Better than queueing two
      // dialogs invisibly.
      if (state.pending) {
        state.pending.resolve(false);
      }
      state.checkboxChecked = options.checkbox?.checked ?? false;
      state.pending = { options, resolve };
    });
  },
  // Read accessor for the dialog component.
  get pending(): Pending | null {
    return state.pending;
  },
  // Current value of the optional dialog checkbox. Callers read this after
  // ask() resolves; the value survives resolve() so it stays readable.
  get checkboxChecked(): boolean {
    return state.checkboxChecked;
  },
  setCheckboxChecked(v: boolean): void {
    state.checkboxChecked = v;
  },
  resolve(ok: boolean): void {
    if (!state.pending) return;
    const p = state.pending;
    state.pending = null;
    p.resolve(ok);
  },
};
