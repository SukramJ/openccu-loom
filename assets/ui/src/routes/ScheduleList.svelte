<!--
  Fleet-wide schedule overview. Answers "which devices have a week
  schedule at all" — the question the device detail can only answer one
  device at a time, and the counterpart to the direct-links list.

  Read-only by design: a row opens the device's own schedule editor,
  which is where a program is changed. The daemon derives this list from
  channel types alone, so opening it costs no CCU traffic even on a
  large fleet.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ScheduleDeviceSummary } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";

  let items = $state<ScheduleDeviceSummary[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let search = $state("");

  async function load() {
    loading = true;
    loadError = null;
    try {
      const resp = await api.listSchedules();
      items = resp.items ?? [];
    } catch (e) {
      loadError = e instanceof ApiError ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const centrals = $derived(
    [...new Set(items.map((i) => i.central).filter(Boolean))].sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    ),
  );

  function matches(item: ScheduleDeviceSummary, q: string): boolean {
    if (!q) return true;
    const needle = q.toLowerCase();
    return [item.name, item.address, item.model, item.central, item.channel?.address]
      .filter(Boolean)
      .some((v) => (v as string).toLowerCase().includes(needle));
  }

  const filtered = $derived(
    items
      .filter((i) => matches(i, search))
      .sort((a, b) => {
        const byCentral = (a.central || "").localeCompare(b.central || "");
        if (byCentral !== 0) return byCentral;
        return (a.name || a.address).localeCompare(b.name || b.address, undefined, {
          sensitivity: "base",
        });
      }),
  );

  /** The device's schedule tab — where the program is actually edited. */
  function href(item: ScheduleDeviceSummary): string {
    return `#/devices/${encodeURIComponent(item.address)}?tab=schedule`;
  }
</script>

<section class="mx-auto max-w-5xl px-4 py-6 sm:px-6">
  <PageHeader title={t("schedules.title")} subtitle={t("schedules.subtitle")} />

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if items.length === 0}
    <EmptyState
      message={t("schedules.empty")}
      description={t("schedules.empty.description")}
      icon="mdi:calendar-clock"
    />
  {:else}
    <div class="mb-4">
      <Input
        type="search"
        bind:value={search}
        placeholder={t("schedules.search")}
        aria-label={t("schedules.search")}
      />
    </div>

    {#if filtered.length === 0}
      <EmptyState message={t("schedules.no_matches")} icon="mdi:calendar-clock" />
    {:else}
      <ul class="flex flex-col gap-3">
        {#each filtered as item (item.central + "|" + item.address)}
          <li>
            <a href={href(item)} class="block no-underline">
              <Card class="flex flex-wrap items-center gap-2 p-4 hover:border-brand-500">
                <Icon
                  name="mdi:calendar-clock"
                  class="shrink-0 text-[var(--ha-secondary-text-color)]"
                />
                <span class="min-w-0 truncate font-semibold text-[var(--ha-primary-text-color)]">
                  {item.name || item.address}
                </span>
                <code class="text-xs text-[var(--ha-secondary-text-color)]">
                  {item.channel?.address ?? item.address}
                </code>
                <span class="grow"></span>
                {#if item.model}
                  <Badge variant="muted">{item.model}</Badge>
                {/if}
                <Badge variant={item.kind === "climate" ? "default" : "muted"}>
                  {item.kind === "climate" ? t("schedules.kind.climate") : t("schedules.kind.week_profile")}
                </Badge>
                {#if centrals.length > 1 && item.central}
                  <Badge variant="muted">{item.central}</Badge>
                {/if}
              </Card>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</section>
