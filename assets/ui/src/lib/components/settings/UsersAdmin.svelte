<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { UserSummaryV2 } from "$lib/api/client";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { roleBadgeVariant, roleLabel, roleOptions } from "./roles";

  let users = $state<UserSummaryV2[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  // Set when /users answers 404: the daemon runs without a live user
  // store, so the list is the read-only bootstrap one from config.yaml.
  let degraded = $state(false);

  let addingUser = $state(false);
  let createForm = $state({ username: "", password: "", role: "viewer" });
  let savingUser = $state(false);

  let editingUser = $state<UserSummaryV2 | null>(null);
  let editForm = $state({ password: "", role: "viewer" });
  let savingEdit = $state(false);

  const roles = $derived(roleOptions());

  function formatDate(iso: string | null | undefined): string {
    if (!iso) return "—";
    try {
      return new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US");
    } catch {
      return iso;
    }
  }

  function errMsg(err: unknown): string {
    return err instanceof ApiError
      ? `${err.status}: ${err.message}`
      : err instanceof Error
        ? err.message
        : String(err);
  }

  async function load() {
    loading = true;
    loadError = null;
    degraded = false;
    try {
      users = await api.listUsersV2();
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        degraded = true;
        users = [];
      } else {
        loadError = err instanceof ApiError ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function submitCreateUser() {
    savingUser = true;
    try {
      await api.createUser({
        username: createForm.username,
        password: createForm.password,
        role: createForm.role,
      });
      toastStore.success(t("users.created"));
      addingUser = false;
      createForm = { username: "", password: "", role: "viewer" };
      await load();
    } catch (err) {
      toastStore.error(errMsg(err));
    } finally {
      savingUser = false;
    }
  }

  function startEditUser(u: UserSummaryV2) {
    editingUser = u;
    editForm = { password: "", role: u.role };
  }

  async function submitEditUser() {
    if (!editingUser) return;
    savingEdit = true;
    try {
      const body: { password?: string; role?: string } = { role: editForm.role };
      if (editForm.password) body.password = editForm.password;
      await api.updateUser(editingUser.subject, body);
      toastStore.success(
        editForm.password ? t("users.password_changed") : t("users.role_changed"),
      );
      editingUser = null;
      await load();
    } catch (err) {
      toastStore.error(errMsg(err));
    } finally {
      savingEdit = false;
    }
  }

  async function deleteUser(u: UserSummaryV2) {
    const ok = await confirmStore.ask({
      title: t("users.confirm_delete_title"),
      body: t("users.confirm_delete_body", { subject: u.subject }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteUser(u.subject);
      toastStore.success(t("users.deleted"));
      await load();
    } catch (err) {
      // The daemon refuses to delete the last admin with a conflict
      // problem code; say what actually happened instead of echoing a
      // bare HTTP status.
      if (err instanceof ApiError && err.problemCode === "conflict") {
        toastStore.error(t("users.last_admin_error"));
      } else {
        toastStore.error(errMsg(err));
      }
    }
  }

  const columns: DataColumn<UserSummaryV2>[] = $derived([
    {
      key: "subject",
      label: t("users.col.subject"),
      sortable: true,
      title: true,
      get: (u) => u.subject,
    },
    { key: "role", label: t("users.col.role"), sortable: true, get: (u) => u.role },
    {
      key: "created",
      label: t("users.col.created"),
      sortable: true,
      get: (u) => u.created_at ?? "",
    },
    {
      key: "last_seen",
      label: t("users.col.last_seen"),
      sortable: true,
      get: (u) => u.last_seen_at ?? "",
    },
    { key: "actions", label: t("users.col.actions"), align: "right", cellClass: "reflow-actions" },
  ]);
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase">
      {t("settings.users")}
    </h3>
    <div class="flex items-center gap-2">
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      {#if !degraded}
        <Button type="button" size="sm" onclick={() => (addingUser = !addingUser)}>
          {addingUser ? t("common.cancel") : t("users.add")}
        </Button>
      {/if}
    </div>
  </div>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} />
  {/if}

  {#if degraded}
    <div class="rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-4 py-3 text-sm text-[var(--ha-secondary-text-color)]">
      {t("users.degraded_note")}
    </div>
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    {#if addingUser}
      <Card class="p-4">
        <h4 class="mb-3 text-base font-semibold">{t("users.add_title")}</h4>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <label class="text-sm">
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("users.col.subject")}</span>
            <Input bind:value={createForm.username} autocomplete="off" />
          </label>
          <label class="text-sm">
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("users.password")}</span>
            <Input type="password" bind:value={createForm.password} autocomplete="new-password" />
          </label>
          <label class="text-sm">
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("users.col.role")}</span>
            <Select options={roles} bind:value={createForm.role} />
          </label>
        </div>
        <div class="mt-3 flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onclick={() => (addingUser = false)}>
            {t("common.cancel")}
          </Button>
          <Button
            type="button"
            size="sm"
            onclick={() => void submitCreateUser()}
            disabled={!createForm.username || !createForm.password || savingUser}
          >
            {savingUser ? t("common.saving") : t("common.add")}
          </Button>
        </div>
      </Card>
    {/if}

    <DataTable
      rows={users}
      {columns}
      rowKey={(u) => u.subject}
      search
      searchPlaceholder={t("common.search")}
      persistKey="users-admin"
      initialSort={{ key: "subject", asc: true }}
      emptyMessage={t("users.empty")}
      emptyIcon="mdi:shield"
    >
      {#snippet cell(u, col)}
        {#if col.key === "subject"}
          <span class="font-mono text-sm font-semibold">{u.subject}</span>
        {:else if col.key === "role"}
          <Badge variant={roleBadgeVariant(u.role)}>{roleLabel(u.role)}</Badge>
        {:else if col.key === "created"}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(u.created_at)}</span>
        {:else if col.key === "last_seen"}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(u.last_seen_at)}</span>
        {:else if col.key === "actions"}
          <span class="inline-flex items-center justify-end gap-1.5">
            {#if !degraded}
              <Button type="button" size="sm" variant="outline" onclick={() => startEditUser(u)}>
                {t("common.edit")}
              </Button>
              <Button type="button" size="sm" variant="destructive" onclick={() => void deleteUser(u)}>
                {t("common.delete")}
              </Button>
            {/if}
          </span>
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</div>

<!-- Edit dialog: role and password in one place, so changing both is
     one round-trip rather than two separate flows. -->
{#if editingUser}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)] p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("users.edit_title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) editingUser = null;
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") editingUser = null;
    }}
  >
    <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900">
      <header class="mb-3 flex items-baseline justify-between gap-2">
        <h2 class="text-lg font-semibold">
          {t("users.edit_title")}
          <span class="font-mono text-sm text-[var(--ha-secondary-text-color)]">{editingUser.subject}</span>
        </h2>
        <Badge variant={roleBadgeVariant(editingUser.role)}>{roleLabel(editingUser.role)}</Badge>
      </header>
      <div class="space-y-3">
        <label class="block text-sm">
          <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("users.col.role")}</span>
          <Select options={roles} bind:value={editForm.role} />
        </label>
        <label class="block text-sm">
          <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("users.new_password")}</span>
          <Input
            type="password"
            bind:value={editForm.password}
            autocomplete="new-password"
            placeholder={t("users.password_leave_blank")}
          />
        </label>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (editingUser = null)}>
          {t("common.cancel")}
        </Button>
        <Button type="button" size="sm" onclick={() => void submitEditUser()} disabled={savingEdit}>
          {savingEdit ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}
