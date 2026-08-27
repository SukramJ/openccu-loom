<script lang="ts">
  import { t } from "$lib/i18n";
  import { authStore } from "$lib/stores/auth.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";

  // A session is an absolute window that activity does not extend, so the
  // only thing the operator can do about it is sign in again. The banner
  // exists to make that a decision rather than an interruption: without it
  // the first sign of a lapsed session is a bounce to the login screen,
  // whenever some background poller happens to fire.
  const minutesLeft = $derived(
    authStore.msToExpiry === null ? 0 : Math.max(1, Math.ceil(authStore.msToExpiry / 60_000)),
  );

  async function signInAgain() {
    await authStore.logout();
  }
</script>

{#if authStore.expiringSoon}
  <div
    class="flex w-full items-center gap-3 bg-amber-50 px-4 py-2.5 text-amber-900 dark:bg-amber-950 dark:text-amber-100"
    role="status"
  >
    <Icon
      name="mdi:calendar-clock"
      size={18}
      class="shrink-0 text-amber-600 dark:text-amber-400"
    />
    <span class="flex-1 text-left text-sm font-medium">
      {t("auth.session_expiring", { minutes: minutesLeft })}
    </span>
    <Button
      variant="outline"
      size="sm"
      class="shrink-0 border-amber-400 bg-transparent text-amber-900 hover:bg-amber-100 dark:border-amber-600 dark:text-amber-100 dark:hover:bg-amber-900"
      onclick={signInAgain}
    >
      {t("auth.sign_in_again")}
    </Button>
  </div>
{/if}
