<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { t } from "$lib/i18n";
  import { securityClassIcon } from "$lib/security/classes";
  import type { SecurityNotification, SecuritySnapshot } from "$lib/api/types";

  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";

  // Security & Safety overview (docs/security-safety-concept.md §7.8). Read
  // model only — every mutation for this domain lives on the Sources/Faults
  // tabs. No WS push exists for this domain yet, so this is a plain fetch +
  // manual reload rather than a live store.

  let snap = $state<SecuritySnapshot | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  async function load() {
    loading = true;
    error = null;
    try {
      snap = await api.getSecuritySnapshot();
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  type BadgeVariant = "default" | "success" | "warning" | "danger" | "muted";

  function severityVariant(sev: string): BadgeVariant {
    switch (sev) {
      case "ok":
        return "success";
      case "info":
        return "default";
      case "warning":
        return "warning";
      case "alarm":
      case "critical":
        return "danger";
      default:
        return "muted";
    }
  }

  function fmtDateTime(iso: string | undefined): string {
    if (!iso) return "";
    try {
      return new Date(iso).toLocaleString(
        prefs.locale === "de" ? "de-DE" : "en-US",
      );
    } catch {
      return iso;
    }
  }

  function sourceLabel(s: { name?: string; channel_address?: string }): string {
    return s.name || s.channel_address || "";
  }

  // First three active-source names + an overflow count, so a class tile
  // stays readable even when a dozen sensors share one class.
  function sourceSummary(sources: { name?: string; channel_address?: string }[]): string {
    const names = sources.map(sourceLabel).filter(Boolean);
    if (names.length === 0) return "";
    const shown = names.slice(0, 3).join(", ");
    const rest = names.length - 3;
    return rest > 0 ? `${shown} ${t("security.overview.sources_more", { count: rest })}` : shown;
  }

  const zones = $derived(snap?.zones ?? []);
  const faults = $derived(snap?.faults ?? []);
  const classes = $derived(snap?.classes ?? []);

  const nothingAtAll = $derived(
    !!snap &&
      classes.length === 0 &&
      zones.length === 0 &&
      faults.length === 0 &&
      !snap.last_alarm &&
      !snap.last_fault,
  );
</script>

{#snippet report(n: SecurityNotification, titleKey: string, borderColor: string)}
  <Card class="border-l-4 p-4" style="border-left-color: {borderColor};">
    <p class="text-xs font-medium uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      {t(titleKey)}
    </p>
    <p class="mt-1 text-base font-semibold">{n.subject}</p>
    <p class="mt-1 text-sm text-[var(--ha-secondary-text-color)]">{n.message}</p>
    <p class="mt-2 text-xs text-[var(--ha-secondary-text-color)]">{fmtDateTime(n.at)}</p>
  </Card>
{/snippet}

{#if loading && !snap}
  <LoadingState />
{:else if error}
  <ErrorState message={error} onRetry={() => void load()} />
{:else if snap && nothingAtAll}
  <EmptyState
    icon="mdi:shield-alert"
    message={t("security.overview.empty")}
    description={t("security.overview.empty.description")}
  />
{:else if snap}
  <div class="flex flex-col gap-4">
    <!-- Last report, first: this is what an operator reads first. -->
    {#if snap.last_alarm || snap.last_fault}
      <div class="grid gap-3 md:grid-cols-2">
        {#if snap.last_alarm}
          {@render report(
            snap.last_alarm,
            "security.overview.last_alarm_title",
            "var(--ha-error-color)",
          )}
        {/if}
        {#if snap.last_fault}
          {@render report(
            snap.last_fault,
            "security.overview.last_fault_title",
            "var(--ha-warning-color)",
          )}
        {/if}
      </div>
    {:else}
      <p class="text-sm text-[var(--ha-secondary-text-color)]">
        {t("security.overview.no_report")}
      </p>
    {/if}

    <!-- Status toolbar: folded severity + engine health + fault count. -->
    <div class="flex flex-wrap items-center gap-3">
      <Badge variant={severityVariant(snap.severity)} class="gap-1.5 px-3 py-1 text-sm">
        <span class="inline-block h-2 w-2 rounded-full" style="background: currentColor;"></span>
        {t(`security.severity.${snap.severity}`)}
      </Badge>
      <Badge variant={snap.engine_healthy ? "success" : "danger"}>
        {snap.engine_healthy
          ? t("security.overview.engine_healthy")
          : t("security.overview.engine_unhealthy")}
      </Badge>
      <a href="#/security/faults" class="ml-auto">
        <Badge variant={faults.length > 0 ? "danger" : "muted"} class="gap-1">
          <Icon name="mdi:alert-triangle" size={14} aria-label="" />
          {faults.length > 0
            ? t("security.overview.faults_count", { count: faults.length })
            : t("security.overview.faults_none")}
        </Badge>
      </a>
    </div>

    <!-- Hazard & fault classes. -->
    <div>
      <h2 class="mb-2 text-sm font-semibold text-[var(--ha-secondary-text-color)]">
        {t("security.overview.classes_title")}
      </h2>
      {#if classes.length === 0}
        <EmptyState
          icon="mdi:shield-alert"
          message={t("security.overview.no_classes")}
          description={t("security.overview.no_classes.description")}
        />
      {:else}
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {#each classes as cls (cls.class)}
            {@const sources = cls.sources ?? []}
            <Card class="flex flex-col gap-2 p-4">
              <div class="flex items-start justify-between gap-2">
                <div class="flex items-center gap-2">
                  <Icon
                    name={securityClassIcon(cls.class)}
                    size={20}
                    class="shrink-0 text-[var(--ha-secondary-text-color)]"
                    aria-label=""
                  />
                  <h3 class="text-sm font-semibold">{t(`security.class.${cls.class}`)}</h3>
                </div>
                <Badge variant={cls.active ? "danger" : "muted"}>
                  {cls.active
                    ? t("security.overview.class_active", { count: sources.length })
                    : t("security.overview.class_inactive")}
                </Badge>
              </div>
              <p class="text-xs text-[var(--ha-secondary-text-color)]">
                {t("security.overview.class_known", { count: cls.known })}
              </p>
              {#if sources.length > 0}
                <p class="text-xs text-[var(--ha-primary-text-color)]">{sourceSummary(sources)}</p>
              {/if}
              {#if cls.since}
                <p class="text-xs text-[var(--ha-secondary-text-color)]">
                  {t("security.overview.class_since", { time: fmtDateTime(cls.since) })}
                </p>
              {/if}
              {#if cls.centrals && cls.centrals.length > 0}
                <p class="truncate text-xs text-[var(--ha-secondary-text-color)]">
                  {cls.centrals.join(", ")}
                </p>
              {/if}
            </Card>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Zones — empty when the alarm engine is disabled. That is a feature
         of this domain, not an error, so it gets a plain explanation
         instead of an error/empty-list treatment. -->
    <div>
      <h2 class="mb-2 text-sm font-semibold text-[var(--ha-secondary-text-color)]">
        {t("security.overview.zones_title")}
      </h2>
      {#if zones.length === 0}
        <EmptyState
          icon="mdi:information-outline"
          message={t("security.overview.zones_empty")}
          description={t("security.overview.zones_empty.description")}
        >
          {#snippet action()}
            <a href="#/alarm">
              <Button variant="outline" size="sm">{t("security.overview.zones_open_alarm")}</Button>
            </a>
          {/snippet}
        </EmptyState>
      {:else}
        <div class="grid gap-3 sm:grid-cols-2">
          {#each zones as zone (zone.id)}
            <Card class="flex items-center justify-between gap-3 p-4">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium">{zone.name}</p>
                {#if zone.since}
                  <p class="text-xs text-[var(--ha-secondary-text-color)]">
                    {t("security.overview.class_since", { time: fmtDateTime(zone.since) })}
                  </p>
                {/if}
              </div>
              <Badge variant={zone.state === "triggered" ? "danger" : "default"}>
                {t(`alarm.state.${zone.state}`)}
              </Badge>
            </Card>
          {/each}
        </div>
        <a href="#/alarm" class="mt-2 inline-block">
          <Button variant="ghost" size="sm">{t("security.overview.zones_open_alarm")}</Button>
        </a>
      {/if}
    </div>
  </div>
{/if}
