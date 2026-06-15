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
      // Fetch all devices across multiple pages. The REST endpoint supports
      // pagination via `page`/`per_page`; the `total` field in the response
      // body tells us how many items exist in total. A safety cap of 100
      // pages prevents unbounded loops on unexpected server behaviour.
      const PER_PAGE = 200;
      const MAX_PAGES = 100;

      const first = await api.listDevices(1, PER_PAGE);
      const all: typeof first.items = [...first.items];
      const total = first.total;

      if (total > PER_PAGE) {
        const pages = Math.min(Math.ceil(total / PER_PAGE), MAX_PAGES);
        if (pages > MAX_PAGES) {
          console.warn(
            `[deviceStore] installation has ${total} devices — capped at ${MAX_PAGES * PER_PAGE} (${MAX_PAGES} pages).`,
          );
        }
        for (let p = 2; p <= pages; p++) {
          const next = await api.listDevices(p, PER_PAGE);
          all.push(...next.items);
          if (all.length >= total) break;
        }
      }

      items = all;
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
