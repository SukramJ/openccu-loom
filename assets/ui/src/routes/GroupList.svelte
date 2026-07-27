<!--
  Heating-groups view (GR01 read + GR02 admin). Groups (HmIP / BidCos)
  are read from the CCU's groups.gson via `CCU.getHeatingGroupList`;
  create / edit / delete run through the CCU jpages proxy (ADR 0055)
  behind the New / Edit / Delete controls. Admin-gated on the server.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { GroupCentralEntry, GroupEntry } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import GroupEditor from "$lib/components/groups/GroupEditor.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";

  let entries = $state<GroupCentralEntry[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("groups:central"));
  $effect(() => saveLS("groups:central", centralFilter));

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.getGroups();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  // Deterministic ordering — a central's card grid should not reshuffle
  // between renders just because the REST response happened to change
  // entry order.
  const centrals = $derived(
    [...entries]
      .map((e) => e.central)
      .sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" })),
  );

  const filteredEntries = $derived(
    centralFilter ? entries.filter((e) => e.central === centralFilter) : entries,
  );

  const totalGroups = $derived(
    entries.reduce((sum, e) => sum + e.groups.length, 0),
  );

  // --- create / edit / delete (GR02) -----------------------------------
  let editorOpen = $state(false);
  let editorCentral = $state("");
  let editorGroup = $state<GroupEntry | undefined>(undefined);

  // The central a new group targets: the active filter, or the sole CCU.
  const createCentral = $derived(
    centralFilter || (centrals.length === 1 ? centrals[0] : ""),
  );
  const canCreate = $derived(centrals.length > 0);

  function openCreate() {
    if (!createCentral) {
      toastStore.error(t("groups.select_ccu_first"));
      return;
    }
    editorCentral = createCentral;
    editorGroup = undefined;
    editorOpen = true;
  }

  function openEdit(central: string, g: GroupEntry) {
    editorCentral = central;
    editorGroup = g;
    editorOpen = true;
  }

  function closeEditor() {
    editorOpen = false;
    editorGroup = undefined;
  }

  async function onEditorSaved() {
    closeEditor();
    await load();
  }

  async function del(central: string, g: GroupEntry) {
    const ok = await confirmStore.ask({
      title: t("groups.delete.title"),
      body: t("groups.delete.body", { name: g.name }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteGroup(g.id, central);
      toastStore.success(t("groups.delete.done"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }
</script>

<svelte:head>
  <title>{t("page.title.groups")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("groups.title")}
    subtitle={loading ? t("common.loading") : t("groups.count", { count: totalGroups })}
  >
    {#snippet actions()}
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
      {#if canCreate}
        <Button size="sm" onclick={openCreate}>
          <Icon name="mdi:plus" size={16} />
          {t("groups.new")}
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if totalGroups === 0}
    <EmptyState
      message={t("groups.empty")}
      description={t("groups.empty.description")}
      icon="mdi:home-group"
    />
  {:else}
    <div class="flex flex-col gap-6">
      {#each filteredEntries as entry (entry.central)}
        {#if entry.groups.length > 0}
          <div class="flex flex-col gap-3">
            {#if centrals.length > 1}
              <h2
                class="text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]"
              >
                {entry.central}
              </h2>
            {/if}
            <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {#each entry.groups as g (g.id)}
                <Card class="flex flex-col gap-3 p-4">
                  <div class="flex items-start justify-between gap-2">
                    <h3
                      class="min-w-0 truncate text-base font-semibold text-[var(--ha-primary-text-color)]"
                    >
                      {g.name}
                    </h3>
                    {#if g.forbid_single_operation}
                      <Badge variant="muted">{t("groups.operate_only_via_group")}</Badge>
                    {/if}
                  </div>

                  <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
                    <dt class="text-[var(--ha-secondary-text-color)]">{t("groups.field.id")}</dt>
                    <dd class="tabular-nums text-[var(--ha-primary-text-color)]">{g.id}</dd>

                    <dt class="text-[var(--ha-secondary-text-color)]">{t("groups.type")}</dt>
                    <dd class="min-w-0 truncate text-[var(--ha-primary-text-color)]">
                      {g.type_label || g.type_id}
                    </dd>

                    {#if g.group_device_name}
                      <dt class="text-[var(--ha-secondary-text-color)]">
                        {t("groups.field.group_device_name")}
                      </dt>
                      <dd class="min-w-0 truncate text-[var(--ha-primary-text-color)]">
                        {g.group_device_name}
                      </dd>
                    {/if}
                  </dl>

                  <div class="flex flex-col gap-1">
                    <span class="text-xs text-[var(--ha-secondary-text-color)]">
                      {t("groups.members", { count: g.members.length })}
                    </span>
                    {#if g.members.length === 0}
                      <span class="text-xs text-[var(--ha-secondary-text-color)]">
                        {t("groups.members.empty")}
                      </span>
                    {:else}
                      <ul class="flex flex-col gap-0.5">
                        {#each g.members as m (m.address)}
                          {#if m.device_name}
                            <li
                              class="min-w-0 truncate text-xs text-[var(--ha-primary-text-color)]"
                            >
                              <span class="font-medium">{m.device_name}</span>
                              {#if m.channel_name && m.channel_name !== m.device_name}
                                <span class="text-[var(--ha-secondary-text-color)]">
                                  · {m.channel_name}</span
                                >
                              {/if}
                              {#if m.rooms && m.rooms.length > 0}
                                <span class="text-[var(--ha-secondary-text-color)]">
                                  · {m.rooms.join(", ")}</span
                                >
                              {/if}
                            </li>
                          {:else}
                            <li class="font-mono text-xs text-[var(--ha-primary-text-color)]">
                              {m.address}
                              {#if m.type_id}
                                <span class="text-[var(--ha-secondary-text-color)]">
                                  ({m.type_id})
                                </span>
                              {/if}
                            </li>
                          {/if}
                        {/each}
                      </ul>
                    {/if}
                  </div>

                  <div class="mt-auto flex justify-end gap-2 border-t border-[var(--ha-divider-color)] pt-3">
                    <Button
                      variant="outline"
                      size="sm"
                      onclick={() => openEdit(entry.central, g)}
                    >
                      <Icon name="mdi:pencil" size={14} />
                      {t("common.edit")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onclick={() => void del(entry.central, g)}
                    >
                      <Icon name="mdi:trash-can" size={14} />
                      {t("common.delete")}
                    </Button>
                  </div>
                </Card>
              {/each}
            </div>
          </div>
        {/if}
      {/each}
    </div>
  {/if}

  {#if editorOpen}
    <GroupEditor
      central={editorCentral}
      group={editorGroup}
      onClose={closeEditor}
      onSaved={onEditorSaved}
    />
  {/if}
</section>
