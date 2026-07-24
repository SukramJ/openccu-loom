<script lang="ts">
  // Create / edit a heating group (GR02). Type picker → member picker →
  // name + "operate only via group" toggle. Backed by the CCU jpages proxy
  // through the REST API; the create path is fire-and-poll on the daemon
  // side, so a save can take a moment. Mirrors the CCU WebUI group editor.
  import { onMount, untrack } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type {
    GroupEntry,
    GroupTypeEntry,
    SuitableMemberEntry,
  } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    central: string;
    group?: GroupEntry;
    onClose: () => void;
    onSaved: () => void;
  };
  let { central, group, onClose, onSaved }: Props = $props();

  const isEdit = $derived(!!group);

  let types = $state<GroupTypeEntry[]>([]);
  let typeId = $state(untrack(() => group?.type_id ?? ""));
  let name = $state(untrack(() => group?.name ?? ""));
  let forbidSingle = $state(untrack(() => group?.forbid_single_operation ?? false));
  let selected = $state<Set<string>>(
    untrack(() => new Set((group?.members ?? []).map((m) => m.address))),
  );
  let candidates = $state<SuitableMemberEntry[]>([]);

  let loading = $state(true);
  let loadingMembers = $state(false);
  let saving = $state(false);
  let error = $state<string | null>(null);

  onMount(async () => {
    try {
      if (!isEdit) {
        types = await api.groupTypes(central);
        if (!typeId && types.length > 0) typeId = types[0].id;
      }
      if (typeId) await loadMembers(typeId);
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  });

  async function loadMembers(type: string) {
    loadingMembers = true;
    try {
      const res = await api.groupSuitableMembers(type, central);
      // Union of the assignable candidates and the group's current members,
      // so an already-assigned member can be unchecked even if the type's
      // suitable list no longer surfaces it.
      const byAddr = new Map<string, SuitableMemberEntry>();
      for (const m of res.assignable) byAddr.set(m.address, m);
      for (const m of group?.members ?? []) {
        if (!byAddr.has(m.address)) {
          byAddr.set(m.address, { address: m.address, type: m.type_id });
        }
      }
      candidates = [...byAddr.values()].sort((a, b) =>
        a.address.localeCompare(b.address),
      );
    } finally {
      loadingMembers = false;
    }
  }

  async function onTypeChange(next: string) {
    typeId = next;
    selected = new Set();
    await loadMembers(next);
  }

  function toggle(addr: string) {
    const s = new Set(selected);
    if (s.has(addr)) s.delete(addr);
    else s.add(addr);
    selected = s;
  }

  const canSave = $derived(
    name.trim().length > 0 && typeId.length > 0 && !saving,
  );

  async function save() {
    if (!canSave) return;
    saving = true;
    try {
      const members = [...selected];
      if (isEdit && group) {
        await api.updateGroup(
          group.id,
          {
            name: name.trim(),
            forbid_single_operation: forbidSingle,
            members,
          },
          central,
        );
        toastStore.success(t("groups.editor.updated"));
      } else {
        await api.createGroup(
          {
            type_id: typeId,
            name: name.trim(),
            forbid_single_operation: forbidSingle,
            members,
          },
          central,
        );
        toastStore.success(t("groups.editor.created"));
      }
      onSaved();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? err.message : String(err),
      );
    } finally {
      saving = false;
    }
  }

  function typeLabel(ty: GroupTypeEntry): string {
    return ty.label_key ? `${ty.id}` : ty.id;
  }
</script>

<div
  class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
  role="dialog"
  aria-modal="true"
  aria-label={isEdit ? t("groups.editor.edit_title") : t("groups.editor.create_title")}
  onkeydown={(e) => {
    if (e.key === "Escape" && !saving) onClose();
  }}
  tabindex="-1"
>
  <div
    class="flex max-h-[90vh] w-full max-w-lg flex-col overflow-hidden rounded-lg shadow-xl"
    style="background-color: var(--ha-card-background-color); color: var(--ha-primary-text-color);"
  >
    <header class="border-b border-[var(--ha-divider-color)] px-5 py-3">
      <h2 class="text-lg font-semibold">
        {isEdit ? t("groups.editor.edit_title") : t("groups.editor.create_title")}
      </h2>
      {#if central}
        <p class="text-xs text-[var(--ha-secondary-text-color)]">{central}</p>
      {/if}
    </header>

    <div class="flex-1 space-y-4 overflow-y-auto px-5 py-4">
      {#if loading}
        <LoadingState />
      {:else if error}
        <p class="text-sm text-red-600 dark:text-red-400">
          {t("common.error")} {error}
        </p>
      {:else}
        <div>
          <label class="mb-1 block text-sm font-medium" for="group-name">
            {t("groups.editor.name")}
          </label>
          <Input id="group-name" bind:value={name} placeholder={t("groups.editor.name")} disabled={saving} />
        </div>

        {#if !isEdit}
          <div>
            <span class="mb-1 block text-sm font-medium">{t("groups.type")}</span>
            {#if types.length <= 1}
              <p class="text-sm text-[var(--ha-secondary-text-color)]">
                {types[0] ? typeLabel(types[0]) : typeId || t("groups.editor.no_types")}
              </p>
            {:else}
              <select
                class="h-10 w-full rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 text-sm text-[var(--ha-primary-text-color)]"
                value={typeId}
                disabled={saving}
                onchange={(e) => void onTypeChange((e.currentTarget as HTMLSelectElement).value)}
              >
                {#each types as ty (ty.id)}
                  <option value={ty.id}>{typeLabel(ty)}</option>
                {/each}
              </select>
            {/if}
          </div>
        {/if}

        <label class="flex items-center justify-between gap-3">
          <span class="text-sm font-medium">{t("groups.operate_only_via_group")}</span>
          <Switch bind:checked={forbidSingle} disabled={saving} />
        </label>

        <div>
          <span class="mb-1 block text-sm font-medium">
            {t("groups.editor.members")}
            <span class="font-normal text-[var(--ha-secondary-text-color)]">({selected.size})</span>
          </span>
          {#if loadingMembers}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
          {:else if candidates.length === 0}
            <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("groups.editor.no_members")}</p>
          {:else}
            <div class="max-h-56 space-y-1 overflow-y-auto rounded-md border border-[var(--ha-divider-color)] p-2">
              {#each candidates as m (m.address)}
                <label class="flex items-center gap-2 rounded px-1 py-1 text-sm hover:bg-[var(--ha-secondary-background-color)]">
                  <input
                    type="checkbox"
                    class="h-4 w-4"
                    checked={selected.has(m.address)}
                    disabled={saving}
                    onchange={() => toggle(m.address)}
                  />
                  <span class="font-mono">{m.address}</span>
                  {#if m.type}
                    <span class="text-xs text-[var(--ha-secondary-text-color)]">({m.type})</span>
                  {/if}
                </label>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <footer class="flex justify-end gap-2 border-t border-[var(--ha-divider-color)] px-5 py-3">
      <Button variant="outline" onclick={onClose} disabled={saving}>
        {t("common.cancel")}
      </Button>
      <Button onclick={() => void save()} disabled={!canSave}>
        {saving ? t("common.saving") : t("common.save")}
      </Button>
    </footer>
  </div>
</div>
