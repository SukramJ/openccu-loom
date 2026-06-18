// Store for the daemon restart-pending state and supervisor capability.
// Callers refresh `restartPending` after every config section save/reset.
// `restartCaps` is loaded once on first mount (capability list is stable
// for the lifetime of the daemon process).

import { api } from "$lib/api/client";

export const restartPending = $state<{ pending: boolean; fields: string[] }>({
  pending: false,
  fields: [],
});

export const restartCaps = $state<{ supervised: boolean; loaded: boolean }>({
  supervised: false,
  loaded: false,
});

export async function refreshRestartPending(): Promise<void> {
  try {
    const res = await api.getRestartPending();
    restartPending.pending = res.pending;
    restartPending.fields = res.fields;
  } catch {
    restartPending.pending = false;
  }
}

export async function loadRestartCaps(): Promise<void> {
  if (restartCaps.loaded) return;
  try {
    const info = await api.info();
    restartCaps.supervised = info.capabilities.includes("system.restart.supervised.v1");
    restartCaps.loaded = true;
  } catch {
    restartCaps.supervised = false;
  }
}
