import { api, ApiError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { subscribe } from "./events.svelte";
import type { EventEnvelope, SystemCCUEntry } from "$lib/api/types";
import { authStore } from "./auth.svelte";

// Wire shape of the WS "central.readiness_changed" message payload.
// Kept local because EventEnvelope widens unknown message payloads to
// `unknown`; we narrow to this before applying an in-place update.
type CentralReadinessChanged = {
  central: string;
  phase: SystemCCUEntry["readiness"]["phase"];
  ready: boolean;
  interfaces_loaded: number;
  interfaces_total: number;
};

/**
 * Svelte 5 rune-based store for the per-central fleet and its
 * readiness-gated southbound bring-up. Surfaces that need to know
 * whether a CCU is still initializing (Fleet, Overview, DeviceList,
 * Matter pairing) import this module once and read the reactive
 * `items`; live readiness transitions arrive over the WS stream and
 * patch the matching entry in place.
 */
function createCentralStore() {
  let items = $state<SystemCCUEntry[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let lastLoaded = $state<Date | null>(null);
  let unsub: (() => void) | null = null;

  async function refresh() {
    loading = true;
    error = null;
    try {
      items = await api.getSystemCCUs();
      lastLoaded = new Date();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // Session expired mid-flight; let the auth probe reset so the
        // router re-renders the login page.
        await authStore.probe();
        error = t("api.error.unauthorized");
      } else {
        error = err instanceof Error ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  function ensureStream() {
    if (unsub) return;
    unsub = subscribe(applyEvent);
  }

  function applyEvent(ev: EventEnvelope) {
    if (ev.type !== "central.readiness_changed") return;
    const p = ev.payload as CentralReadinessChanged;
    const i = items.findIndex((c) => c.name === p.central);
    if (i < 0) return;
    // $state arrays are reactive on index assignment; replace the
    // readiness sub-object so dependent $derived reads re-run.
    items[i] = {
      ...items[i],
      available: p.ready ? true : items[i].available,
      readiness: {
        phase: p.phase,
        ready: p.ready,
        interfaces_loaded: p.interfaces_loaded,
        interfaces_total: p.interfaces_total,
      },
    };
  }

  function byName(name: string): SystemCCUEntry | undefined {
    return items.find((c) => c.name === name);
  }

  return {
    get items() {
      return items;
    },
    get loading() {
      return loading;
    },
    get error() {
      return error;
    },
    get lastLoaded() {
      return lastLoaded;
    },
    get allReady() {
      return items.length > 0 && items.every((c) => c.readiness.ready);
    },
    get anyReady() {
      return items.some((c) => c.readiness.ready);
    },
    get notReady() {
      return items.filter((c) => !c.readiness.ready);
    },
    refresh,
    ensureStream,
    byName,
    close() {
      unsub?.();
      unsub = null;
    },
  };
}

export const centralStore = createCentralStore();
