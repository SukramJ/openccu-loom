import {
  api,
  ApiError,
  setUnauthorizedHandler,
  type Identity,
} from "$lib/api/client";
import { t } from "$lib/i18n";

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

  async function probe() {
    checking = true;
    error = null;
    try {
      identity = await api.me();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        identity = null;
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
  }

  // expire is called by the API client when any non-login call returns
  // 401 — the session cookie is stale (typically the daemon restarted and
  // lost its in-memory sessions). Dropping the identity makes App.svelte
  // render the login view, which unmounts the app shell and stops its
  // background pollers (install-mode, event stream). Guarded so a 401 while
  // already logged out is a no-op and never clobbers an in-flight login.
  function expire() {
    if (identity === null) return;
    identity = null;
    // Reuse the shared "session expired" string (de/en) the API-error
    // formatter already exposes, so the login screen shows it in the
    // active locale instead of a hard-coded German line.
    error = t("api.error.unauthorized");
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
    probe,
    login,
    logout,
    expire,
  };
}

export const authStore = createAuthStore();

// Route every client-side 401 (stale session) through the store so the
// SPA self-heals back to the login view instead of looping on 401s.
setUnauthorizedHandler(() => authStore.expire());
