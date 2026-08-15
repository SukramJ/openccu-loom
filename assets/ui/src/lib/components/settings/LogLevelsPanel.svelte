<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { LogLevelsResponse } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { authStore } from "$lib/stores/auth.svelte";

  // Per-logger log-level overrides. The global default level lives in
  // the Logs toolbar; this panel manages the path-scoped overrides
  // (e.g. raise "openccu-loom.client" to debug for one CCU) that the
  // backend's hmlog.LevelRegistry resolves hierarchically. GET is open;
  // PUT/DELETE are admin-only, so mutation controls are gated on role.

  const LEVELS = ["debug", "info", "warn", "error"] as const;

  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let data = $state<LogLevelsResponse | null>(null);
  let busy = $state(false);

  let newPath = $state("");
  let newLevel = $state<string>("debug");
  // Minutes; empty ⇒ permanent. The field below is an <input type="number">,
  // and Svelte's numeric `bind:value` hands back a number — or null once the
  // field is cleared — never a string.
  let newTtl = $state<number | null>(null);

  const isAdmin = $derived(authStore.identity?.role === "admin");
  const levelOptions = LEVELS.map((l) => ({ value: l, label: l }));

  function describe(err: unknown): string {
    if (err instanceof ApiError) return `${err.status}: ${err.message}`;
    if (err instanceof Error) return err.message;
    return String(err);
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      data = await api.listLogLevels();
    } catch (err) {
      loadError = describe(err);
    } finally {
      loading = false;
    }
  }

  async function add(e: SubmitEvent) {
    e.preventDefault();
    const path = newPath.trim();
    if (!path) return;
    const minutes = newTtl ?? 0;
    const ttlSeconds =
      Number.isFinite(minutes) && minutes > 0 ? Math.round(minutes * 60) : 0;
    busy = true;
    try {
      await api.setLogLevel(path, newLevel, ttlSeconds);
      toastStore.success(t("loglevels.added", { path }));
      newPath = "";
      newTtl = null;
      await load();
    } catch (err) {
      toastStore.error(describe(err));
    } finally {
      busy = false;
    }
  }

  async function remove(path: string) {
    busy = true;
    try {
      await api.resetLogLevel(path);
      toastStore.success(t("loglevels.removed", { path }));
      await load();
    } catch (err) {
      toastStore.error(describe(err));
    } finally {
      busy = false;
    }
  }

  function remaining(ms: number | undefined): string {
    if (!ms || ms <= 0) return "";
    const mins = Math.round(ms / 60000);
    return mins >= 1 ? t("loglevels.expires_in_min", { mins }) : t("loglevels.expires_soon");
  }

  function levelVariant(level: string) {
    if (level === "error") return "danger" as const;
    if (level === "warn") return "warning" as const;
    if (level === "debug") return "default" as const;
    return "muted" as const;
  }

  onMount(load);
</script>

<Card class="p-4">
  <header class="mb-3 flex items-center justify-between gap-3">
    <div>
      <h3 class="text-base font-semibold">{t("loglevels.title")}</h3>
      <p class="text-xs text-[var(--ha-secondary-text-color)]">
        {t("loglevels.subtitle")}
      </p>
    </div>
    {#if data}
      <Badge variant="muted">{t("loglevels.default", { level: data.default })}</Badge>
    {/if}
  </header>

  {#if loading}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={load} />
  {:else if data}
    {#if data.overrides.length === 0}
      <EmptyState message={t("loglevels.empty")} />
    {:else}
      <ul class="divide-y divide-[var(--ha-divider-color)]">
        {#each data.overrides as ov (ov.path)}
          <li class="flex items-center justify-between gap-3 py-2">
            <div class="min-w-0">
              <code class="truncate text-sm">{ov.path}</code>
              <div class="mt-0.5 flex items-center gap-2 text-xs text-[var(--ha-secondary-text-color)]">
                <Badge variant={levelVariant(ov.level)}>{ov.level}</Badge>
                {#if ov.permanent}
                  <span>{t("loglevels.permanent")}</span>
                {:else if ov.remaining_ms}
                  <span>{remaining(ov.remaining_ms)}</span>
                {/if}
              </div>
            </div>
            {#if isAdmin}
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={busy}
                onclick={() => void remove(ov.path)}
              >
                {t("common.remove")}
              </Button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}

    {#if isAdmin}
      <form class="mt-4 flex flex-wrap items-end gap-2" onsubmit={add}>
        <label class="flex-1 min-w-48 text-xs">
          <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
            >{t("loglevels.path_label")}</span
          >
          <Input
            bind:value={newPath}
            placeholder="openccu-loom.client"
            disabled={busy}
          />
        </label>
        <label class="text-xs">
          <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
            >{t("loglevels.level_label")}</span
          >
          <Select
            options={levelOptions}
            bind:value={newLevel}
            disabled={busy}
            class="w-32"
          />
        </label>
        <label class="text-xs">
          <span class="mb-1 block text-[var(--ha-secondary-text-color)]"
            >{t("loglevels.ttl_label")}</span
          >
          <Input
            type="number"
            min="0"
            bind:value={newTtl}
            placeholder={t("loglevels.ttl_permanent")}
            disabled={busy}
            class="w-28"
          />
        </label>
        <Button type="submit" disabled={busy || newPath.trim() === ""}>
          {t("loglevels.add")}
        </Button>
      </form>
    {:else}
      <p class="mt-3 text-xs text-[var(--ha-secondary-text-color)]">
        {t("loglevels.admin_only")}
      </p>
    {/if}
  {/if}
</Card>
