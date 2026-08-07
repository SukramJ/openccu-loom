<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { TokenSummaryV2 } from "$lib/api/client";
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

  let tokens = $state<TokenSummaryV2[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  let creatingToken = $state(false);
  let tokenForm = $state({ subject: "", role: "viewer" });
  let savingToken = $state(false);

  // Plaintext token, shown exactly once after creation.
  let revealToken = $state<{ token: string; fingerprint: string } | null>(null);
  let copied = $state(false);

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
    try {
      tokens = await api.listTokensV2();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

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
      // why the button did nothing. A token that is shown once and
      // silently fails to copy is a token that is lost.
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

  const columns: DataColumn<TokenSummaryV2>[] = $derived([
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

<div class="space-y-4">
  <div class="flex items-center justify-between gap-2">
    <h3 class="text-sm font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase">
      {t("settings.tokens")}
    </h3>
    <div class="flex items-center gap-2">
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      <Button type="button" size="sm" onclick={() => (creatingToken = !creatingToken)}>
        {creatingToken ? t("common.cancel") : t("tokens.create")}
      </Button>
    </div>
  </div>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} />
  {/if}

  {#if loading}
    <LoadingState />
  {:else}
    {#if creatingToken}
      <Card class="p-4">
        <h4 class="mb-3 text-base font-semibold">{t("tokens.create_title")}</h4>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label class="text-sm">
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("tokens.col.subject")}</span>
            <Input bind:value={tokenForm.subject} autocomplete="off" />
          </label>
          <label class="text-sm">
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{t("tokens.col.role")}</span>
            <Select options={roles} bind:value={tokenForm.role} />
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
            {savingToken ? t("common.saving") : t("tokens.create")}
          </Button>
        </div>
      </Card>
    {/if}

    <DataTable
      rows={tokens}
      {columns}
      rowKey={(tk) => tk.fingerprint}
      search
      searchPlaceholder={t("common.search")}
      persistKey="tokens-admin"
      initialSort={{ key: "created", asc: false }}
      emptyMessage={t("tokens.empty")}
      emptyIcon="mdi:key"
    >
      {#snippet cell(tk, col)}
        {#if col.key === "subject"}
          <span class="font-mono text-sm font-semibold">{tk.subject}</span>
        {:else if col.key === "role"}
          <Badge variant={roleBadgeVariant(tk.role)}>{roleLabel(tk.role)}</Badge>
        {:else if col.key === "fingerprint"}
          <span class="font-mono text-xs text-[var(--ha-secondary-text-color)]">{tk.fingerprint}</span>
        {:else if col.key === "created"}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(tk.created_at)}</span>
        {:else if col.key === "last_seen"}
          <span class="text-xs text-[var(--ha-secondary-text-color)]">{formatDate(tk.last_seen_at)}</span>
        {:else if col.key === "actions"}
          <span class="inline-flex items-center justify-end gap-1.5">
            <Button type="button" size="sm" variant="destructive" onclick={() => void deleteToken(tk)}>
              {t("tokens.revoke")}
            </Button>
          </span>
        {/if}
      {/snippet}
    </DataTable>
  {/if}
</div>

<!-- Copy-once token reveal dialog -->
{#if revealToken}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center bg-[color-mix(in_srgb,var(--color-slate-900)_50%,transparent)] p-4"
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
      <p class="mb-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-[color-mix(in_srgb,var(--color-amber-900)_30%,transparent)] dark:text-amber-200">
        {t("tokens.reveal_warning")}
      </p>
      <div
        class="mb-4 break-all rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] px-3 py-2 font-mono text-sm"
        data-testid="token-value"
      >
        {revealToken.token}
      </div>
      <p class="mb-4 text-xs text-[var(--ha-secondary-text-color)]">
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
