<script lang="ts">
  import { onMount } from "svelte";
  import { visibilityStore } from "$lib/stores/visibility.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  let selectedCentral = $state<string>("");
  let searchText = $state("");
  let customPattern = $state("");
  let savingCentral = $state<string | null>(null);
  let includeMaster = $state(false);

  onMount(async () => {
    await Promise.all([
      visibilityStore.loadCentrals(),
      visibilityStore.loadCandidates(includeMaster),
    ]);
    if (!selectedCentral && visibilityStore.centrals.length > 0) {
      selectedCentral = visibilityStore.centrals[0].central_name;
    }
  });

  function toggleIncludeMaster() {
    includeMaster = !includeMaster;
    void visibilityStore.loadCandidates(includeMaster);
  }

  const centralOptions = $derived(
    visibilityStore.centrals.map((c) => c.central_name),
  );

  const effective = $derived.by<string[]>(() => {
    if (!selectedCentral) return [];
    return visibilityStore.effectivePatterns(selectedCentral);
  });

  const effectiveSet = $derived(new Set(effective));

  const pickerItems = $derived.by<string[]>(() => {
    const set = new Set<string>(visibilityStore.candidates);
    for (const p of effective) set.add(p);
    const q = searchText.trim().toLowerCase();
    const items = Array.from(set);
    items.sort();
    if (!q) return items;
    return items.filter((p) => p.toLowerCase().includes(q));
  });

  const hasPending = $derived(
    selectedCentral ? visibilityStore.hasPending(selectedCentral) : false,
  );

  function toggle(pattern: string) {
    if (!selectedCentral) return;
    visibilityStore.togglePattern(selectedCentral, pattern);
  }

  function addCustom() {
    if (!selectedCentral) return;
    if (!customPattern.trim()) return;
    visibilityStore.addPattern(selectedCentral, customPattern.trim());
    customPattern = "";
  }

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
      visibilityStore.loadCandidates(includeMaster),
    ]);
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-8 sm:px-6">
  <header class="mb-6">
    <h1 class="text-2xl font-semibold">{t("unignore.title")}</h1>
    <p class="mt-2 text-sm text-slate-500 dark:text-slate-400">
      {t("unignore.subtitle")}
    </p>
    <p class="mt-2 text-sm text-amber-600 dark:text-amber-400">
      ⚠ {t("unignore.warning")}
    </p>
  </header>

  {#if visibilityStore.centralsLoading || visibilityStore.candidatesLoading}
    <LoadingState />
  {:else if visibilityStore.centralsError}
    <ErrorState message={visibilityStore.centralsError} onRetry={reloadCentrals} />
  {:else if centralOptions.length === 0}
    <EmptyState message={t("unignore.no_centrals")} icon="mdi:server" />
  {:else}
    <Card>
      <div class="flex flex-wrap items-center gap-3 p-4">
        <label class="text-sm font-medium">
          {t("unignore.central_label")}
          <select
            bind:value={selectedCentral}
            class="ml-2 rounded border border-slate-300 bg-white px-2 py-1 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          >
            {#each centralOptions as name (name)}
              <option value={name}>{name}</option>
            {/each}
          </select>
        </label>

        <label class="flex items-center gap-2 text-sm sm:ml-auto">
          <input
            type="checkbox"
            checked={includeMaster}
            onchange={toggleIncludeMaster}
          />
          {t("unignore.include_master")}
        </label>
      </div>

      <div class="border-t border-slate-200 p-4 dark:border-slate-800">
        <Input
          bind:value={searchText}
          placeholder={t("unignore.search_placeholder")}
          aria-label={t("unignore.search_placeholder")}
        />

        <ul
          class="mt-3 max-h-[480px] divide-y divide-slate-100 overflow-y-auto rounded border border-slate-200 dark:divide-slate-800 dark:border-slate-700"
        >
          {#each pickerItems as pattern (pattern)}
            <li class="flex items-center gap-3 px-3 py-2 text-sm">
              <input
                type="checkbox"
                checked={effectiveSet.has(pattern)}
                onchange={() => toggle(pattern)}
              />
              <code class="font-mono">{pattern}</code>
              {#if !visibilityStore.candidates.includes(pattern)}
                <span class="ml-auto text-xs text-slate-500 dark:text-slate-400">
                  {t("unignore.no_match")}
                </span>
              {/if}
            </li>
          {:else}
            <li class="px-3 py-3 text-sm text-slate-500 dark:text-slate-400">
              {t("unignore.no_candidates")}
            </li>
          {/each}
        </ul>
      </div>

      <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 p-4 dark:border-slate-800">
        <Input
          bind:value={customPattern}
          placeholder={t("unignore.add_pattern_placeholder")}
          aria-label={t("unignore.add_pattern_placeholder")}
        />
        <Button onclick={addCustom} disabled={!customPattern.trim()}>
          {t("unignore.add_pattern")}
        </Button>
      </div>

      <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 p-4 dark:border-slate-800">
        {#if hasPending}
          <span class="text-sm text-amber-600 dark:text-amber-400">
            {t("unignore.unsaved_changes")}
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
          >
            {savingCentral === selectedCentral
              ? t("common.saving")
              : t("unignore.save")}
          </Button>
        </div>
      </div>

      {#if visibilityStore.lastSave && visibilityStore.lastSave.parse_errors && visibilityStore.lastSave.parse_errors.length > 0}
        <div class="border-t border-slate-200 p-4 text-sm dark:border-slate-800">
          <p class="font-semibold text-red-600 dark:text-red-400">{t("unignore.parse_errors_title")}</p>
          <ul class="mt-2 list-disc pl-5 text-red-600 dark:text-red-400">
            {#each visibilityStore.lastSave.parse_errors as err (err)}
              <li>{err}</li>
            {/each}
          </ul>
        </div>
      {/if}
    </Card>
  {/if}
</section>
