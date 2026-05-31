<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { AlarmMessage, ServiceMessage } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";

  type Props = { locale: string };
  let { locale }: Props = $props();

  let alarms = $state<AlarmMessage[]>([]);
  let services = $state<ServiceMessage[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let tab = $state<"alarm" | "service">("alarm");
  let acking = $state<string | null>(null);
  let banner = $state<string | null>(null);
  let typeFilter = $state<string>("");
  let onlyQuittable = $state(false);
  let centralFilter = $state<string>("");

  function serviceTypeLabel(typeId: string): string {
    const known = new Set([
      "generic",
      "sticky",
      "config_pending",
      "alarm",
      "update_pending",
      "communication",
    ]);
    return known.has(typeId) ? t(`messages.type.${typeId}`) : typeId;
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      const [a, s] = await Promise.all([
        api.listAlarmMessages(),
        api.listServiceMessages(),
      ]);
      alarms = a;
      services = s;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function ackAlarm(id: string, central: string) {
    acking = id;
    banner = null;
    try {
      await api.ackAlarm(id, central);
      banner = t("messages.acknowledged");
      await load();
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      acking = null;
    }
  }

  async function ackService(id: string, central: string) {
    acking = id;
    banner = null;
    try {
      await api.ackService(id, central);
      banner = t("messages.acknowledged");
      await load();
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      acking = null;
    }
  }

  onMount(load);

  function formatDate(iso: string): string {
    try {
      return new Date(iso).toLocaleString(locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  const alarmCentrals = $derived.by(() => {
    const set = new Set<string>();
    for (const a of alarms) if (a.central) set.add(a.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const serviceCentrals = $derived.by(() => {
    const set = new Set<string>();
    for (const s of services) if (s.central) set.add(s.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const alarmsSorted = $derived(
    [...alarms]
      .filter((a) => !centralFilter || a.central === centralFilter)
      .sort((a, b) => b.timestamp.localeCompare(a.timestamp)),
  );
  const servicesSorted = $derived(
    [...services]
      .filter((s) => {
        if (onlyQuittable && !s.quittable) return false;
        if (typeFilter && (s.type ?? "") !== typeFilter) return false;
        if (centralFilter && s.central !== centralFilter) return false;
        return true;
      })
      .sort((a, b) => b.timestamp.localeCompare(a.timestamp)),
  );

  const serviceTypes = $derived.by(() => {
    const set = new Set<string>();
    for (const s of services) if (s.type) set.add(s.type);
    return Array.from(set).sort();
  });
</script>

<section class="mx-auto max-w-6xl px-6 py-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("messages.title")}</h1>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">
        {t("messages.summary", { alarms: alarms.length, services: services.length })}
      </p>
    </div>
    <div class="flex items-center gap-2">
      {#if banner}
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</span>
      {/if}
      {#if (tab === "alarm" ? alarmCentrals : serviceCentrals).length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
          title="CCU"
        >
          <option value="">Alle CCUs</option>
          {#each (tab === "alarm" ? alarmCentrals : serviceCentrals) as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    </div>
  </header>

  <nav class="mb-4 flex gap-1 border-b border-slate-200 dark:border-slate-800">
    <button
      type="button"
      class="border-b-2 px-3 py-2 text-sm transition {tab === 'alarm'
        ? 'border-brand-500 text-brand-700'
        : 'border-transparent text-[var(--ha-secondary-text-color)] hover:text-brand-700'}"
      onclick={() => (tab = "alarm")}
    >
      {t("messages.alarms")}
      <Badge variant="muted">{alarms.length}</Badge>
    </button>
    <button
      type="button"
      class="border-b-2 px-3 py-2 text-sm transition {tab === 'service'
        ? 'border-brand-500 text-brand-700'
        : 'border-transparent text-[var(--ha-secondary-text-color)] hover:text-brand-700'}"
      onclick={() => (tab = "service")}
    >
      {t("messages.service")}
      <Badge variant="muted">{services.length}</Badge>
    </button>
  </nav>

  {#if loadError}
    <Card class="mb-4 p-3">
      <p class="text-sm text-red-600 dark:text-red-400">
        {t("common.error")} {loadError}
      </p>
    </Card>
  {/if}

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if tab === "alarm"}
    {#if alarmsSorted.length === 0}
      <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
        {t("messages.empty.alarms")}
      </Card>
    {:else}
      <ul class="space-y-2">
        {#each alarmsSorted as a (a.central + "/" + a.id)}
          <li>
            <Card class="p-4">
              <div class="flex flex-wrap items-start justify-between gap-2">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-baseline gap-2">
                    <h3 class="font-semibold">{a.name}</h3>
                    <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(a.timestamp)}</span>
                    {#if a.counter > 1}
                      <Badge variant="warning">×{a.counter}</Badge>
                    {/if}
                    {#if a.device_name}
                      <Badge variant="muted">{a.device_name}</Badge>
                    {/if}
                    {#if a.rooms && a.rooms.length > 0}
                      <span class="text-xs text-[var(--ha-secondary-text-color)]">{a.rooms.join(", ")}</span>
                    {/if}
                    {#if alarmCentrals.length > 1 && a.central}
                      <Badge variant="muted">{a.central}</Badge>
                    {/if}
                  </div>
                  {#if a.description}
                    <p class="mt-1 text-sm text-slate-600 dark:text-slate-300">{a.description}</p>
                  {/if}
                  {#if a.last_trigger}
                    <p class="mt-1 text-xs italic text-[var(--ha-secondary-text-color)]">
                      {t("messages.last_trigger")} {a.last_trigger}
                    </p>
                  {/if}
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => void ackAlarm(a.id, a.central)}
                  disabled={acking === a.id}
                  title={t("common.acknowledge")}
                >
                  {acking === a.id ? "…" : t("common.acknowledge")}
                </Button>
              </div>
            </Card>
          </li>
        {/each}
      </ul>
    {/if}
  {:else}
    <div class="mb-3 flex flex-wrap items-center gap-2 text-xs">
      <select
        bind:value={typeFilter}
        class="rounded-md border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
      >
        <option value="">{t("messages.all_types")}</option>
        {#each serviceTypes as st (st)}
          <option value={st}>{serviceTypeLabel(st)}</option>
        {/each}
      </select>
      <label class="flex items-center gap-1 text-slate-600 dark:text-slate-400">
        <input type="checkbox" bind:checked={onlyQuittable} />
        {t("messages.quittable_only")}
      </label>
      {#if serviceCentrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-1 text-xs dark:border-slate-700 dark:bg-slate-900"
          title="CCU"
        >
          <option value="">Alle CCUs</option>
          {#each serviceCentrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <span class="text-[var(--ha-secondary-text-color)]">
        {servicesSorted.length} / {services.length}
      </span>
    </div>
    {#if servicesSorted.length === 0}
      <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
        {t("messages.empty.service")}
      </Card>
    {:else}
      <ul class="space-y-2">
        {#each servicesSorted as s (s.central + "/" + s.id)}
          <li>
            <Card class="p-4">
              <div class="flex flex-wrap items-start justify-between gap-2">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-baseline gap-2">
                    <h3 class="font-semibold">{s.name}</h3>
                    <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(s.timestamp)}</span>
                    {#if s.counter > 1}
                      <Badge variant="warning">×{s.counter}</Badge>
                    {/if}
                    {#if s.type}
                      <Badge variant="muted">{serviceTypeLabel(s.type)}</Badge>
                    {/if}
                    {#if s.quittable}
                      <Badge variant="default">{t("messages.ackable")}</Badge>
                    {/if}
                    {#if serviceCentrals.length > 1 && s.central}
                      <Badge variant="muted">{s.central}</Badge>
                    {/if}
                  </div>
                  {#if s.device_name || s.address}
                    <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
                      {s.device_name ?? ""}
                      {#if s.address}
                        <span class="font-mono">· {s.address}</span>
                      {/if}
                    </p>
                  {/if}
                </div>
                {#if s.quittable}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => void ackService(s.id, s.central)}
                    disabled={acking === s.id}
                    title={t("common.acknowledge")}
                  >
                    {acking === s.id ? "…" : t("common.acknowledge")}
                  </Button>
                {/if}
              </div>
            </Card>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</section>
