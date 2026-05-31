import { api, ApiError } from "$lib/api/client";

function createInstallModeStore() {
  let active = $state(false);
  let remainingSeconds = $state<number | null>(null);
  let busy = $state(false);
  let banner = $state<string | null>(null);
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let refCount = 0;

  async function refresh() {
    try {
      const r = await api.getInstallMode();
      active = r.active;
      remainingSeconds = r.remaining_seconds ?? null;
    } catch {
      active = false;
      remainingSeconds = null;
    }
  }

  async function toggle() {
    busy = true;
    banner = null;
    try {
      const r = await api.setInstallMode(!active, 60);
      active = r.active;
      remainingSeconds = r.remaining_seconds ?? null;
      banner = active ? "Anlernmodus aktiv (60 s)." : "Anlernmodus beendet.";
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      busy = false;
    }
  }

  function ensurePoll() {
    refCount++;
    if (pollTimer) return;
    void refresh();
    pollTimer = setInterval(() => void refresh(), 5000);
  }

  function release() {
    refCount = Math.max(0, refCount - 1);
    if (refCount === 0 && pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  return {
    get active() {
      return active;
    },
    get remainingSeconds() {
      return remainingSeconds;
    },
    get busy() {
      return busy;
    },
    get banner() {
      return banner;
    },
    refresh,
    toggle,
    ensurePoll,
    release,
  };
}

export const installModeStore = createInstallModeStore();
