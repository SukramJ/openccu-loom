import { api } from "$lib/api/client";
import { foldedRouteTarget, isKnownLandingRoute } from "$lib/nav";

/**
 * The route the SPA opens after a login or a fresh page load (O03).
 *
 * Stored server-side under the per-user preferences key rather than in
 * localStorage: "start page per user" means it follows the operator to
 * another browser or device, which a device-local preference cannot do.
 * The other appearance settings stay local precisely because they are
 * device-shaped (a wall tablet wants a different theme than a laptop).
 *
 * An empty value means "no preference" and leaves the router at its
 * default. A stored route is only honoured while it still resolves — a
 * view that has since been removed, or one the operator lost access to
 * when their role changed, must not strand them on an empty page.
 */
const PREF_KEY = "start_route";

function createStartRouteStore() {
  let route = $state<string>("");
  let loaded = $state(false);

  async function load(): Promise<void> {
    try {
      const stored = await api.getPreference<string>(PREF_KEY);
      route = typeof stored === "string" ? stored : "";
    } catch {
      // Preferences unavailable (persistence disabled, or the request
      // failed): fall back to the default landing route rather than
      // blocking the first paint.
      route = "";
    } finally {
      loaded = true;
    }
  }

  async function set(next: string): Promise<void> {
    route = next;
    if (next === "") {
      await api.deletePreference(PREF_KEY);
      return;
    }
    await api.putPreference(PREF_KEY, next);
  }

  /**
   * The hash to open on a fresh load, or "" to keep the router default.
   * Checks only that the view still exists - see isKnownLandingRoute for
   * why the capability gates deliberately do not apply here.
   *
   * A view that was folded into another one resolves to its successor,
   * so an operator who picked it as their start page before the move
   * keeps landing on the same surface rather than being bounced back to
   * the default.
   */
  function resolve(): string {
    if (!route) return "";
    const folded = foldedRouteTarget(route);
    if (folded) return folded;
    return isKnownLandingRoute(route) ? route : "";
  }

  return {
    get route() {
      return route;
    },
    get loaded() {
      return loaded;
    },
    load,
    set,
    resolve,
  };
}

export const startRouteStore = createStartRouteStore();
