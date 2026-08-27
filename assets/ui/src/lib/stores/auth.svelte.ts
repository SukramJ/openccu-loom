import {
  api,
  ApiError,
  setUnauthorizedHandler,
  type Identity,
} from "$lib/api/client";
import { shutdown as shutdownEventPump } from "$lib/stores/events.svelte";
import { t } from "$lib/i18n";

/**
 * How long before a credential's deadline the SPA starts warning. A
 * session is an absolute 12h window that activity does not extend, so the
 * warning is the operator's only cue to finish what they are editing and
 * sign in again deliberately. Long enough to save real work, short enough
 * that the banner is not permanent furniture.
 */
export const EXPIRY_WARNING_MS = 15 * 60 * 1000;

/**
 * Auth state for the SPA. The store probes `/api/v1/auth/me` on boot
 * to decide whether the UI should render the login page or the main
 * shell, and persists the identity in memory for the lifetime of the
 * tab. Session cookies are HTTP-only — we never read them in JS.
 */
function createAuthStore() {
  let identity = $state<Identity | null>(null);
  let checking = $state(true);
  let error = $state<string | null>(null);
  // expiresAt is the parsed identity.expires_at, or null when the
  // credential has no server-side deadline (Basic, HA Ingress, an
  // unbounded bearer token). Kept beside the identity rather than derived
  // on read so an unparseable value degrades to "no deadline" once, at
  // the point it arrives, instead of on every tick.
  let expiresAt = $state<Date | null>(null);
  // msToExpiry is refreshed by the ticker below. It drives the banner, so
  // it has to be reactive state rather than a Date.now() read — nothing
  // would invalidate the latter.
  let msToExpiry = $state<number | null>(null);
  let ticker: ReturnType<typeof setInterval> | null = null;
  // Guards expire()'s re-probe against re-entrancy: the /auth/me call it
  // makes can itself 401 and route back through the unauthorized handler,
  // which would otherwise recurse.
  let reprobing = false;

  function stopTicker() {
    if (ticker !== null) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  /**
   * Recompute the remaining lifetime and, once it is gone, hand over to
   * the logged-out state directly.
   *
   * Waiting for the next request to 401 instead would be worse than slow:
   * the daemon closes the WebSocket at the same instant, so the SPA would
   * first show a reconnect loop against a socket that can never come back,
   * and only reach the login screen whenever some poller happened to fire.
   */
  function tick() {
    if (expiresAt === null) {
      msToExpiry = null;
      return;
    }
    const remaining = expiresAt.getTime() - Date.now();
    msToExpiry = remaining;
    if (remaining <= 0) {
      stopTicker();
      identity = null;
      expiresAt = null;
      msToExpiry = null;
      error = t("api.error.unauthorized");
      shutdownEventPump();
    }
  }

  /**
   * Adopt the deadline carried by a freshly resolved identity. An absent
   * or unparseable `expires_at` means "no server-side expiry" — the
   * banner stays away and nothing is scheduled.
   */
  function adoptDeadline(id: Identity | null) {
    stopTicker();
    const raw = id?.expires_at;
    const parsed = raw != null ? new Date(raw) : null;
    expiresAt =
      parsed !== null && !Number.isNaN(parsed.getTime()) ? parsed : null;
    if (expiresAt === null) {
      msToExpiry = null;
      return;
    }
    tick();
    // A 12h window does not need second precision; a 30s cadence keeps the
    // countdown honest to the minute it displays.
    if (expiresAt !== null) {
      ticker = setInterval(tick, 30_000);
    }
  }

  async function probe() {
    checking = true;
    error = null;
    try {
      identity = await api.me();
      adoptDeadline(identity);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        identity = null;
        adoptDeadline(null);
      } else {
        error = err instanceof Error ? err.message : String(err);
      }
    } finally {
      checking = false;
    }
  }

  async function login(username: string, password: string) {
    error = null;
    try {
      identity = await api.login(username, password);
      adoptDeadline(identity);
    } catch (err) {
      error =
        err instanceof ApiError && err.status === 401
          ? t("auth.error.invalid_credentials")
          : err instanceof Error
            ? err.message
            : String(err);
      throw err;
    }
  }

  async function logout() {
    try {
      await api.logout();
    } catch {
      // Even if the server call fails, clear the local state so the
      // UI returns to the login screen.
    }
    identity = null;
    adoptDeadline(null);
    // The shared WS pump ref-counts its handlers, but a store bound for
    // the tab's lifetime (maintenanceStore) never unsubscribes, so the
    // count never reaches zero on its own — without this the pump keeps
    // reconnecting a socket the daemon now rejects with 401, forever.
    shutdownEventPump();
  }

  // expire is called by the API client when any non-login call returns
  // 401 — typically a stale session cookie (the daemon restarted and lost
  // its in-memory sessions), but it can also be a momentarily lapsed HA
  // Ingress session (ADR 0044), where the operator has no local credential
  // and a bounce to the login screen is a dead end.
  //
  // So before giving up we re-probe /auth/me: under Ingress the Supervisor
  // passthrough re-authenticates the request and the identity survives; a
  // genuinely stale session returns 401 again and we fall through to the
  // login view (which unmounts the app shell and stops its background
  // pollers). Guarded so a 401 while already logged out — or the re-probe's
  // own 401 — is a no-op and never clobbers an in-flight login.
  async function expire() {
    if (identity === null || reprobing) return;
    reprobing = true;
    try {
      identity = await api.me();
      // Under Ingress the re-probe can hand back a different credential
      // from the one that just 401'd, so re-read the deadline rather than
      // keeping the old one ticking.
      adoptDeadline(identity);
    } catch {
      identity = null;
      adoptDeadline(null);
      // Reuse the shared "session expired" string (de/en) the API-error
      // formatter already exposes, so the login screen shows it in the
      // active locale instead of a hard-coded German line.
      error = t("api.error.unauthorized");
      // The re-probe also failed: the session is genuinely gone, so stop
      // the WS pump the same way logout() does (see the comment there).
      shutdownEventPump();
    } finally {
      reprobing = false;
    }
  }

  return {
    get identity() {
      return identity;
    },
    get checking() {
      return checking;
    },
    get error() {
      return error;
    },
    get authenticated() {
      return identity !== null;
    },
    /** The credential's deadline, or null when it has no server-side expiry. */
    get expiresAt() {
      return expiresAt;
    },
    /**
     * Milliseconds until the credential lapses, or null when it never
     * does. Refreshed on a 30s cadence while a deadline is known.
     */
    get msToExpiry() {
      return msToExpiry;
    },
    /**
     * Whether the deadline is close enough to warn about. False for a
     * credential without one, so the HA Ingress and Basic deployments
     * never see the banner.
     */
    get expiringSoon() {
      return (
        msToExpiry !== null && msToExpiry > 0 && msToExpiry <= EXPIRY_WARNING_MS
      );
    },
    probe,
    login,
    logout,
    expire,
  };
}

export const authStore = createAuthStore();

// Route every client-side 401 (stale session) through the store so the
// SPA self-heals back to the login view instead of looping on 401s.
setUnauthorizedHandler(() => void authStore.expire());
