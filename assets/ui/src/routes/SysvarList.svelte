<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SysvarEntry } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  let sysvars = $state<SysvarEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let search = $state("");
  let centralFilter = $state("");
  let drafts = $state<Record<string, unknown>>({});
  let savingName = $state<string | null>(null);
  let creating = $state(false);
  let createCentral = $state("");
  let createForm = $state<{
    name: string;
    value_type: string;
    unit: string;
    min: string;
    max: string;
    value_list: string;
  }>({ name: "", value_type: "BOOL", unit: "", min: "", max: "", value_list: "" });

  let editing = $state<SysvarEntry | null>(null);
  let editForm = $state({
    unit: "",
    min: "",
    max: "",
    value_list: "",
    description: "",
  });

  function startEdit(sv: SysvarEntry) {
    editing = sv;
    editForm = {
      unit: sv.unit ?? "",
      min: "",
      max: "",
      value_list: (sv.value_list ?? []).join(";"),
      description: sv.description ?? "",
    };
  }

  async function saveEdit() {
    if (!editing) return;
    savingName = editing.name;
    try {
      const body: Record<string, unknown> = {};
      if (editForm.unit) body.unit = editForm.unit;
      if (editForm.min) body.min = editForm.min;
      if (editForm.max) body.max = editForm.max;
      if (editForm.description) body.description = editForm.description;
      if (editForm.value_list && editing.value_type === "ENUM") {
        body.value_list = editForm.value_list
          .split(";")
          .map((s) => s.trim())
          .filter(Boolean);
      }
      await api.patchSysvar(editing.name, body, editing.central);
      toastStore.success(t("sysvars.updated", { name: editing.name }));
      editing = null;
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
      savingName = null;
    }
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      sysvars = await api.listSysvars();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  function draftKey(sv: SysvarEntry): string {
    return sv.central + "/" + sv.name;
  }

  async function save(sv: SysvarEntry) {
    const key = draftKey(sv);
    if (!(key in drafts)) return;
    savingName = sv.name;
    try {
      await api.setSysvar(sv.name, drafts[key], sv.central);
      delete drafts[key];
      drafts = { ...drafts };
      toastStore.success(t("sysvars.saved", { name: sv.name }));
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
      savingName = null;
    }
  }

  function discardDraft(sv: SysvarEntry) {
    const key = draftKey(sv);
    delete drafts[key];
    drafts = { ...drafts };
  }

  async function createSv() {
    if (!createForm.name) return;
    savingName = "__create__";
    try {
      await api.createSysvar(
        {
          name: createForm.name,
          value_type: createForm.value_type,
          unit: createForm.unit || undefined,
          min: createForm.min || undefined,
          max: createForm.max || undefined,
          value_list: createForm.value_type === "ENUM" && createForm.value_list
            ? createForm.value_list.split(";").map((s) => s.trim()).filter(Boolean)
            : undefined,
        },
        createCentral,
      );
      toastStore.success(t("sysvars.created"));
      creating = false;
      createForm = { name: "", value_type: "BOOL", unit: "", min: "", max: "", value_list: "" };
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
      savingName = null;
    }
  }

  async function deleteSv(sv: SysvarEntry) {
    const ok = await confirmStore.ask({
      title: t("sysvars.confirm_remove", { name: sv.name }),
      confirmLabel: t("common.remove"),
      destructive: true,
    });
    if (!ok) return;
    savingName = sv.name;
    try {
      await api.deleteSysvar(sv.name, sv.central);
      toastStore.success(t("sysvars.removed", { name: sv.name }));
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
      savingName = null;
    }
  }

  onMount(load);

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const s of sysvars) if (s.central) set.add(s.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const filtered = $derived.by(() => {
    const q = search.trim().toLowerCase();
    const list = [...sysvars].sort((a, b) => a.name.localeCompare(b.name));
    const bySearch = !q
      ? list
      : list.filter(
          (s) =>
            s.name.toLowerCase().includes(q) ||
            (s.description ?? "").toLowerCase().includes(q),
        );
    if (!centralFilter) return bySearch;
    return bySearch.filter((s) => s.central === centralFilter);
  });

  function currentValue(s: SysvarEntry): unknown {
    const key = draftKey(s);
    return key in drafts ? drafts[key] : s.value;
  }

  function setDraft(s: SysvarEntry, v: unknown) {
    const key = draftKey(s);
    drafts = { ...drafts, [key]: v };
  }

  function isDirty(s: SysvarEntry): boolean {
    const key = draftKey(s);
    if (!(key in drafts)) return false;
    return JSON.stringify(drafts[key]) !== JSON.stringify(s.value);
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("sysvars.title")}</h1>
      <p class="text-sm text-slate-500 dark:text-slate-400">
        {loading
          ? t("common.loading")
          : t("sysvars.count", { count: sysvars.length })}
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
      <Button type="button" size="sm" onclick={() => (creating = !creating)}>
        {creating ? t("common.cancel") : t("common.new")}
      </Button>
    </div>
  </header>

  {#if creating}
    <Card class="mb-4 p-4">
      <h2 class="mb-2 text-lg font-semibold">{t("sysvars.create.title")}</h2>
      <div class="grid grid-cols-1 gap-2 md:grid-cols-2">
        {#if centrals.length > 1}
          <label class="text-sm md:col-span-2">
            <span class="block text-xs text-slate-500 dark:text-slate-400">CCU</span>
            <select
              bind:value={createCentral}
              class="w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
            >
              <option value="">{t("common.select_placeholder")}</option>
              {#each centrals as c (c)}
                <option value={c}>{c}</option>
              {/each}
            </select>
          </label>
        {/if}
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.name")}</span>
          <Input bind:value={createForm.name} />
        </label>
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.type")}</span>
          <select
            bind:value={createForm.value_type}
            class="w-full rounded-md border border-slate-300 bg-white px-2 py-2 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          >
            <option value="BOOL">BOOL</option>
            <option value="INTEGER">INTEGER</option>
            <option value="FLOAT">FLOAT</option>
            <option value="STRING">STRING</option>
            <option value="ENUM">ENUM</option>
          </select>
        </label>
        <label class="text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.unit")}</span>
          <Input bind:value={createForm.unit} />
        </label>
        {#if createForm.value_type === "INTEGER" || createForm.value_type === "FLOAT"}
          <div class="grid grid-cols-2 gap-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.min")}</span>
              <Input bind:value={createForm.min} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.max")}</span>
              <Input bind:value={createForm.max} />
            </label>
          </div>
        {/if}
        {#if createForm.value_type === "ENUM"}
          <label class="text-sm md:col-span-2">
            <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.values")}</span>
            <Input bind:value={createForm.value_list} placeholder={t("sysvars.create.values_placeholder")} />
          </label>
        {/if}
      </div>
      <div class="mt-3 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => (creating = false)}
        >
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => void createSv()}
          disabled={!createForm.name || savingName === "__create__"}
        >
          {savingName === "__create__" ? "…" : t("common.add")}
        </Button>
      </div>
    </Card>
  {/if}

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
    <EmptyState message={t("sysvars.empty")} icon="mdi:sliders" />
  {:else}
    <ul class="space-y-2">
      {#each filtered as sv (sv.central + "/" + sv.name)}
        {@const dirty = isDirty(sv)}
        {@const saving = savingName === sv.name}
        <li>
          <Card class="p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-2">
                  <h3 class="font-mono text-sm font-semibold">{sv.name}</h3>
                  <Badge variant="muted">{sv.value_type}</Badge>
                  {#if sv.unit}
                    <span class="text-xs text-slate-500 dark:text-slate-400">{sv.unit}</span>
                  {/if}
                  {#if dirty}
                    <Badge variant="warning">{t("common.modified")}</Badge>
                  {/if}
                  {#if centrals.length > 1 && sv.central}
                    <Badge variant="muted">{sv.central}</Badge>
                  {/if}
                </div>
                {#if sv.description}
                  <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{sv.description}</p>
                {/if}
              </div>
              <div class="flex flex-wrap items-center gap-2">
                {#if sv.value_type === "BOOL"}
                  <Switch
                    checked={Boolean(currentValue(sv))}
                    onCheckedChange={(v) => setDraft(sv, v)}
                  />
                {:else if sv.value_list && sv.value_list.length > 0}
                  <Select
                    options={sv.value_list.map((label, i) => ({
                      value: String(i),
                      label,
                    }))}
                    value={currentValue(sv) != null ? String(currentValue(sv)) : ""}
                    onValueChange={(v) => setDraft(sv, Number(v))}
                  />
                {:else if sv.value_type === "INTEGER" || sv.value_type === "FLOAT"}
                  <Input
                    type="number"
                    step={sv.value_type === "FLOAT" ? "any" : "1"}
                    value={currentValue(sv) as number | null}
                    oninput={(e) => {
                      const n = Number((e.target as HTMLInputElement).value);
                      if (Number.isFinite(n)) setDraft(sv, n);
                    }}
                  />
                {:else}
                  <Input
                    type="text"
                    value={(currentValue(sv) ?? "") as string}
                    oninput={(e) => setDraft(sv, (e.target as HTMLInputElement).value)}
                  />
                {/if}
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onclick={() => discardDraft(sv)}
                  disabled={!dirty || saving}
                >
                  ×
                </Button>
                <Button
                  type="button"
                  size="sm"
                  onclick={() => void save(sv)}
                  disabled={!dirty || saving}
                >
                  {saving ? "…" : t("common.save")}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onclick={() => startEdit(sv)}
                  disabled={savingName === sv.name}
                  title={t("sysvars.edit.tooltip")}
                >
                  ⚙
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="destructive"
                  onclick={() => void deleteSv(sv)}
                  disabled={savingName === sv.name}
                  title={t("sysvars.remove.tooltip")}
                >
                  ×
                </Button>
              </div>
            </div>
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>

{#if editing}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("sysvars.edit.title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) editing = null;
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") editing = null;
    }}
  >
    <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900">
      <header class="mb-3 flex items-baseline justify-between gap-2">
        <h2 class="text-lg font-semibold">
          {t("sysvars.edit.title")}
          <span class="font-mono text-sm text-slate-500 dark:text-slate-400">{editing.name}</span>
        </h2>
        <Badge variant="muted">{editing.value_type}</Badge>
      </header>
      <p class="mb-3 text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.note")}</p>
      <div class="space-y-2">
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.edit.description")}</span>
          <Input bind:value={editForm.description} />
        </label>
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.unit")}</span>
          <Input bind:value={editForm.unit} />
        </label>
        {#if editing.value_type === "INTEGER" || editing.value_type === "FLOAT"}
          <div class="grid grid-cols-2 gap-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.min")}</span>
              <Input bind:value={editForm.min} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("common.max")}</span>
              <Input bind:value={editForm.max} />
            </label>
          </div>
        {/if}
        {#if editing.value_type === "ENUM"}
          <label class="block text-sm">
            <span class="block text-xs text-slate-500 dark:text-slate-400">{t("sysvars.create.values")}</span>
            <Input bind:value={editForm.value_list} />
          </label>
        {/if}
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => (editing = null)}
        >
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => void saveEdit()}
          disabled={!editing || savingName === editing.name}
        >
          {t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}
