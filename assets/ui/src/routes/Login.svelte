<script lang="ts">
  import { authStore } from "$lib/stores/auth.svelte";
  import BrandMark from "$lib/components/ui/BrandMark.svelte";

  let username = $state("");
  let password = $state("");
  let submitting = $state(false);

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    submitting = true;
    try {
      await authStore.login(username, password);
      // Navigate to the device list by default. Deep-link support
      // ("came from /app/#/devices/0001") is deferred until a real
      // router arrives.
      location.hash = "#/devices";
    } catch {
      // Error message is surfaced through authStore.error.
    } finally {
      submitting = false;
    }
  }
</script>

<section class="flex min-h-screen items-center justify-center px-4">
  <form
    onsubmit={onSubmit}
    class="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-6 shadow-sm dark:border-slate-800 dark:bg-slate-900"
  >
    <div class="mb-6 flex items-center justify-center">
      <BrandMark mode="wordmark" height={40} />
    </div>

    <label class="mb-3 block">
      <span class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
        Benutzername
      </span>
      <input
        type="text"
        autocomplete="username"
        required
        bind:value={username}
        class="h-11 w-full rounded-md border border-slate-300 bg-white px-3 text-base focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </label>

    <label class="mb-4 block">
      <span class="mb-1 block text-sm font-medium text-slate-700 dark:text-slate-300">
        Passwort
      </span>
      <input
        type="password"
        autocomplete="current-password"
        required
        bind:value={password}
        class="h-11 w-full rounded-md border border-slate-300 bg-white px-3 text-base focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 sm:text-sm dark:border-slate-700 dark:bg-slate-900"
      />
    </label>

    {#if authStore.error}
      <div class="mb-3 rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-200">
        {authStore.error}
      </div>
    {/if}

    <button
      type="submit"
      class="flex h-11 w-full items-center justify-center rounded-md bg-brand-500 px-3 text-base font-medium text-white transition hover:bg-brand-700 disabled:opacity-50 sm:text-sm"
      disabled={submitting}
    >
      {submitting ? "Anmelden…" : "Anmelden"}
    </button>

    <!-- Single Sign-On button: bounces the browser to the REST OIDC
         start endpoint, which redirects to the IdP and on callback
         drops the same session cookie the password form would. The
         backend 503s when OIDC is not configured, so a click on a
         missing-IdP setup just shows an error page instead of a
         silent no-op. -->
    <div class="mt-3">
      <a
        href="/api/v1/auth/oidc/start"
        class="flex min-h-11 w-full items-center justify-center rounded-md border border-slate-300 px-3 text-center text-sm font-medium text-slate-700 transition hover:border-brand-500 hover:text-brand-700 dark:border-slate-700 dark:text-slate-200"
      >
        Single Sign-On (OIDC)
      </a>
    </div>
  </form>
</section>
