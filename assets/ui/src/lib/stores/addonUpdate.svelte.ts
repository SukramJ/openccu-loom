import { api, ApiError, friendlyError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { subscribe } from "./events.svelte";
import type { AddonUpdateStatus, EventEnvelope } from "$lib/api/types";

/**
 * Svelte 5 rune-based store for the CCU add-on self-update mechanism
 * (ADR 0057). One singleton shared by the settings card: `refresh()`
 * seeds from `GET /system/addon-update`, `ensureStream()` wires the
 * `addon_update.state_changed` WS broadcast so a background check or
 * an install triggered from elsewhere (another tab, the MQTT `update`
 * entity) still lands here live. `check()`/`install()` wrap the verbs
 * and never throw — a failure is captured in `error` for the caller to
 * surface (toast and/or the persistent ErrorState). Structure mirrors
 * alarmPanel.svelte.ts.
 */
function createAddonUpdateStore() {
  let status = $state<AddonUpdateStatus | null>(null);
  let loading = $state(false);
  let checking = $state(false);
  let installing = $state(false);
  let error = $state<string | null>(null);

  let unsub: (() => void) | null = null;

  async function refresh(): Promise<void> {
    loading = true;
    error = null;
    try {
      status = await api.getAddonUpdateStatus();
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  function ensureStream(): void {
    if (!unsub) unsub = subscribe(applyEvent);
  }

  function applyEvent(ev: EventEnvelope): void {
    if (ev.type !== "addon_update.state_changed") return;
    status = ev.payload as AddonUpdateStatus;
  }

  async function check(): Promise<boolean> {
    if (checking) return false;
    checking = true;
    error = null;
    try {
      await api.checkAddonUpdate();
      // The outcome normally arrives over the WS broadcast; also refresh
      // so a caller without a live socket (or a race with a slow first
      // frame) still observes the result of its own request.
      await refresh();
      return true;
    } catch (err) {
      error = err instanceof ApiError ? friendlyError(err, t) : String(err);
      return false;
    } finally {
      checking = false;
    }
  }

  async function install(): Promise<boolean> {
    if (installing) return false;
    installing = true;
    error = null;
    try {
      await api.installAddonUpdate();
      await refresh();
      return true;
    } catch (err) {
      error = err instanceof ApiError ? friendlyError(err, t) : String(err);
      return false;
    } finally {
      installing = false;
    }
  }

  return {
    get status() {
      return status;
    },
    get loading() {
      return loading;
    },
    get checking() {
      return checking;
    },
    get installing() {
      return installing;
    },
    get error() {
      return error;
    },
    // True while any verb is in flight or the daemon-reported state itself
    // is mid-operation — the card uses this to disable both buttons at
    // once instead of tracking each condition separately.
    get busy() {
      return (
        checking ||
        installing ||
        status?.state === "checking" ||
        status?.state === "downloading" ||
        status?.state === "installing"
      );
    },
    refresh,
    ensureStream,
    check,
    install,
    close() {
      unsub?.();
      unsub = null;
    },
  };
}

export const addonUpdateStore = createAddonUpdateStore();
