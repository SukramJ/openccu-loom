import { api, ApiError } from "$lib/api/client";
import type { InstallModeInterfaceEntry } from "$lib/api/types";
import { t } from "$lib/i18n";

// Scope of a teach-in request. Omit `interface` to open install mode on
// every radio at once (the global endpoint); set it to target a single
// interface. `deviceAddress` requests targeted pairing (e.g. by serial)
// and is forwarded to the per-interface endpoint.
export type InstallScope = { interface?: string; deviceAddress?: string };

function createInstallModeStore() {
  let active = $state(false);
  let remainingSeconds = $state<number | null>(null);
  let interfaces = $state<InstallModeInterfaceEntry[]>([]);
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
    // The per-interface list is best-effort: older daemons without the
    // endpoint leave the dropdown empty and the global toggle still works.
    try {
      interfaces = await api.listInstallModeInterfaces();
    } catch {
      interfaces = [];
    }
  }

  function describeError(err: unknown): string {
    if (err instanceof ApiError) return `${err.status}: ${err.message}`;
    if (err instanceof Error) return err.message;
    return String(err);
  }

  async function toggle(scope: InstallScope = {}) {
    busy = true;
    banner = null;
    try {
      if (scope.interface) {
        // Per-interface toggle: derive the next state from the current
        // entry so the button mirrors the selected radio, not the global
        // aggregate.
        const entry = interfaces.find((i) => i.interface === scope.interface);
        const next = !(entry?.active ?? false);
        interfaces = await api.setInstallModeInterface(
          scope.interface,
          next,
          60,
          scope.deviceAddress,
        );
        const g = await api.getInstallMode();
        active = g.active;
        remainingSeconds = g.remaining_seconds ?? null;
        banner = next
          ? t("inbox.install_mode_banner_iface_on", {
              iface: scope.interface,
              seconds: 60,
            })
          : t("inbox.install_mode_banner_iface_off", {
              iface: scope.interface,
            });
      } else {
        const r = await api.setInstallMode(!active, 60);
        active = r.active;
        remainingSeconds = r.remaining_seconds ?? null;
        await refresh();
        banner = active
          ? t("inbox.install_mode_banner_on", { seconds: 60 })
          : t("inbox.install_mode_banner_off");
      }
    } catch (err) {
      banner = describeError(err);
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
    get interfaces() {
      return interfaces;
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
