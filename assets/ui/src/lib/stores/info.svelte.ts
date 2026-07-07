// Runed store for the daemon build/runtime info (`GET /api/v1/info`).
// Loaded lazily and kept for the session: the sidebar shows the
// version line from it, the About route re-fetches for a fresh
// uptime but seeds instantly from this cache.

import { api, type DaemonInfo } from "$lib/api/client";

function createInfoStore() {
  let info = $state<DaemonInfo | null>(null);
  let inflight: Promise<void> | null = null;

  // Fetch once per session; concurrent callers share the request.
  // A failed fetch resets so the next caller retries — the version
  // line simply stays hidden until the daemon answers.
  function ensure(): Promise<void> {
    if (info) return Promise.resolve();
    inflight ??= api
      .info()
      .then((i) => {
        info = i;
      })
      .catch(() => undefined)
      .finally(() => {
        inflight = null;
      });
    return inflight;
  }

  // Fresh fetch for surfaces that show runtime values (uptime).
  async function refresh(): Promise<DaemonInfo> {
    const i = await api.info();
    info = i;
    return i;
  }

  return {
    get info() {
      return info;
    },
    ensure,
    refresh,
  };
}

export const infoStore = createInfoStore();
