<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SystemCCUEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { authStore } from "$lib/stores/auth.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  // CCU maintenance panel. Lists every configured CCU and — for admins —
  // exposes host-level maintenance actions per central. Today that is a
  // reboot (POST /system/ccu/{central}/reboot, which runs the reboot_ccu
  // ReGa script on the CCU); the row's action area is the landing place
  // for further CCU maintenance actions as they land.

  let entries = $state<SystemCCUEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // The central whose reboot is currently being triggered (button busy).
  let busy = $state<string | null>(null);

  const isAdmin = $derived(authStore.identity?.role === "admin");

  async function load() {
    loading = true;
    error = null;
    try {
      entries = await api.getSystemCCUs();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function reboot(e: SystemCCUEntry) {
    const central = e.name;
    const ok = await confirmStore.ask({
      title: t("ccu_maintenance.confirm_title"),
      body: t("ccu_maintenance.confirm_body", { central }),
      confirmLabel: t("ccu_maintenance.reboot"),
      destructive: true,
    });
    if (!ok) return;
    busy = central;
    try {
      await api.rebootCCU(central);
      toastStore.success(t("ccu_maintenance.triggered", { central }));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      busy = null;
    }
  }

  onMount(load);
</script>

<div class="space-y-4">
  <div class="flex flex-wrap items-center justify-between gap-2">
    <h3 class="text-sm font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {t("ccu_maintenance.title")}
    </h3>
    <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
      {t("common.reload")}
    </Button>
  </div>
  <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("ccu_maintenance.subtitle")}</p>

  {#if loading}
    <LoadingState />
  {:else if error}
    <ErrorState message={error} onRetry={() => void load()} />
  {:else if entries.length === 0}
    <EmptyState message={t("ccu_maintenance.empty")} icon="mdi:server-network" />
  {:else}
    <div class="space-y-3">
      {#each entries as e (e.name)}
        <div class="rounded-md bg-[var(--ha-secondary-background-color)] p-3">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              <div class="font-medium">{e.name}</div>
              {#if e.host}
                <div class="mt-0.5 font-mono text-xs text-[var(--ha-secondary-text-color)]">
                  {e.host}
                </div>
              {/if}
            </div>
            <div class="flex flex-wrap items-center gap-2">
              {#if e.available}
                <Badge variant="success">{t("ccu_maintenance.online")}</Badge>
              {:else}
                <Badge variant="muted">{t("ccu_maintenance.offline")}</Badge>
              {/if}
              {#if isAdmin}
                <Button
                  type="button"
                  variant="destructive"
                  size="sm"
                  disabled={busy === e.name}
                  onclick={() => void reboot(e)}
                >
                  {busy === e.name ? t("ccu_maintenance.rebooting") : t("ccu_maintenance.reboot")}
                </Button>
              {/if}
            </div>
          </div>
        </div>
      {/each}
    </div>
    {#if !isAdmin}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("ccu_maintenance.admin_only")}</p>
    {/if}
  {/if}
</div>
