<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { ProgramEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let programs = $state<ProgramEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let search = $state("");
  let centralFilter = $state("");
  let runningId = $state<string | null>(null);
  let togglingId = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      programs = await api.listPrograms();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function execute(id: string, name: string, central: string) {
    const ok = await confirmStore.ask({
      title: t("programs.confirm_run", { name }),
      confirmLabel: t("programs.run"),
      destructive: false,
    });
    if (!ok) return;
    runningId = id;
    try {
      await api.executeProgram(id, central);
      toastStore.success(t("programs.executed", { name }));
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      runningId = null;
    }
  }

  async function toggle(id: string, name: string, current: boolean | undefined, central: string) {
    togglingId = id;
    try {
      const next = !(current === true);
      await api.setProgramEnabled(id, next, central);
      toastStore.success(
        t("programs.toggle_done", {
          name,
          state: next ? t("programs.enabled") : t("programs.disabled"),
        }),
      );
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      togglingId = null;
    }
  }

  onMount(load);

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const p of programs) if (p.central) set.add(p.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const filtered = $derived.by(() => {
    const q = search.trim().toLowerCase();
    const list = [...programs].sort((a, b) => a.name.localeCompare(b.name));
    const bySearch = !q
      ? list
      : list.filter(
          (p) =>
            p.name.toLowerCase().includes(q) ||
            (p.description ?? "").toLowerCase().includes(q),
        );
    if (!centralFilter) return bySearch;
    return bySearch.filter((p) => p.central === centralFilter);
  });
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("programs.title")}</h1>
      <p class="text-sm text-slate-500 dark:text-slate-400">
        {loading
          ? t("common.loading")
          : t("programs.count", { count: programs.length })}
      </p>
    </div>
    <div class="flex items-center gap-2">
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title="CCU"
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    </div>
  </header>

  <div class="mb-4 max-w-md">
    <Input
      type="search"
      placeholder={t("common.search")}
      bind:value={search}
    />
  </div>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if filtered.length === 0}
    <EmptyState message={t("programs.empty")} icon="mdi:play" />
  {:else}
    <ul class="grid grid-cols-1 gap-3 md:grid-cols-2">
      {#each filtered as p (p.central + "/" + p.id)}
        {@const running = runningId === p.id}
        <li>
          <Card class="flex h-full flex-col justify-between p-4">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <h3 class="truncate font-semibold">{p.name}</h3>
                {#if p.active === true}
                  <Badge variant="default">{t("programs.active")}</Badge>
                {:else if p.active === false}
                  <Badge variant="muted">{t("programs.inactive")}</Badge>
                {/if}
                {#if centrals.length > 1 && p.central}
                  <Badge variant="muted">{p.central}</Badge>
                {/if}
              </div>
              {#if p.description}
                <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{p.description}</p>
              {/if}
              <p class="mt-1 font-mono text-[10px] text-slate-500 dark:text-slate-400">{p.id}</p>
            </div>
            <div class="mt-3 flex justify-end gap-2">
              {#if p.active !== undefined}
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onclick={() => void toggle(p.id, p.name, p.active, p.central)}
                  disabled={togglingId === p.id}
                  title={t("programs.toggle.tooltip")}
                >
                  {togglingId === p.id
                    ? "…"
                    : p.active
                      ? t("common.disable")
                      : t("common.enable")}
                </Button>
              {/if}
              <Button
                type="button"
                size="sm"
                onclick={() => void execute(p.id, p.name, p.central)}
                disabled={running}
              >
                {running ? t("programs.running") : t("programs.run")}
              </Button>
            </div>
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>
