<script lang="ts">
  import { api, type TaxonomyCentral, type TaxonomyEnum } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { apiErrorMessage } from "$lib/features";
  import { flatten, moveTargets, type FlatNode } from "$lib/taxonomy/tree";
  import Button from "$lib/components/ui/Button.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";

  // Edits one central's nested taxonomies: create a node at the top or
  // below another, rename, move and delete it. What the system does not
  // allow is not offered — a central without `writable` is read-only, one
  // without `tree` has no nesting and no moving.
  type Props = {
    central: TaxonomyCentral;
    /** Called after every successful edit; paths below an edited node change. */
    onChanged: () => void | Promise<void>;
  };

  let { central, onChanged }: Props = $props();

  let activeEnum = $state("");
  const current = $derived<TaxonomyEnum | undefined>(
    central.enums.find((e) => e.id === activeEnum) ?? central.enums[0],
  );
  const rows = $derived(flatten(current?.nodes));

  function enumLabel(e: TaxonomyEnum): string {
    const own = e.names?.[prefs.locale] ?? e.names?.en;
    if (own) return own;
    const k = `taxonomy.enum.${e.id}`;
    const known = t(k);
    return known === k ? e.id : known;
  }
  const tabs = $derived(central.enums.map((e) => ({ key: e.id, label: enumLabel(e) })));

  // One inline form at a time: a new node (below `parent`, "" for the
  // top), or the rename of `path`.
  let adding = $state<string | null>(null);
  let renaming = $state<string | null>(null);
  let draft = $state("");
  let busy = $state(false);

  async function run(action: () => Promise<unknown>, done: string) {
    busy = true;
    try {
      await action();
      toastStore.success(t(done));
      adding = null;
      renaming = null;
      draft = "";
      await onChanged();
    } catch (err) {
      toastStore.error(apiErrorMessage(err));
    } finally {
      busy = false;
    }
  }

  function create() {
    const name = draft.trim();
    if (!name || !current || adding === null) return;
    const parent = adding;
    void run(() => api.createTaxonomyNode(central.central, current.id, name, parent), "taxonomy.created");
  }

  function rename(row: FlatNode) {
    const name = draft.trim();
    if (!name || !current || name === row.name) {
      renaming = null;
      return;
    }
    void run(() => api.updateTaxonomyNode(central.central, current.id, row.path, { name }), "taxonomy.renamed");
  }

  function move(row: FlatNode, parent: string) {
    if (!current || parent === row.parentPath) return;
    void run(
      () => api.updateTaxonomyNode(central.central, current.id, row.path, { parent_path: parent }),
      "taxonomy.moved",
    );
  }

  async function remove(row: FlatNode) {
    if (!current) return;
    const ok = await confirmStore.ask({
      title: t("taxonomy.delete_confirm", { name: row.label }),
      body: t("taxonomy.delete_confirm.body"),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    void run(() => api.deleteTaxonomyNode(central.central, current.id, row.path), "taxonomy.deleted");
  }

  function moveOptions(row: FlatNode) {
    return [
      { value: "", label: t("taxonomy.move.root") },
      ...moveTargets(current, row.path).map((n) => ({ value: n.path, label: n.label })),
    ];
  }

  function startAdd(parent: string) {
    renaming = null;
    adding = parent;
    draft = "";
  }
  function startRename(row: FlatNode) {
    adding = null;
    renaming = row.path;
    draft = row.name;
  }
</script>

{#snippet addForm()}
  <form
    class="flex flex-wrap items-center gap-2"
    onsubmit={(e) => {
      e.preventDefault();
      create();
    }}
  >
    <Input class="h-8 w-56" bind:value={draft} placeholder={t("taxonomy.new_name")} aria-label={t("taxonomy.new_name")} />
    <Button type="submit" size="sm" disabled={busy || draft.trim() === ""}>{t("common.save")}</Button>
    <Button type="button" variant="ghost" size="sm" onclick={() => (adding = null)}>{t("common.cancel")}</Button>
  </form>
{/snippet}

<div class="space-y-3" data-testid="taxonomy-tree-{central.central}">
  {#if central.enums.length > 1}
    <Tabs
      items={tabs}
      active={current?.id ?? ""}
      variant="segmented"
      ariaLabel={t("taxonomy.enums")}
      onSelect={(k) => {
        activeEnum = k;
        adding = null;
        renaming = null;
      }}
    />
  {/if}
  {#if !central.writable}
    <p class="text-xs text-[var(--ha-secondary-text-color)]">{t("taxonomy.readonly")}</p>
  {/if}

  {#if rows.length === 0}
    <EmptyState message={t("taxonomy.empty")} icon="mdi:text-box-search-outline" />
  {:else}
    <ul class="divide-y divide-[var(--ha-divider-color)]">
      {#each rows as row (row.path)}
        <li class="py-2" style="padding-left: {row.depth * 1.25}rem">
          {#if renaming === row.path}
            <form
              class="flex flex-wrap items-center gap-2"
              onsubmit={(e) => {
                e.preventDefault();
                rename(row);
              }}
            >
              <Input class="h-8 w-56" bind:value={draft} aria-label={t("taxonomy.rename")} />
              <Button type="submit" size="sm" disabled={busy}>{t("common.save")}</Button>
              <Button type="button" variant="ghost" size="sm" onclick={() => (renaming = null)}>
                {t("common.cancel")}
              </Button>
            </form>
          {:else}
            <div class="flex flex-wrap items-center justify-between gap-2">
              <span class="text-sm">{row.name}</span>
              {#if central.writable}
                <div class="flex flex-wrap items-center gap-1">
                  {#if central.tree}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      title={t("taxonomy.add_child")}
                      aria-label={t("taxonomy.add_child_named", { name: row.name })}
                      onclick={() => startAdd(row.path)}
                    >
                      <Icon name="mdi:plus" size={14} aria-label="" />
                    </Button>
                    <Select
                      class="h-8 w-44"
                      options={moveOptions(row)}
                      value={row.parentPath}
                      ariaLabel={t("taxonomy.move_named", { name: row.name })}
                      disabled={busy}
                      onValueChange={(v) => move(row, v)}
                    />
                  {/if}
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    title={t("taxonomy.rename")}
                    aria-label={t("taxonomy.rename_named", { name: row.name })}
                    onclick={() => startRename(row)}
                  >
                    <Icon name="mdi:pencil" size={14} aria-label="" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    title={t("taxonomy.delete")}
                    aria-label={t("taxonomy.delete_named", { name: row.name })}
                    onclick={() => void remove(row)}
                  >
                    <Icon name="mdi:trash-can" size={14} aria-label="" />
                  </Button>
                </div>
              {/if}
            </div>
            {#if adding === row.path}
              <div class="mt-2" style="padding-left: 1.25rem">
                {@render addForm()}
              </div>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#if central.writable}
    {#if adding === ""}
      {@render addForm()}
    {:else}
      <Button type="button" variant="outline" size="sm" onclick={() => startAdd("")}>
        <Icon name="mdi:plus" size={14} aria-label="" />
        {t("taxonomy.add_root")}
      </Button>
    {/if}
  {/if}
</div>
