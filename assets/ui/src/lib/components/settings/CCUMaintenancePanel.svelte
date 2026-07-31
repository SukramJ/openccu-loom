<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SystemCCUEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
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
  // Edited coordinates per central, keyed by name. Seeded from the loaded
  // entry; an untouched central has no draft and its inputs read the
  // server value directly, so a reload cannot silently discard an edit.
  let posDrafts = $state<Record<string, { lon: string; lat: string }>>({});
  let savingPos = $state<string | null>(null);

  function draftFor(e: SystemCCUEntry): { lon: string; lat: string } {
    return (
      posDrafts[e.name] ?? {
        lon: e.longitude != null ? String(e.longitude) : "",
        lat: e.latitude != null ? String(e.latitude) : "",
      }
    );
  }

  function setDraft(name: string, field: "lon" | "lat", value: string) {
    const cur = posDrafts[name] ?? { lon: "", lat: "" };
    posDrafts[name] = { ...cur, [field]: value };
  }

  // A draft counts as saveable only once both fields parse as finite
  // numbers inside the coordinate ranges - the daemon rejects anything
  // else with 422, and failing here keeps the round trip out of it.
  function draftValid(d: { lon: string; lat: string }): boolean {
    const lon = Number(d.lon);
    const lat = Number(d.lat);
    return (
      d.lon.trim() !== "" &&
      d.lat.trim() !== "" &&
      Number.isFinite(lon) &&
      Number.isFinite(lat) &&
      lon >= -180 &&
      lon <= 180 &&
      lat >= -90 &&
      lat <= 90
    );
  }

  async function savePosition(e: SystemCCUEntry) {
    const d = draftFor(e);
    if (!draftValid(d)) return;
    const central = e.name;
    const ok = await confirmStore.ask({
      title: t("ccu_position.confirm_title"),
      body: t("ccu_position.confirm_body", { central }),
      confirmLabel: t("common.save"),
    });
    if (!ok) return;
    savingPos = central;
    try {
      await api.setCCUPosition(central, Number(d.lon), Number(d.lat));
      toastStore.success(t("ccu_position.saved", { central }));
      delete posDrafts[central];
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      savingPos = null;
    }
  }

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

  // The three host actions differ only in confirm copy, endpoint and
  // toast, so one driver covers them - the shape of the interaction
  // (confirm, busy, toast, no reload because the CCU is going down) is
  // identical.
  type HostAction = "poweroff" | "safe_mode" | "recovery_mode";

  async function hostAction(e: SystemCCUEntry, action: HostAction) {
    const central = e.name;
    const ok = await confirmStore.ask({
      title: t(`ccu_host.${action}.confirm_title`),
      body: t(`ccu_host.${action}.confirm_body`, { central }),
      confirmLabel: t(`ccu_host.${action}.action`),
      destructive: true,
    });
    if (!ok) return;
    busy = central;
    try {
      if (action === "poweroff") await api.poweroffCCU(central);
      else if (action === "safe_mode") await api.ccuSafeMode(central);
      else await api.ccuRecoveryMode(central);
      toastStore.success(t(`ccu_host.${action}.triggered`, { central }));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      busy = null;
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
        {@const d = draftFor(e)}
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
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={busy === e.name}
                  onclick={() => void hostAction(e, "safe_mode")}
                >
                  {t("ccu_host.safe_mode.action")}
                </Button>
                <!-- Recovery lives on OpenCCU / RaspberryMatic only. The
                     button is hidden rather than disabled on a stock CCU3:
                     there is nothing the operator could do to enable it. -->
                {#if e.recovery_mode_supported}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={busy === e.name}
                    onclick={() => void hostAction(e, "recovery_mode")}
                  >
                    {t("ccu_host.recovery_mode.action")}
                  </Button>
                {/if}
                <Button
                  type="button"
                  variant="destructive"
                  size="sm"
                  disabled={busy === e.name}
                  onclick={() => void hostAction(e, "poweroff")}
                >
                  {t("ccu_host.poweroff.action")}
                </Button>
              {/if}
            </div>
          </div>

          <!-- Astro reference position. Read-only for non-admins: it moves
               every sunrise/sunset time the CCU computes. -->
          <div class="mt-3 border-t border-[var(--ha-divider-color)] pt-3">
            <div class="mb-2 flex flex-wrap items-center gap-2">
              <span class="text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
                {t("ccu_position.title")}
              </span>
              {#if e.timezone}
                <Badge variant="muted">{e.timezone}</Badge>
              {/if}
            </div>
            {#if isAdmin}
              <div class="flex flex-wrap items-end gap-2">
                <label class="text-xs">
                  <span class="block text-[var(--ha-secondary-text-color)]">{t("ccu_position.latitude")}</span>
                  <Input
                    type="number"
                    step="0.000001"
                    class="w-36"
                    value={d.lat}
                    oninput={(ev) => setDraft(e.name, "lat", (ev.target as HTMLInputElement).value)}
                  />
                </label>
                <label class="text-xs">
                  <span class="block text-[var(--ha-secondary-text-color)]">{t("ccu_position.longitude")}</span>
                  <Input
                    type="number"
                    step="0.000001"
                    class="w-36"
                    value={d.lon}
                    oninput={(ev) => setDraft(e.name, "lon", (ev.target as HTMLInputElement).value)}
                  />
                </label>
                <Button
                  type="button"
                  size="sm"
                  disabled={savingPos === e.name || !draftValid(d)}
                  onclick={() => void savePosition(e)}
                >
                  {savingPos === e.name ? "…" : t("common.save")}
                </Button>
              </div>
              <p class="mt-1.5 text-xs text-[var(--ha-secondary-text-color)]">
                {t("ccu_position.help")}
              </p>
            {:else if e.latitude != null && e.longitude != null}
              <p class="font-mono text-xs text-[var(--ha-secondary-text-color)]">
                {e.latitude} / {e.longitude}
              </p>
            {:else}
              <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("ccu_position.unknown")}</p>
            {/if}
          </div>
        </div>
      {/each}
    </div>
    {#if !isAdmin}
      <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("ccu_maintenance.admin_only")}</p>
    {/if}
  {/if}
</div>
