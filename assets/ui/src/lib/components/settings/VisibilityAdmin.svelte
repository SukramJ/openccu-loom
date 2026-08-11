<script lang="ts">
  import { onMount } from "svelte";
  import { visibilityStore } from "$lib/stores/visibility.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import type { UnIgnoreCandidateGroup } from "$lib/api/visibility-types";
  import {
    activeScopeCount,
    defaultReasonFilter,
    emptyFilter,
    filterGroups,
    groupKey,
    groupState,
    orphanPatterns,
    presentReasons,
    reasonCounts,
    reasonLabelKey,
    reasonHelpKey,
    reasonBadgeText,
    suppressedCount,
    toggleGroup,
    togglePattern,
    type CandidateFilter,
  } from "$lib/visibility/candidates";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";

  let selectedCentral = $state<string>("");
  let customPattern = $state("");
  let savingCentral = $state<string | null>(null);
  let expanded = $state<Set<string>>(new Set());
  let filter = $state<CandidateFilter>(emptyFilter());
  // The default reason filter depends on which reasons the fleet
  // actually produces, so it is applied once the first candidate load
  // lands rather than at construction.
  let filterInitialised = $state(false);

  onMount(async () => {
    await Promise.all([
      visibilityStore.loadCentrals(),
      // Both paramsets arrive in `groups` regardless of this flag; it
      // only governs the legacy flat `candidates` field.
      visibilityStore.loadCandidates(true),
    ]);
    if (!selectedCentral && visibilityStore.centrals.length > 0) {
      selectedCentral = visibilityStore.centrals[0].central_name;
    }
    initFilter();
  });

  function initFilter() {
    if (filterInitialised || visibilityStore.groups.length === 0) return;
    filter = {
      ...filter,
      reasons: defaultReasonFilter(
        visibilityStore.groups,
        visibilityStore.reasonVocabulary,
      ),
    };
    filterInitialised = true;
  }

  const centralOptions = $derived(
    visibilityStore.centrals.map((c) => c.central_name),
  );

  const effective = $derived.by<string[]>(() => {
    if (!selectedCentral) return [];
    return visibilityStore.effectivePatterns(selectedCentral);
  });

  const activeSet = $derived(new Set(effective));

  const groups = $derived(visibilityStore.groups);

  const visibleGroups = $derived(filterGroups(groups, filter, activeSet));

  const availableReasons = $derived(
    presentReasons(groups, visibilityStore.reasonVocabulary),
  );

  const counts = $derived(reasonCounts(groups, filter, activeSet));

  const hiddenByFilter = $derived(suppressedCount(groups, filter, activeSet));

  const paramsets = $derived.by<string[]>(() => {
    const set = new Set<string>();
    for (const g of groups) set.add(g.paramset);
    return Array.from(set).sort();
  });

  const activeGroupCount = $derived(
    groups.filter((g) => groupState(g, activeSet) !== "off").length,
  );

  const orphans = $derived(orphanPatterns(groups, effective));

  const hasPending = $derived(
    selectedCentral ? visibilityStore.hasPending(selectedCentral) : false,
  );

  const pendingCount = $derived.by<number>(() => {
    if (!selectedCentral) return 0;
    const saved = new Set(visibilityStore.activePatterns(selectedCentral));
    let diff = 0;
    for (const p of effective) if (!saved.has(p)) diff++;
    for (const p of saved) if (!activeSet.has(p)) diff++;
    return diff;
  });

  function toggleExpanded(key: string) {
    const next = new Set(expanded);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expanded = next;
  }

  function toggleReason(reason: string) {
    const next = new Set(filter.reasons);
    if (next.has(reason)) next.delete(reason);
    else next.add(reason);
    filter = { ...filter, reasons: next };
  }

  function toggleParamset(paramset: string) {
    const next = new Set(filter.paramsets);
    if (next.has(paramset)) next.delete(paramset);
    else next.add(paramset);
    filter = { ...filter, paramsets: next };
  }

  function showAllReasons() {
    filter = { ...filter, reasons: new Set() };
  }

  function resetFilter() {
    filter = {
      ...emptyFilter(),
      reasons: defaultReasonFilter(groups, visibilityStore.reasonVocabulary),
    };
  }

  function onToggleGroup(group: UnIgnoreCandidateGroup) {
    if (!selectedCentral) return;
    visibilityStore.setPatterns(
      selectedCentral,
      toggleGroup(group, effective),
    );
  }

  function onTogglePattern(group: UnIgnoreCandidateGroup, pattern: string) {
    if (!selectedCentral) return;
    visibilityStore.setPatterns(
      selectedCentral,
      togglePattern(group, pattern, effective),
    );
  }

  function removeOrphan(pattern: string) {
    if (!selectedCentral) return;
    visibilityStore.setPatterns(
      selectedCentral,
      effective.filter((p) => p !== pattern),
    );
  }

  function addCustom() {
    if (!selectedCentral) return;
    if (!customPattern.trim()) return;
    visibilityStore.addPattern(selectedCentral, customPattern.trim());
    customPattern = "";
  }

  // Saving deliberately runs without a confirm dialog: a MASTER change
  // needs no device restart, so the un-ignore concept dropped the
  // confirm step to keep VALUES and MASTER behaving alike. The result
  // is reported by a toast.
  async function save() {
    if (!selectedCentral) return;
    savingCentral = selectedCentral;
    try {
      const resp = await visibilityStore.save(selectedCentral);
      if (resp && resp.parse_errors && resp.parse_errors.length > 0) {
        toastStore.error(
          t("unignore.saved_with_errors", {
            count: String(resp.parse_errors.length),
          }),
        );
      } else if (resp) {
        toastStore.success(
          t("unignore.saved", { count: String(resp.applied_count) }),
        );
      }
    } catch (e) {
      toastStore.error(
        t("unignore.save_failed", {
          err: e instanceof Error ? e.message : String(e),
        }),
      );
    } finally {
      savingCentral = null;
    }
  }

  function discard() {
    if (!selectedCentral) return;
    visibilityStore.discardPending(selectedCentral);
  }

  async function reloadCentrals() {
    await Promise.all([
      visibilityStore.loadCentrals(),
      visibilityStore.loadCandidates(true),
    ]);
    initFilter();
  }

  /** Short summary of where a group is currently enabled. */
  function scopeSummary(group: UnIgnoreCandidateGroup): string {
    const state = groupState(group, activeSet);
    if (state === "all") return t("unignore.scope.all_devices");
    if (state === "partial") {
      return t("unignore.scope.partial", {
        count: String(activeScopeCount(group, activeSet)),
      });
    }
    return t("unignore.scope.models", {
      count: String(group.models?.length ?? 0),
    });
  }

  function reasonVariant(
    reason: string,
  ): "default" | "success" | "warning" | "danger" | "muted" {
    switch (reason) {
      case "operation_mode":
        return "warning";
      case "master_gate":
        return "default";
      case "hidden":
      case "device_specific":
        return "success";
      case "unknown":
        return "danger";
      default:
        return "muted";
    }
  }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h3
      class="text-sm font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase"
    >
      {t("settings.tab.visibility")}
    </h3>
    <Button
      type="button"
      variant="outline"
      size="sm"
      onclick={() => void reloadCentrals()}
      disabled={visibilityStore.centralsLoading ||
        visibilityStore.candidatesLoading}
    >
      {t("common.reload")}
    </Button>
  </div>

  <p class="text-sm text-[var(--ha-secondary-text-color)]">
    {t("unignore.subtitle")}
  </p>
  <p class="text-sm text-amber-600 dark:text-amber-400">
    ⚠ {t("unignore.warning")}
  </p>

  {#if visibilityStore.centralsLoading || visibilityStore.candidatesLoading}
    <LoadingState />
  {:else if visibilityStore.centralsError}
    <ErrorState
      message={visibilityStore.centralsError}
      onRetry={reloadCentrals}
    />
  {:else if visibilityStore.candidatesError}
    <ErrorState
      message={visibilityStore.candidatesError}
      onRetry={reloadCentrals}
    />
  {:else if centralOptions.length === 0}
    <EmptyState message={t("unignore.no_centrals")} icon="mdi:server" />
  {:else}
    <Card>
      <!-- Central selector + headline counters -->
      <div
        class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-[var(--ha-divider-color)] p-4"
      >
        <span class="flex items-center gap-2 text-sm font-medium">
          {t("unignore.central_label")}
          <Select
            class="w-auto"
            bind:value={selectedCentral}
            options={centralOptions.map((name) => ({
              value: name,
              label: name,
            }))}
          />
        </span>
        <p
          class="text-sm text-[var(--ha-secondary-text-color)]"
          data-testid="unignore-stats"
        >
          {t("unignore.stats", {
            total: String(groups.length),
            active: String(activeGroupCount),
            pending: String(pendingCount),
          })}
        </p>
      </div>

      <!-- Filters -->
      <div
        class="space-y-3 border-b border-[var(--ha-divider-color)] p-4"
        data-testid="unignore-filters"
      >
        <div class="flex flex-wrap items-center gap-2">
          <span
            class="flex items-center gap-1 text-xs font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase"
          >
            <Icon name="mdi:filter" class="h-3.5 w-3.5" />
            {t("unignore.filter.categories")}
          </span>
          {#each availableReasons as reason (reason)}
            <button
              type="button"
              class="rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {filter.reasons.has(
                reason,
              ) || filter.reasons.size === 0
                ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]'}"
              aria-pressed={filter.reasons.has(reason) ||
                filter.reasons.size === 0}
              title={t(reasonHelpKey(reason))}
              data-testid="reason-chip-{reason}"
              onclick={() => toggleReason(reason)}
            >
              {t(reasonLabelKey(reason))}
              <span class="ml-1 opacity-70">{counts.get(reason) ?? 0}</span>
            </button>
          {/each}
        </div>

        <div class="flex flex-wrap items-center gap-3">
          {#if paramsets.length > 1}
            <span class="flex flex-wrap items-center gap-2">
              <span
                class="text-xs font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase"
              >
                {t("unignore.filter.paramset")}
              </span>
              {#each paramsets as paramset (paramset)}
                <button
                  type="button"
                  class="rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {filter.paramsets.has(
                    paramset,
                  ) || filter.paramsets.size === 0
                    ? 'border-[var(--ha-primary-color)] bg-[color-mix(in_srgb,var(--ha-primary-color)_15%,transparent)] text-[var(--ha-primary-color)]'
                    : 'border-[var(--ha-divider-color)] text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]'}"
                  aria-pressed={filter.paramsets.has(paramset) ||
                    filter.paramsets.size === 0}
                  data-testid="paramset-chip-{paramset}"
                  onclick={() => toggleParamset(paramset)}
                >
                  {paramset}
                </button>
              {/each}
            </span>
          {/if}

          <label class="flex items-center gap-2 text-sm">
            <Switch
              checked={filter.onlyEnabled}
              ariaLabel={t("unignore.filter.only_enabled")}
              onCheckedChange={(v) => (filter = { ...filter, onlyEnabled: v })}
            />
            {t("unignore.filter.only_enabled")}
          </label>

          <div class="ml-auto w-full max-w-xs">
            <Input
              type="search"
              placeholder={t("unignore.search_placeholder")}
              aria-label={t("unignore.search_placeholder")}
              data-testid="unignore-search"
              value={filter.query}
              oninput={(e) =>
                (filter = {
                  ...filter,
                  query: (e.currentTarget as HTMLInputElement).value,
                })}
            />
          </div>
        </div>

        {#if hiddenByFilter > 0}
          <p class="text-xs text-[var(--ha-secondary-text-color)]">
            {t("unignore.filter.hidden_notice", {
              count: String(hiddenByFilter),
            })}
            <button
              type="button"
              class="ml-1 underline hover:text-[var(--ha-primary-color)]"
              data-testid="unignore-show-all"
              onclick={showAllReasons}
            >
              {t("unignore.filter.show_all")}
            </button>
          </p>
        {/if}
      </div>

      <!-- Parameter list -->
      <div class="p-4">
        {#if groups.length === 0}
          <EmptyState
            message={t("unignore.no_candidates")}
            icon="mdi:hidden"
          />
        {:else if visibleGroups.length === 0}
          <EmptyState
            message={t("unignore.no_filter_match")}
            description={t("unignore.no_filter_match_hint")}
            icon="mdi:filter"
          />
          <div class="mt-3 flex justify-center">
            <Button variant="outline" size="sm" onclick={resetFilter}>
              {t("unignore.filter.reset")}
            </Button>
          </div>
        {:else}
          <ul class="divide-y divide-[var(--ha-divider-color)]">
            {#each visibleGroups as group (groupKey(group))}
              {@const key = groupKey(group)}
              {@const state = groupState(group, activeSet)}
              {@const isOpen = expanded.has(key)}
              <li data-testid="unignore-group" data-parameter={group.parameter}>
                <div class="flex items-center gap-3 py-2">
                  <input
                    type="checkbox"
                    class="h-4 w-4 shrink-0 accent-[var(--ha-primary-color)]"
                    checked={state === "all"}
                    indeterminate={state === "partial"}
                    aria-label={t("unignore.toggle_parameter", {
                      parameter: group.parameter,
                    })}
                    data-testid="group-toggle-{group.parameter}"
                    onchange={() => onToggleGroup(group)}
                  />
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <code class="font-mono text-sm break-all"
                        >{group.parameter}</code
                      >
                      <Badge
                        variant={reasonVariant(group.reason)}
                        title={t(reasonHelpKey(group.reason))}
                      >
                        {reasonBadgeText(group, t)}
                      </Badge>
                      {#if group.paramset === "MASTER"}
                        <Badge variant="muted">MASTER</Badge>
                      {/if}
                    </div>
                    {#if group.label}
                      <p
                        class="truncate text-xs text-[var(--ha-secondary-text-color)]"
                      >
                        {group.label}
                      </p>
                    {/if}
                  </div>
                  <span
                    class="hidden shrink-0 text-xs text-[var(--ha-secondary-text-color)] sm:inline"
                  >
                    {scopeSummary(group)}
                  </span>
                  <button
                    type="button"
                    class="shrink-0 rounded p-1 text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]"
                    aria-expanded={isOpen}
                    aria-label={t("unignore.toggle_scopes", {
                      parameter: group.parameter,
                    })}
                    data-testid="group-expand-{group.parameter}"
                    onclick={() => toggleExpanded(key)}
                  >
                    <Icon
                      name={isOpen ? "mdi:chevron-down" : "mdi:chevron-right"}
                      class="h-4 w-4"
                    />
                  </button>
                </div>

                {#if isOpen}
                  <div
                    class="mb-2 ml-7 space-y-2 rounded-md bg-[var(--ha-secondary-background-color)] p-3"
                    data-testid="group-scopes-{group.parameter}"
                  >
                    {#if group.simple_pattern}
                      <label class="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          class="h-4 w-4 accent-[var(--ha-primary-color)]"
                          checked={activeSet.has(group.simple_pattern)}
                          onchange={() =>
                            onTogglePattern(
                              group,
                              group.simple_pattern as string,
                            )}
                        />
                        <span>{t("unignore.scope.all_devices")}</span>
                        <code
                          class="font-mono text-xs text-[var(--ha-secondary-text-color)]"
                          >{group.simple_pattern}</code
                        >
                      </label>
                    {/if}
                    {#each group.models ?? [] as model (model.model)}
                      <div class="space-y-1">
                        <div class="flex flex-wrap items-center gap-2 text-sm">
                          {#if model.wildcard_pattern}
                            <label class="flex items-center gap-2">
                              <input
                                type="checkbox"
                                class="h-4 w-4 accent-[var(--ha-primary-color)]"
                                checked={activeSet.has(model.wildcard_pattern)}
                                onchange={() =>
                                  onTogglePattern(
                                    group,
                                    model.wildcard_pattern as string,
                                  )}
                              />
                              <span class="font-medium">{model.model}</span>
                              <span
                                class="text-xs text-[var(--ha-secondary-text-color)]"
                                >{t("unignore.scope.all_channels")}</span
                              >
                            </label>
                          {:else}
                            <span class="font-medium">{model.model}</span>
                          {/if}
                          <span
                            class="text-xs text-[var(--ha-secondary-text-color)]"
                          >
                            {t("unignore.scope.device_count", {
                              count: String(model.device_count),
                            })}
                          </span>
                        </div>
                        <div class="ml-6 flex flex-wrap gap-x-4 gap-y-1">
                          {#each model.channels ?? [] as ch (ch.channel)}
                            <label
                              class="flex items-center gap-1.5 text-xs text-[var(--ha-secondary-text-color)]"
                            >
                              <input
                                type="checkbox"
                                class="h-3.5 w-3.5 accent-[var(--ha-primary-color)]"
                                checked={activeSet.has(ch.pattern)}
                                onchange={() => onTogglePattern(group, ch.pattern)}
                              />
                              {t("unignore.scope.channel", {
                                channel: String(ch.channel),
                              })}
                            </label>
                          {/each}
                        </div>
                      </div>
                    {/each}
                  </div>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <!-- Patterns with no matching candidate -->
      {#if orphans.length > 0}
        <div class="border-t border-[var(--ha-divider-color)] p-4">
          <p class="text-sm font-medium">{t("unignore.orphans_title")}</p>
          <p class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
            {t("unignore.orphans_hint")}
          </p>
          <ul class="mt-2 space-y-1" data-testid="unignore-orphans">
            {#each orphans as pattern (pattern)}
              <li class="flex items-center gap-2">
                <code class="font-mono text-xs break-all">{pattern}</code>
                <Badge variant="warning">{t("unignore.no_match")}</Badge>
                <button
                  type="button"
                  class="rounded p-1 text-[var(--ha-secondary-text-color)] hover:text-[var(--ha-error-color)]"
                  aria-label={t("unignore.remove_pattern", { pattern })}
                  onclick={() => removeOrphan(pattern)}
                >
                  <Icon name="mdi:close" class="h-3.5 w-3.5" />
                </button>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      <!-- Custom pattern add -->
      <div
        class="flex flex-wrap items-center gap-2 border-t border-[var(--ha-divider-color)] p-4"
      >
        <Input
          bind:value={customPattern}
          placeholder={t("unignore.add_pattern_placeholder")}
          aria-label={t("unignore.add_pattern_placeholder")}
        />
        <Button onclick={addCustom} disabled={!customPattern.trim()}>
          {t("unignore.add_pattern")}
        </Button>
      </div>

      <!-- Save / discard -->
      <div
        class="sticky bottom-0 flex flex-wrap items-center gap-2 border-t border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] p-4"
      >
        {#if hasPending}
          <span class="text-sm text-amber-600 dark:text-amber-400">
            {t("unignore.pending_changes", { count: String(pendingCount) })}
          </span>
        {/if}
        <div class="ml-auto flex gap-2">
          <Button
            variant="outline"
            onclick={discard}
            disabled={!hasPending || savingCentral !== null}
          >
            {t("unignore.discard")}
          </Button>
          <Button
            onclick={save}
            disabled={!hasPending || savingCentral !== null}
            data-testid="unignore-save"
          >
            {savingCentral === selectedCentral
              ? t("common.saving")
              : t("unignore.save")}
          </Button>
        </div>
      </div>

      {#if visibilityStore.lastSave && visibilityStore.lastSave.parse_errors && visibilityStore.lastSave.parse_errors.length > 0}
        <div class="border-t border-[var(--ha-divider-color)] p-4 text-sm">
          <p class="font-semibold text-red-600 dark:text-red-400">
            {t("unignore.parse_errors_title")}
          </p>
          <ul class="mt-2 list-disc pl-5 text-red-600 dark:text-red-400">
            {#each visibilityStore.lastSave.parse_errors as err (err)}
              <li>{err}</li>
            {/each}
          </ul>
        </div>
      {/if}
    </Card>
  {/if}
</div>
