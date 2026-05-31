import { api, ApiError, type Identity } from "$lib/api/client";

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
          ? "Ungültige Anmeldedaten"
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
  };
}

export const authStore = createAuthStore();
