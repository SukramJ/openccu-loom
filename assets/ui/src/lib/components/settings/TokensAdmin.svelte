<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { TokenSummaryV2 } from "$lib/api/client";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";

  let tokens = $state<TokenSummaryV2[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

  // Create token modal
  let showCreate = $state(false);
  let createSubject = $state("");
  let createRole = $state("operator");
  let createSaving = $state(false);
  let createError = $state<string | null>(null);

  // Token reveal modal (shown after creation)
  let revealToken = $state<string | null>(null);
  let copied = $state(false);

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

  onMount(() => void load());

  async function createToken() {
    createSaving = true;
    createError = null;
    try {
      const result = await api.createTokenV2({
        subject: createSubject,
        role: createRole,
      });
      showCreate = false;
      createSubject = "";
      createRole = "operator";
      revealToken = result.token;
      copied = false;
    } catch (err) {
      createError = err instanceof ApiError ? err.message : String(err);
    } finally {
      createSaving = false;
    }
  }

  async function revokeToken(fingerprint: string) {
    const ok = await confirmStore.ask({
      title: t("tokens.confirm_revoke_title"),
      body: t("tokens.confirm_revoke_body", { fingerprint }),
      confirmLabel: t("tokens.revoke"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteTokenV2(fingerprint);
      toastStore.success(t("tokens.revoked"));
      await load();
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    }
  }

  async function copyToken() {
    if (!revealToken) return;
    try {
      await navigator.clipboard.writeText(revealToken);
      copied = true;
    } catch {
      // fallback: silently ignore — user can select manually
    }
  }

  function closeReveal() {
    revealToken = null;
    copied = false;
    void load();
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
      {t("settings.tokens")}
    </h3>
    <Button type="button" variant="outline" size="sm" onclick={() => (showCreate = true)}>
      {t("tokens.create")}
    </Button>
  </div>

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if loadError}
    <p class="text-sm text-red-600 dark:text-red-400">{t("common.error")} {loadError}</p>
  {:else if tokens.length === 0}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("tokens.empty")}</p>
  {:else}
    <div class="overflow-x-auto">
      <table class="table-reflow w-full text-sm">
        <thead>
          <tr class="border-b border-slate-200 text-left dark:border-slate-800">
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.subject")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.role")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.fingerprint")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.created")}</th>
            <th class="pb-2 pr-4 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.last_seen")}</th>
            <th class="pb-2 font-medium text-[var(--ha-secondary-text-color)]">{t("tokens.col.actions")}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100 dark:divide-slate-800">
          {#each tokens as tok (tok.fingerprint)}
            <tr>
              <td class="reflow-title py-2 pr-4">{tok.subject}</td>
              <td class="py-2 pr-4" data-label={t("tokens.col.role")}>
                <Badge variant={roleBadgeVariant(tok.role)}>{tok.role}</Badge>
              </td>
              <td class="py-2 pr-4 font-mono text-xs" data-label={t("tokens.col.fingerprint")}>{tok.fingerprint}</td>
              <td class="py-2 pr-4 text-[var(--ha-secondary-text-color)]" data-label={t("tokens.col.created")}>{fmtDate(tok.created_at)}</td>
              <td class="py-2 pr-4 text-[var(--ha-secondary-text-color)]" data-label={t("tokens.col.last_seen")}>{fmtDate(tok.last_seen_at)}</td>
              <td class="reflow-actions py-2">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  class="text-red-600 hover:text-red-700 dark:text-red-400"
                  onclick={() => void revokeToken(tok.fingerprint)}
                >
                  {t("tokens.revoke")}
                </Button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<!-- Create token modal -->
{#if showCreate}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    onclick={(e) => { if (e.target === e.currentTarget) showCreate = false; }}
    onkeydown={(e) => { if (e.key === "Escape") showCreate = false; }}
    tabindex="-1"
  >
    <div class="w-full max-w-sm rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-4 text-base font-semibold">{t("tokens.create_title")}</h2>
      <div class="space-y-3">
        <label class="flex flex-col gap-1 text-sm">
          <span>{t("tokens.col.subject")}</span>
          <input
            type="text"
            bind:value={createSubject}
            class="h-10 rounded border border-slate-300 px-3 text-base sm:text-sm dark:border-slate-700 dark:bg-slate-900"
          />
        </label>
        <label class="flex flex-col gap-1 text-sm">
          <span>{t("tokens.col.role")}</span>
          <select
            bind:value={createRole}
            class="h-10 rounded border border-slate-300 px-2 text-base sm:text-sm dark:border-slate-700 dark:bg-slate-900"
          >
            <option value="viewer">viewer</option>
            <option value="operator">operator</option>
            <option value="admin">admin</option>
          </select>
        </label>
        {#if createError}
          <p class="text-xs text-red-600 dark:text-red-400">{createError}</p>
        {/if}
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => (showCreate = false)}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          variant="default"
          size="sm"
          disabled={createSaving || !createSubject}
          onclick={() => void createToken()}
        >
          {createSaving ? t("common.saving") : t("tokens.create")}
        </Button>
      </div>
    </div>
  </div>
{/if}

<!-- Token reveal modal -->
{#if revealToken}
  <div
    class="modal-safe-pad fixed inset-0 z-50 flex items-center justify-center"
    style="background-color: rgb(0 0 0 / 0.45);"
    role="dialog"
    aria-modal="true"
    tabindex="-1"
  >
    <div class="w-full max-w-md rounded-lg border border-slate-200 bg-white p-5 shadow-xl dark:border-slate-700 dark:bg-slate-900">
      <h2 class="mb-2 text-base font-semibold">{t("tokens.reveal_title")}</h2>
      <p class="mb-3 text-xs text-amber-700 dark:text-amber-400">
        {t("tokens.reveal_warning")}
      </p>
      <pre
        class="mb-3 overflow-x-auto rounded bg-slate-100 p-3 font-mono text-xs dark:bg-slate-800"
        >{revealToken}</pre>
      <div class="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onclick={() => void copyToken()}>
          {copied ? t("tokens.copied") : t("common.copy")}
        </Button>
        <Button type="button" variant="default" size="sm" onclick={closeReveal}>
          {t("common.close")}
        </Button>
      </div>
    </div>
  </div>
{/if}
