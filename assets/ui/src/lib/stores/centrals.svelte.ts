import { api, ApiError } from "$lib/api/client";
import { t } from "$lib/i18n";
import { onResync, subscribe } from "./events.svelte";
import type { CentralFeatureState, EventEnvelope, SystemCCUEntry } from "$lib/api/types";
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

// Wire shape of the WS "central.features_changed" payload: the central's
// complete feature set.
type CentralFeaturesChanged = {
  central: string;
  system_type?: SystemCCUEntry["system_type"];
  features: Record<string, CentralFeatureState>;
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
    const unsubEvents = subscribe(applyEvent);
    // A resync follows each central's boot snapshot, which is exactly
    // when readiness and availability have moved.
    const unsubResync = onResync(() => void refresh());
    unsub = () => {
      unsubEvents();
      unsubResync();
    };
  }

  function applyEvent(ev: EventEnvelope) {
    if (ev.type === "central.features_changed") {
      applyFeatures(ev.payload as CentralFeaturesChanged);
      return;
    }
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

  // The push carries the complete set, never a delta, so it replaces the
  // entry's map outright.
  function applyFeatures(p: CentralFeaturesChanged) {
    const i = items.findIndex((c) => c.name === p.central);
    if (i < 0) return;
    items[i] = {
      ...items[i],
      features: p.features,
      ...(p.system_type ? { system_type: p.system_type } : {}),
    };
  }

  function byName(name: string): SystemCCUEntry | undefined {
    return items.find((c) => c.name === name);
  }

  /**
   * Whether at least one central offers the feature — the question a
   * navigation entry asks. Before the fleet has loaded, and for a key a
   * central does not report, the answer is yes: the view renders its own
   * state, and a navigation that blanks during the first paint is worse.
   * A feature that is only waiting for its system counts as offered, so
   * the navigation does not flicker while a system boots.
   */
  function featureAvailable(key: string): boolean {
    if (items.length === 0) return true;
    return items.some((c) => {
      const f = c.features?.[key];
      return !f || f.available || f.reason === "not_ready";
    });
  }

  /**
   * The centrals that do not offer the feature for a lasting reason — the
   * system has no such thing, or the credential lacks the scope.
   */
  function centralsLacking(key: string): SystemCCUEntry[] {
    return items.filter((c) => {
      const f = c.features?.[key];
      return f !== undefined && !f.available && f.reason !== "not_ready";
    });
  }

  /**
   * Whether an action needing the feature may be offered for a central —
   * or, without one, for any central. Hidden, never shown-and-failing:
   * only a central that reports the feature absent answers no.
   */
  function offers(central: string | undefined, key: string): boolean {
    if (!central) return featureAvailable(key);
    return featureOf(central, key)?.available !== false;
  }

  /** A central's state of one feature; undefined when it does not report it. */
  function featureOf(central: string, key: string): CentralFeatureState | undefined {
    return byName(central)?.features?.[key];
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
    featureAvailable,
    centralsLacking,
    offers,
    featureOf,
    close() {
      unsub?.();
      unsub = null;
    },
  };
}

export const centralStore = createCentralStore();
