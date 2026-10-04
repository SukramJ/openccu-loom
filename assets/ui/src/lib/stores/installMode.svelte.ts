import { api, ApiError } from "$lib/api/client";
import type { InstallModeInterfaceEntry } from "$lib/api/types";
import { t } from "$lib/i18n";

// Scope of a teach-in request. `interface` is required — install mode on
// the CCU is always per-interface (there is no CCU-wide toggle).
// `central` names the CCU when several expose the same interface name (a
// mixed openccu-lite + CCU fleet both report HmIP-RF); without it the daemon
// picks the first matching entry. `deviceAddress` requests targeted pairing
// (e.g. by serial) and is forwarded to the per-interface endpoint.
export type InstallScope = {
  interface?: string;
  central?: string;
  deviceAddress?: string;
};

function createInstallModeStore() {
  let active = $state(false);
  let remainingSeconds = $state<number | null>(null);
  let interfaces = $state<InstallModeInterfaceEntry[]>([]);
  let busy = $state(false);
  let banner = $state<string | null>(null);
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let refCount = 0;

  // recomputeAggregate derives the central-wide active flag + remaining
  // countdown from the per-interface list. There is no CCU-wide endpoint:
  // "active" means any interface is pairing; remaining is the longest
  // running window. Drives the sidebar dot and the no-selection display.
  function recomputeAggregate() {
    const running = interfaces.filter((i) => i.active);
    active = running.length > 0;
    remainingSeconds = active
      ? Math.max(...running.map((i) => i.seconds ?? 0))
      : null;
  }

  async function refresh() {
    try {
      interfaces = await api.listInstallModeInterfaces();
    } catch {
      interfaces = [];
    }
    recomputeAggregate();
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
      if (!scope.interface) {
        // Install mode is per-interface only — the caller must name one.
        banner = t("inbox.install_mode_select_interface");
        return;
      }
      // Derive the next state from the current entry so the button mirrors
      // the selected radio.
      const entry = interfaces.find(
        (i) =>
          i.interface === scope.interface &&
          (scope.central === undefined || (i.central ?? "") === scope.central),
      );
      const next = !(entry?.active ?? false);
      // Sending the central is always correct when the entry names one, and
      // the only way to reach the second of two same-named interfaces.
      const central = scope.central || entry?.central || undefined;
      interfaces = await api.setInstallModeInterface(scope.interface, next, {
        seconds: 60,
        deviceAddress: scope.deviceAddress,
        central,
      });
      recomputeAggregate();
      // With several centrals the interface name alone does not say which
      // CCU the banner is about.
      const multiCentral = new Set(interfaces.map((i) => i.central ?? "")).size > 1;
      const iface = multiCentral && central ? `${scope.interface} · ${central}` : scope.interface;
      banner = next
        ? t("inbox.install_mode_banner_iface_on", { iface, seconds: 60 })
        : t("inbox.install_mode_banner_iface_off", { iface });
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
