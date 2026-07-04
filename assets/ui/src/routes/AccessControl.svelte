<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { UserSummaryV2, TokenSummaryV2 } from "$lib/api/client";
  import type { DataColumn } from "$lib/components/ui/data-table";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import DataTable from "$lib/components/ui/DataTable.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let users = $state<UserSummaryV2[]>([]);
  let tokens = $state<TokenSummaryV2[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  // true when /users returned 404 (UserAdmin store not wired)
  let degraded = $state(false);

  let addingUser = $state(false);
  let createForm = $state({ username: "", password: "", role: "viewer" });
  let savingUser = $state(false);

  let editingUser = $state<UserSummaryV2 | null>(null);
  let editForm = $state({ password: "", role: "viewer" });
  let savingEdit = $state(false);

  let creatingToken = $state(false);
  let tokenForm = $state({ subject: "", role: "viewer" });
  let savingToken = $state(false);

  // Plaintext token shown exactly once after creation.
  let revealToken = $state<{ token: string; fingerprint: string } | null>(null);
  let copied = $state(false);

  const ROLES = ["viewer", "operator", "admin"] as const;
  const roleOptions = $derived(ROLES.map((r) => ({ value: r, label: r })));

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
      const [u, tk] = await Promise.all([
        api.listUsersV2().catch((err) => {
          if (err instanceof ApiError && err.status === 404) {
            degraded = true;
            return [] as UserSummaryV2[];
          }
          throw err;
        }),
        api.listTokensV2(),
      ]);
      users = u;
      tokens = tk;
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

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
      toastStore.success(t("users.role_changed"));
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
      toastStore.error(errMsg(err));
    }
  }

  async function submitCreateToken() {
    savingToken = true;
    try {
      const result = await api.createTokenV2({
        subject: tokenForm.subject,
        role: tokenForm.role,
      });
      creatingToken = false;
      tokenForm = { subject: "", role: "viewer" };
      revealToken = result;
      copied = false;
      await load();
    } catch (err) {
      toastStore.error(errMsg(err));
    } finally {
      savingToken = false;
    }
  }

  async function copyToken() {
    if (!revealToken) return;
    try {
      // The Clipboard API only exists in a secure context (HTTPS or
      // localhost); over plain http navigator.clipboard is undefined
      // and writeText would throw. Guard so the reject is handled.
      if (!navigator.clipboard) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(revealToken.token);
      copied = true;
    } catch {
      // Insecure context or a denied permission: fall back to selecting
      // the token so the operator can copy it manually, and tell them
      // why the button did nothing.
      copied = false;
      selectTokenText();
      toastStore.error(t("tokens.copy_failed"));
    }
  }

  // Selects the revealed token's text so the operator can copy it with
  // the keyboard when the Clipboard API is unavailable.
  function selectTokenText() {
    const el = document.querySelector('[data-testid="token-value"]');
    const selection = window.getSelection();
    if (!el || !selection) return;
    const range = document.createRange();
    range.selectNodeContents(el);
    selection.removeAllRanges();
    selection.addRange(range);
  }

  async function deleteToken(tk: TokenSummaryV2) {
    const ok = await confirmStore.ask({
      title: t("tokens.confirm_revoke_title"),
      body: t("tokens.confirm_revoke_body", { fingerprint: tk.fingerprint }),
      confirmLabel: t("tokens.revoke"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteTokenV2(tk.fingerprint);
      toastStore.success(t("tokens.revoked"));
      await load();
    } catch (err) {
      toastStore.error(errMsg(err));
    }
  }

  onMount(load);

  const userColumns: DataColumn<UserSummaryV2>[] = $derived([
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

  const tokenColumns: DataColumn<TokenSummaryV2>[] = $derived([
    {
      key: "subject",
      label: t("tokens.col.subject"),
      sortable: true,
      title: true,
      get: (tk) => tk.subject,
    },
    { key: "role", label: t("tokens.col.role"), sortable: true, get: (tk) => tk.role },
    {
      key: "fingerprint",
      label: t("tokens.col.fingerprint"),
      sortable: true,
      get: (tk) => tk.fingerprint,
    },
    {
      key: "created",
      label: t("tokens.col.created"),
      sortable: true,
      get: (tk) => tk.created_at ?? "",
    },
    {
      key: "last_seen",
      label: t("tokens.col.last_seen"),
      sortable: true,
      get: (tk) => tk.last_seen_at ?? "",
    },
    {
      key: "actions",
      label: t("tokens.col.actions"),
      align: "right",
      cellClass: "reflow-actions",
    },
  ]);
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader title={t("access.title")} subtitle={t("access.subtitle")}>
    {#snippet actions()}
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if degraded}
    <div class="mb-4 rounded-lg border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-4 py-3 text-sm text-[var(--ha-secondary-text-color)]">
      {t("access.degraded_note")}
    </div>
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    <!-- Users section -->
    <div class="mb-6">
      <div class="mb-3 flex items-center justify-between gap-2">
        <h2 class="text-lg font-semibold">{t("access.users_title")}</h2>
        {#if !degraded}
          <Button type="button" size="sm" onclick={() => (addingUser = !addingUser)}>
            {addingUser ? t("common.cancel") : t("access.add_user")}
          </Button>
        {/if}
      </div>

      {#if addingUser}
        <Card class="mb-4 p-4">
          <h3 class="mb-3 text-base font-semibold">{t("users.add_title")}</h3>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("users.col.subject")}</span>
              <Input bind:value={createForm.username} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("users.password")}</span>
              <Input type="password" bind:value={createForm.password} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("users.col.role")}</span>
              <Select options={roleOptions} bind:value={createForm.role} />
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
              {savingUser ? "…" : t("common.add")}
            </Button>
          </div>
        </Card>
      {/if}

      <Card class="p-4">
        <DataTable
          rows={users}
          columns={userColumns}
          rowKey={(u) => u.subject}
          search
          searchPlaceholder={t("common.search")}
          persistKey="access-users"
          initialSort={{ key: "subject", asc: true }}
          emptyMessage={t("users.empty")}
          emptyIcon="mdi:shield"
        >
          {#snippet cell(u, col)}
            {#if col.key === "subject"}
              <span class="font-mono text-sm font-semibold">{u.subject}</span>
            {:else if col.key === "role"}
              <Badge variant="muted">{u.role}</Badge>
            {:else if col.key === "created"}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(u.created_at)}</span>
            {:else if col.key === "last_seen"}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(u.last_seen_at)}</span>
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
      </Card>
    </div>

    <!-- Tokens section -->
    <div>
      <div class="mb-3 flex items-center justify-between gap-2">
        <h2 class="text-lg font-semibold">{t("access.tokens_title")}</h2>
        <Button type="button" size="sm" onclick={() => (creatingToken = !creatingToken)}>
          {creatingToken ? t("common.cancel") : t("tokens.create")}
        </Button>
      </div>

      {#if creatingToken}
        <Card class="mb-4 p-4">
          <h3 class="mb-3 text-base font-semibold">{t("tokens.create_title")}</h3>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("tokens.col.subject")}</span>
              <Input bind:value={tokenForm.subject} />
            </label>
            <label class="text-sm">
              <span class="block text-xs text-slate-500 dark:text-slate-400">{t("tokens.col.role")}</span>
              <Select options={roleOptions} bind:value={tokenForm.role} />
            </label>
          </div>
          <div class="mt-3 flex justify-end gap-2">
            <Button type="button" variant="outline" size="sm" onclick={() => (creatingToken = false)}>
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              onclick={() => void submitCreateToken()}
              disabled={!tokenForm.subject || savingToken}
            >
              {savingToken ? "…" : t("tokens.create")}
            </Button>
          </div>
        </Card>
      {/if}

      <Card class="p-4">
        <DataTable
          rows={tokens}
          columns={tokenColumns}
          rowKey={(tk) => tk.fingerprint}
          search
          searchPlaceholder={t("common.search")}
          persistKey="access-tokens"
          initialSort={{ key: "created", asc: false }}
          emptyMessage={t("tokens.empty")}
          emptyIcon="mdi:key"
        >
          {#snippet cell(tk, col)}
            {#if col.key === "subject"}
              <span class="font-mono text-sm font-semibold">{tk.subject}</span>
            {:else if col.key === "role"}
              <Badge variant="muted">{tk.role}</Badge>
            {:else if col.key === "fingerprint"}
              <span class="font-mono text-xs text-slate-500 dark:text-slate-400">{tk.fingerprint}</span>
            {:else if col.key === "created"}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(tk.created_at)}</span>
            {:else if col.key === "last_seen"}
              <span class="text-xs text-slate-500 dark:text-slate-400">{formatDate(tk.last_seen_at)}</span>
            {:else if col.key === "actions"}
              <span class="inline-flex items-center justify-end gap-1.5">
                <Button type="button" size="sm" variant="destructive" onclick={() => void deleteToken(tk)}>
                  {t("tokens.revoke")}
                </Button>
              </span>
            {/if}
          {/snippet}
        </DataTable>
      </Card>
    </div>
  {/if}
</section>

<!-- Edit user dialog -->
{#if editingUser}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("access.edit_user_title")}
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
          {t("access.edit_user_title")}
          <span class="font-mono text-sm text-slate-500 dark:text-slate-400">{editingUser.subject}</span>
        </h2>
        <Badge variant="muted">{editingUser.role}</Badge>
      </header>
      <div class="space-y-3">
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("users.col.role")}</span>
          <Select options={roleOptions} bind:value={editForm.role} />
        </label>
        <label class="block text-sm">
          <span class="block text-xs text-slate-500 dark:text-slate-400">{t("users.new_password")}</span>
          <Input type="password" bind:value={editForm.password} placeholder={t("access.password_leave_blank")} />
        </label>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (editingUser = null)}>
          {t("common.cancel")}
        </Button>
        <Button type="button" size="sm" onclick={() => void submitEditUser()} disabled={savingEdit}>
          {savingEdit ? "…" : t("common.save")}
        </Button>
      </div>
    </div>
  </div>
{/if}

<!-- Copy-once token reveal dialog -->
{#if revealToken}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 p-4"
    role="dialog"
    aria-modal="true"
    aria-label={t("tokens.reveal_title")}
    tabindex="-1"
    onclick={(e) => {
      if (e.target === e.currentTarget) {
        revealToken = null;
        copied = false;
      }
    }}
    onkeydown={(e) => {
      if (e.key === "Escape") {
        revealToken = null;
        copied = false;
      }
    }}
  >
    <div class="w-full max-w-lg rounded-lg bg-white p-5 shadow-xl dark:bg-slate-900">
      <h2 class="mb-2 text-lg font-semibold">{t("tokens.reveal_title")}</h2>
      <p class="mb-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
        {t("tokens.reveal_warning")}
      </p>
      <div
        class="mb-4 break-all rounded-md border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-sm dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100"
        data-testid="token-value"
      >
        {revealToken.token}
      </div>
      <p class="mb-4 text-xs text-slate-500 dark:text-slate-400">
        {t("tokens.col.fingerprint")}: <span class="font-mono">{revealToken.fingerprint}</span>
      </p>
      <div class="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => void copyToken()}>
          {copied ? t("tokens.copied") : t("common.copy")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => {
            revealToken = null;
            copied = false;
          }}
        >
          {t("common.close")}
        </Button>
      </div>
    </div>
  </div>
{/if}
