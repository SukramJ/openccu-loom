import { api } from "$lib/api/client";
import { foldedRouteTarget, isKnownLandingRoute, navSurfaceID } from "$lib/nav";
import { authStore } from "./auth.svelte";
import { surfacesStore } from "./surfaces.svelte";

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
  // Subject the loaded route belongs to; null while nothing is loaded.
  // The store outlives a session, so after a logout/login in the same tab
  // the route in memory is still the previous operator's until the reload
  // for the new one lands.
  let loadedFor = $state<string | null>(null);

  // "" stands for "no identity" (auth disabled, or the boot probe has not
  // answered yet) so the anonymous case still reaches a loaded state.
  function subject(): string {
    return authStore.identity?.subject ?? "";
  }

  // Whether a response started for `forSubject` still belongs to the
  // session on screen. The boot sequence fires this load next to the auth
  // probe, so the identity is routinely still unknown ("") when the
  // request goes out; that answer was authenticated by the same session
  // cookie the probe resolves and is adopted under whatever identity
  // arrived meanwhile. Only a switch between two known operators
  // invalidates a response.
  function stillFor(forSubject: string): boolean {
    return forSubject === "" || forSubject === subject();
  }

  async function load(): Promise<void> {
    const forSubject = subject();
    // Never show the previous operator's start page while their successor's
    // preference is in flight.
    if (loadedFor !== null && loadedFor !== forSubject) route = "";
    try {
      const stored = await api.getPreference<string>(PREF_KEY);
      if (!stillFor(forSubject)) return;
      route = typeof stored === "string" ? stored : "";
    } catch {
      // Preferences unavailable (persistence disabled, or the request
      // failed): fall back to the default landing route rather than
      // blocking the first paint.
      if (!stillFor(forSubject)) return;
      route = "";
    } finally {
      if (stillFor(forSubject)) loadedFor = subject();
    }
  }

  async function set(next: string): Promise<void> {
    route = next;
    loadedFor = subject();
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
    const target = folded ?? route;
    if (!folded && !isKnownLandingRoute(route)) return "";
    // A view the operator's surface profile hides is not a landing page,
    // however valid the route is. The check runs on the resolved store
    // only — while it is still loading it answers "visible", which is the
    // same race the capability gates deliberately avoid here.
    if (!surfacesStore.visible(navSurfaceID(target))) return "";
    return target;
  }

  return {
    get route() {
      return route;
    },
    /** True while `route` reflects the operator that is signed in now. */
    get loaded() {
      return loadedFor === subject();
    },
    load,
    set,
    resolve,
  };
}

export const startRouteStore = createStartRouteStore();
