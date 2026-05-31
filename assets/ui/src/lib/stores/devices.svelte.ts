import { api, ApiError } from "$lib/api/client";
import { subscribe } from "./events.svelte";
import type {
  DeviceAvailableEvent,
  DeviceSummary,
  EventEnvelope,
} from "$lib/api/types";
import { authStore } from "./auth.svelte";

/**
 * Svelte 5 rune-based store for the device list. Every page that
 * renders devices imports this module once and subscribes through the
 * exported proxy; the underlying `$state` re-renders reactively on
 * list refresh + WS events.
 */
function createDeviceStore() {
  let items = $state<DeviceSummary[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let lastLoaded = $state<Date | null>(null);
  let unsub: (() => void) | null = null;

  async function refresh() {
    loading = true;
    error = null;
    try {
      const page = await api.listDevices(1, 200);
      items = page.items;
      lastLoaded = new Date();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // Session expired mid-flight; let the auth probe reset so
        // the router re-renders the login page.
        await authStore.probe();
        error = "Sitzung abgelaufen";
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
    if (ev.type === "device_available") {
      // The fallback variant of EventEnvelope widens payload to
      // `unknown`, so narrow explicitly before indexing.
      const p = ev.payload as DeviceAvailableEvent;
      const i = items.findIndex((d) => d.address === p.address);
      if (i >= 0) {
        // $state arrays are reactive on index assignment; no spread
        // needed for the mutation to propagate.
        items[i] = { ...items[i], available: p.available };
      }
    }
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
    refresh,
    ensureStream,
    close() {
      unsub?.();
      unsub = null;
    },
  };
}

export const deviceStore = createDeviceStore();
