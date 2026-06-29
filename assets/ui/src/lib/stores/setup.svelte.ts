import { api } from "$lib/api/client";

/**
 * First-run onboarding state. The store probes `/api/v1/setup/status` on
 * boot; when the daemon reports `required: true` the SPA renders the setup
 * wizard instead of the login screen. After the wizard finalizes, `complete()`
 * flips `required` to false so the app falls through to the login view, where
 * the operator signs in with the just-created admin account.
 */
function createSetupStore() {
  let required = $state(false);
  let checking = $state(true);

  async function probe() {
    checking = true;
    try {
      const s = await api.setupStatus();
      required = s.required;
    } catch {
      // A missing or erroring status endpoint must never trap the operator on
      // the wizard — default to "not required" and let the login flow proceed.
      required = false;
    } finally {
      checking = false;
    }
  }

  // complete is called by the wizard once POST /setup succeeds. The operator
  // is sent to the login screen (App.svelte renders it once required is false
  // and no session exists yet).
  function complete() {
    required = false;
  }

  return {
    get required() {
      return required;
    },
    get checking() {
      return checking;
    },
    probe,
    complete,
  };
}

export const setupStore = createSetupStore();
