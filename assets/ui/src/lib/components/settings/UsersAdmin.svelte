<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { UserSummaryV2 } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let users = $state<UserSummaryV2[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // Add-user modal
  let showAdd = $state(false);
  let addUsername = $state("");
  let addPassword = $state("");
  let addRole = $state("operator");
  let addSaving = $state(false);
  let addError = $state<string | null>(null);

  // Change-password modal
  let pwSubject = $state<string | null>(null);
  let newPassword = $state("");
  let pwSaving = $state(false);
  let pwError = $state<string | null>(null);

  async function load() {
    loading = true;
    loadError = null;
    try {
      users = await api.listUsersV2();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(() => void load());

  async function addUser() {
    addSaving = true;
    addError = null;
    try {
      await api.createUser({
        username: addUsername,
        password: addPassword,
        role: addRole,
      });
      showAdd = false;
      addUsername = "";
      addPassword = "";
      addRole = "operator";
      toastStore.success(t("users.created"));
      await load();
    } catch (err) {
      addError = err instanceof ApiError ? err.message : String(err);
    } finally {
      addSaving = false;
    }
  }

  async function changePassword(subject: string) {
    pwSaving = true;
    pwError = null;
    try {
      await api.updateUser(subject, { password: newPassword });
      pwSubject = null;
      newPassword = "";
      toastStore.success(t("users.password_changed"));
    } catch (err) {
      pwError = err instanceof ApiError ? err.message : String(err);
    } finally {
      pwSaving = false;
    }
  }

  async function changeRole(subject: string, role: string) {
    try {
      await api.updateUser(subject, { role });
      toastStore.success(t("users.role_changed"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function deleteUser(subject: string) {
    const ok = await confirmStore.ask({
      title: t("users.confirm_delete_title"),
      body: t("users.confirm_delete_body", { subject }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteUser(subject);
      toastStore.success(t("users.deleted"));
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.problemCode === "conflict") {
        toastStore.error(t("users.last_admin_error"));
      } else {
        toastStore.error(err instanceof ApiError ? err.message : String(err));
      }
    }
  }

  function roleBadgeVariant(role: string) {
    if (role === "admin") return "danger" as const;
    if (role === "operator") return "warning" as const;
    return "muted" as const;
  }

  function fmtDate(s?: string | null): string {
    if (!s) return "—";
    try {
      return new Date(s).toLocaleDateString();
    } catch {
      return s;
    }
  }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold text-[var(--ha-secondary-text-color)] uppercase tracking-wide">
      {t("settings.users")}
    </h3>
    <Button type="button" variant="outline" size="sm" onclick={() => (showAdd = true)}>
      {t("common.add")}
    </Button>
  </div>

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if loadError}
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {loadError}</p>
  {:else if users.length === 0}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("users.empty")}</p>
  {:else}
    <div class="overflow-x-auto">
      <table class="table-reflow w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 text-left dark:border-slate-800">
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("users.col.subject")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("users.col.role")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("users.col.created")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("users.col.last_seen")}</th>
            <th class="pb-2 font-medium text-[var(--ha-secondary-text-color)]">{t("users.col.actions")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 dark:divide-slate-800">
          {#each users as u (u.subject)}
            <tr>
              <td class="reflow-title py-2 pr-4 font-mono">{u.subject}</td>
              <td class="py-2 pr-4" data-label={t("users.col.role")}>
                <span class="inline-flex items-center gap-1">
                  <select
                    value={u.role}
                    onchange={(e) =>
                      void changeRole(u.subject, (e.target as HTMLSelectElement).value)}
                    class="min-h-[36px] rounded border border-slate-300 bg-white px-2 py-0.5 text-xs sm:min-h-0 dark:border-slate-700 dark:bg-slate-900"
                  >
                    <option value="viewer">viewer</option>
                    <option value="operator">operator</option>
                    <option value="admin">admin</option>
                  </select>
                  <Badge variant={roleBadgeVariant(u.role)}>{u.role}</Badge>
                </span>
              </td>
              <td class="py-2 pr-4 text-[var(--ha-secondary-text-color)]" data-label={t("users.col.created")}>{fmtDate(u.created_at)}</td>
              <td class="py-2 pr-4 text-[var(--ha-secondary-text-color)]" data-label={t("users.col.last_seen")}>{fmtDate(u.last_seen_at)}</td>
              <td class="reflow-actions py-2">
                <div class="flex gap-1">
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onclick={() => {
                      pwSubject = u.subject;
                      newPassword = "";
                      pwError = null;
                    }}
                  >
                    {t("users.change_password")}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    class="text-red-600 hover:text-red-700 dark:text-red-400"
                    onclick={() => void deleteUser(u.subject)}
                  >
                    {t("common.delete")}
                  </Button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<!-- Add-user modal -->
{#if showAdd}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    onclick={(e) => { if (e.target === e.currentTarget) showAdd = false; }}
    onkeydown={(e) => { if (e.key === "Escape") showAdd = false; }}
    tabindex="-1"
  >
    <div class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-4 text-base font-semibold">{t("users.add_title")}</h2>
      <div class="space-y-3">
        <label class="flex flex-col gap-1 text-sm">
          <span>{t("users.col.subject")}</span>
          <input
            type="text"
            bind:value={addUsername}
            class="h-10 rounded border border-slate-300 px-3 text-base sm:text-sm dark:border-slate-700 dark:bg-slate-900"
            autocomplete="off"
          />
        </label>
        <label class="flex flex-col gap-1 text-sm">
          <span>{t("users.password")}</span>
          <input
            type="password"
            bind:value={addPassword}
            class="h-10 rounded border border-slate-300 px-3 text-base sm:text-sm dark:border-slate-700 dark:bg-slate-900"
            autocomplete="new-password"
          />
        </label>
        <label class="flex flex-col gap-1 text-sm">
          <span>{t("users.col.role")}</span>
          <select
            bind:value={addRole}
            class="h-10 rounded border border-slate-300 px-2 text-base sm:text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="viewer">viewer</option>
            <option value="operator">operator</option>
            <option value="admin">admin</option>
          </select>
        </label>
        {#if addError}
          <p class="text-xs text-red-600 dark:text-red-400">{addError}</p>
        {/if}
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (showAdd = false)}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={addSaving || !addUsername || !addPassword}
          onclick={() => void addUser()}
        >
          {addSaving ? t("common.saving") : t("common.add")}
        </Button>
      </div>
    </div>
  </div>
{/if}

<!-- Change-password modal -->
{#if pwSubject}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    onclick={(e) => { if (e.target === e.currentTarget) pwSubject = null; }}
    onkeydown={(e) => { if (e.key === "Escape") pwSubject = null; }}
    tabindex="-1"
  >
    <div class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-4 text-base font-semibold">
        {t("users.change_password_title", { subject: pwSubject })}
      </h2>
      <label class="flex flex-col gap-1 text-sm">
        <span>{t("users.new_password")}</span>
        <input
          type="password"
          bind:value={newPassword}
          class="h-9 rounded border border-slate-300 px-3 text-sm dark:border-slate-700 dark:bg-slate-900"
          autocomplete="new-password"
        />
      </label>
      {#if pwError}
        <p class="mt-2 text-xs text-red-600 dark:text-red-400">{pwError}</p>
      {/if}
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (pwSubject = null)}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={pwSaving || !newPassword}
          onclick={() => void changePassword(pwSubject!)}
        >
          {pwSaving ? t("common.saving") : t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}
