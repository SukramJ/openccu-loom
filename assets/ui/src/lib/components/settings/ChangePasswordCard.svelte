<script lang="ts">
  import { api, ApiError } from "$lib/api/client";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { authStore } from "$lib/stores/auth.svelte";

  // Self-service password change for the logged-in user. Available to
  // every role (not just admins); the daemon verifies the current
  // password and preserves the role. OIDC / token identities have no
  // local password and get a clear 409 surfaced as a toast.

  let current = $state("");
  let next = $state("");
  let confirm = $state("");
  let busy = $state(false);

  const MIN_LEN = 8;
  const mismatch = $derived(confirm !== "" && next !== confirm);
  const tooShort = $derived(next !== "" && next.length < MIN_LEN);
  const canSubmit = $derived(
    current !== "" &&
      next !== "" &&
      confirm !== "" &&
      !mismatch &&
      !tooShort &&
      !busy,
  );

  function describe(err: unknown): string {
    if (err instanceof ApiError) return `${err.status}: ${err.message}`;
    if (err instanceof Error) return err.message;
    return String(err);
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    busy = true;
    try {
      await api.changeOwnPassword(current, next);
      toastStore.success(t("account.password.changed"));
      current = "";
      next = "";
      confirm = "";
    } catch (err) {
      toastStore.error(describe(err));
    } finally {
      busy = false;
    }
  }
</script>

<Card class="p-4">
  <header class="mb-3">
    <h3 class="text-base font-semibold">{t("account.password.title")}</h3>
    <p class="text-xs text-[var(--ha-secondary-text-color)]">
      {t("account.password.subtitle", {
        user: authStore.identity?.subject ?? "",
      })}
    </p>
  </header>

  <form class="max-w-sm space-y-3" onsubmit={submit}>
    <label class="block text-xs">
      <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
        >{t("account.password.current")}</span
      >
      <Input
        type="password"
        autocomplete="current-password"
        bind:value={current}
        disabled={busy}
      />
    </label>
    <label class="block text-xs">
      <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
        >{t("account.password.new")}</span
      >
      <Input
        type="password"
        autocomplete="new-password"
        bind:value={next}
        disabled={busy}
      />
      {#if tooShort}
        <span class="mt-1 block text-[var(--ha-error-color,#d33)]"
          >{t("account.password.too_short", { min: MIN_LEN })}</span
        >
      {/if}
    </label>
    <label class="block text-xs">
      <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
        >{t("account.password.confirm")}</span
      >
      <Input
        type="password"
        autocomplete="new-password"
        bind:value={confirm}
        disabled={busy}
      />
      {#if mismatch}
        <span class="mt-1 block text-[var(--ha-error-color,#d33)]"
          >{t("account.password.mismatch")}</span
        >
      {/if}
    </label>
    <Button type="submit" disabled={!canSubmit}>
      {t("account.password.submit")}
    </Button>
  </form>
</Card>
