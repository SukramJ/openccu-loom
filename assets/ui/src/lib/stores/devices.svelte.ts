import { api, ApiError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { onResync } from "./events.svelte";
import type { DeviceSummary } from "$lib/api/types";
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
        error = t("api.error.unauthorized");
      } else {
        error = err instanceof Error ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  // The daemon has no per-device availability broadcast, so the list has
  // no finer-grained live update than a full reload: it re-reads on the
  // daemon's resync signal (the boot snapshot writes MQTT's retained
  // topics instead of replaying every data point into the stream, so
  // without this the list keeps what it read before the restart).
  function ensureStream() {
    if (unsub) return;
    unsub = onResync(() => void refresh());
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
