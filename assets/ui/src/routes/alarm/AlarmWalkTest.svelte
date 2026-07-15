<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import { alarmPanelStore } from "$lib/stores/alarmPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import type { AlarmWalkTestStatus } from "$lib/api/types";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  // Walk test (docs/alarm-concept.md §12.4): an arm-less test session where
  // tripping each sensor ticks its checklist row green with a timestamp.
  // The seen/total progress bar tracks the shared alarmPanelStore's live
  // `walktest_progress` counter (WS-driven, near-instant). The per-sensor
  // checklist itself has no WS payload — `alarm.walktest_progress` only
  // carries the area-level count — so each time that counter moves this
  // view re-fetches GET .../walktest to learn which row just ticked.

  const store = alarmPanelStore;

  let selectedAreaId = $state("");
  let status = $state<AlarmWalkTestStatus | null>(null);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let busy = $state(false);

  // Guards against an in-flight request for a previously selected area
  // overwriting the checklist after the user has already switched areas.
  let requestId = 0;

  async function loadStatus(id: string) {
    const rid = ++requestId;
    loading = true;
    error = null;
    try {
      const res = await api.getAlarmWalkTestStatus(id);
      if (rid === requestId) status = res;
    } catch (err) {
      if (rid === requestId) error = friendlyError(err, t);
    } finally {
      if (rid === requestId) loading = false;
    }
  }

  // Auto-select the first configured area once the store has loaded.
  $effect(() => {
    if (!selectedAreaId && store.areasConfig.length > 0) {
      selectedAreaId = store.areasConfig[0].id;
    }
  });

  // Reload the checklist whenever the selected area changes or its live
  // WS tick counter moves.
  $effect(() => {
    const id = selectedAreaId;
    const tick = id ? store.walktest[id]?.seen : undefined;
    void tick;
    if (!id) {
      status = null;
      return;
    }
    void loadStatus(id);
  });

  const progress = $derived.by(() => {
    const live = selectedAreaId ? store.walktest[selectedAreaId] : undefined;
    if (live) return live;
    if (status) {
      return {
        seen: status.sensors.filter((s) => s.tested).length,
        total: status.sensors.length,
      };
    }
    return { seen: 0, total: 0 };
  });

  const active = $derived(
    status?.active ??
      store.areas.find((a) => a.id === selectedAreaId)?.walktest_active ??
      false,
  );

  const progressPct = $derived(
    progress.total > 0 ? Math.round((progress.seen / progress.total) * 100) : 0,
  );

  async function start() {
    if (!selectedAreaId || busy) return;
    busy = true;
    try {
      await api.startAlarmWalkTest(selectedAreaId);
      toastStore.success(t("alarm.toast.walktest_started"));
      await loadStatus(selectedAreaId);
    } catch (err) {
      toastStore.error(t("alarm.toast.walktest_start_failed"), friendlyError(err, t));
    } finally {
      busy = false;
    }
  }

  async function stop() {
    if (!selectedAreaId || busy) return;
    busy = true;
    try {
      await api.stopAlarmWalkTest(selectedAreaId);
      await loadStatus(selectedAreaId);
      const seen = status?.sensors.filter((s) => s.tested).length ?? 0;
      const total = status?.sensors.length ?? 0;
      toastStore.success(
        t("alarm.toast.walktest_stopped"),
        t("alarm.walktest.progress", { seen, total }),
      );
    } catch (err) {
      toastStore.error(t("alarm.toast.walktest_stop_failed"), friendlyError(err, t));
    } finally {
      busy = false;
    }
  }

  function fmtTime(iso: string): string {
    try {
      return new Date(iso).toLocaleTimeString(
        prefs.locale === "de" ? "de-DE" : "en-US",
        { hour: "2-digit", minute: "2-digit", second: "2-digit" },
      );
    } catch {
      return iso;
    }
  }
</script>

<div>
  {#if store.areasConfig.length === 0}
    <EmptyState
      icon="mdi:gesture-tap-button"
      message={t("alarm.overview.empty")}
      description={t("alarm.overview.empty.description")}
    >
      {#snippet action()}
        <a href="#/alarm/wizard">
          <Button size="sm">{t("alarm.wizard.launch")}</Button>
        </a>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="mb-4 flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1.5">
        <span class="text-xs font-medium text-[var(--ha-secondary-text-color)]">
          {t("alarm.walktest.select_area")}
        </span>
        <Select
          class="w-48"
          bind:value={selectedAreaId}
          options={store.areasConfig.map((a) => ({ value: a.id, label: a.name }))}
        />
      </div>

      <Badge variant={active ? "success" : "muted"}>
        {active ? t("alarm.walktest.active") : t("alarm.walktest.inactive")}
      </Badge>

      <div class="ml-auto flex gap-2">
        <Button variant="outline" onclick={stop} disabled={!active || busy}>
          {t("alarm.walktest.stop")}
        </Button>
        <Button onclick={start} disabled={active || busy || !selectedAreaId}>
          {t("alarm.walktest.start")}
        </Button>
      </div>
    </div>

    {#if error}
      <ErrorState message={error} onRetry={() => loadStatus(selectedAreaId)} class="mb-4" />
    {/if}

    {#if loading && !status}
      <LoadingState />
    {:else if status}
      <div class="mb-4">
        <div class="mb-1 flex items-center justify-between text-sm">
          <span>{t("alarm.walktest.progress", { seen: progress.seen, total: progress.total })}</span>
          <span class="text-[var(--ha-secondary-text-color)]">{progressPct}%</span>
        </div>
        <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--ha-secondary-background-color)]">
          <div
            class="h-full rounded-full bg-[var(--ha-success-color)] transition-all"
            style="width: {progressPct}%"
          ></div>
        </div>
      </div>

      {#if status.sensors.length === 0}
        <EmptyState icon="mdi:gesture-tap-button" message={t("alarm.walktest.empty")} />
      {:else}
        <Card class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <tbody>
              {#each status.sensors as s (s.id)}
                <tr
                  class="border-b border-[var(--ha-divider-color)] last:border-0 {s.tested
                    ? ''
                    : 'opacity-60'}"
                >
                  <td class="w-8 p-2">
                    {#if s.tested}
                      <Icon
                        name="mdi:check-circle"
                        size={18}
                        class="text-[var(--ha-success-color)]"
                        aria-label=""
                      />
                    {:else}
                      <Icon
                        name="mdi:circle-outline"
                        size={18}
                        class="text-[var(--ha-secondary-text-color)]"
                        aria-label=""
                      />
                    {/if}
                  </td>
                  <td class="p-2 {s.tested ? 'font-medium' : ''}">
                    {s.name || s.id}
                  </td>
                  <td class="p-2 text-right text-xs text-[var(--ha-secondary-text-color)]">
                    {#if s.tested}
                      {t("alarm.walktest.tested")}{#if s.last_triggered_at}
                        · {fmtTime(s.last_triggered_at)}
                      {/if}
                    {:else}
                      {t("alarm.walktest.untested")}
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </Card>
      {/if}
    {/if}
  {/if}
</div>
