<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import { pairingRequestsStore } from "$lib/stores/pairingRequests.svelte";
  import type { PairingView } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { prefs } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { roleBadgeVariant, roleLabel } from "./roles";

  const requests = $derived(pairingRequestsStore.requests);
  const loading = $derived(pairingRequestsStore.loading);
  const loadError = $derived(pairingRequestsStore.error);

  // Per-request typed code (the admin's half of the pairing protocol —
  // confirmation-by-typing, never a display-and-confirm dialog) and
  // per-request busy state, keyed by request id.
  let codeInputs = $state<Record<string, string>>({});
  let busyId = $state<string | null>(null);

  let streamed = false;
  onMount(() => {
    streamed = true;
    void pairingRequestsStore.refresh();
    pairingRequestsStore.ensureStream();
  });

  onDestroy(() => {
    if (streamed) pairingRequestsStore.close();
  });

  function errMsg(err: unknown): string {
    return err instanceof ApiError
      ? `${err.status}: ${err.message}`
      : err instanceof Error
        ? err.message
        : String(err);
  }

  function codeFor(id: string): string {
    return codeInputs[id] ?? "";
  }

  function setCode(id: string, value: string): void {
    // Digits only, capped at six — the code the client displays is always
    // a six-digit string.
    codeInputs[id] = value.replace(/\D/g, "").slice(0, 6);
  }

  function canApprove(id: string): boolean {
    return codeFor(id).length === 6;
  }

  function formatExpires(iso: string): string {
    const expires = new Date(iso).getTime();
    if (Number.isNaN(expires)) return iso;
    const remainingMs = expires - Date.now();
    if (remainingMs <= 0) return t("pairing.expired");
    const remainingMinutes = Math.ceil(remainingMs / 60000);
    if (remainingMinutes < 60) {
      return t("pairing.expires_in_minutes", { minutes: remainingMinutes });
    }
    return t("pairing.expires_at", {
      time: new Date(iso).toLocaleString(prefs.locale === "de" ? "de-DE" : "en-US"),
    });
  }

  function appLine(req: PairingView): string {
    const parts = [req.app];
    if (req.app_version) parts.push(req.app_version);
    if (req.instance) parts.push(req.instance);
    return parts.join(" · ");
  }

  async function approve(req: PairingView) {
    const code = codeFor(req.id);
    if (code.length !== 6) return;
    busyId = req.id;
    try {
      await api.approvePairing(req.id, code);
      toastStore.success(t("pairing.approved"));
      delete codeInputs[req.id];
      await pairingRequestsStore.refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toastStore.error(t("pairing.wrong_code_rejected"));
        await pairingRequestsStore.refresh();
      } else if (err instanceof ApiError && err.status === 404) {
        toastStore.error(t("pairing.gone"));
        await pairingRequestsStore.refresh();
      } else {
        toastStore.error(errMsg(err));
      }
    } finally {
      busyId = null;
    }
  }

  async function reject(req: PairingView) {
    const ok = await confirmStore.ask({
      title: t("pairing.confirm_reject_title"),
      body: t("pairing.confirm_reject_body", { name: req.name }),
      confirmLabel: t("pairing.reject"),
      destructive: false,
    });
    if (!ok) return;
    busyId = req.id;
    try {
      await api.rejectPairing(req.id);
      toastStore.success(t("pairing.rejected"));
      await pairingRequestsStore.refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        toastStore.error(t("pairing.gone"));
        await pairingRequestsStore.refresh();
      } else {
        toastStore.error(errMsg(err));
      }
    } finally {
      busyId = null;
    }
  }
</script>

{#if loadError}
  <Card class="p-4">
    <ErrorState message={loadError} onRetry={() => void pairingRequestsStore.refresh()} />
  </Card>
{:else if !loading && requests.length > 0}
  <Card class="p-4">
    <h3 class="mb-3 text-sm font-semibold tracking-wide text-[var(--ha-secondary-text-color)] uppercase">
      {t("pairing.title")}
    </h3>
    <ul class="space-y-3">
      {#each requests as req (req.id)}
        <li
          class="rounded-md border border-[var(--ha-divider-color)] p-3"
          data-testid={`pairing-request-${req.id}`}
        >
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-semibold">{req.name}</span>
                <Badge variant={roleBadgeVariant(req.role)}>{roleLabel(req.role)}</Badge>
              </div>
              <div class="text-xs text-[var(--ha-secondary-text-color)]">{appLine(req)}</div>
              <div class="text-xs text-[var(--ha-secondary-text-color)]">{req.address}</div>
              {#if req.purpose}
                <div class="text-xs text-[var(--ha-secondary-text-color)]">{req.purpose}</div>
              {/if}
            </div>
            <div class="text-right text-xs text-[var(--ha-secondary-text-color)]">
              {formatExpires(req.expires)}
            </div>
          </div>

          {#if req.look_alike}
            <p class="mt-2 rounded-md bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-[color-mix(in_srgb,var(--color-amber-900)_30%,transparent)] dark:text-amber-200">
              {t("pairing.look_alike_warning")}
            </p>
          {/if}

          <div class="mt-3 flex flex-wrap items-center justify-end gap-2">
            <label class="text-sm">
              <span class="sr-only">{t("pairing.code_label")}</span>
              <Input
                value={codeFor(req.id)}
                oninput={(e) => setCode(req.id, (e.currentTarget as HTMLInputElement).value)}
                inputmode="numeric"
                autocomplete="off"
                maxlength={6}
                placeholder={t("pairing.code_placeholder")}
                class="w-28 text-center font-mono tracking-widest"
                aria-label={t("pairing.code_label")}
              />
            </label>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onclick={() => void reject(req)}
              disabled={busyId === req.id}
            >
              {t("pairing.reject")}
            </Button>
            <Button
              type="button"
              size="sm"
              onclick={() => void approve(req)}
              disabled={!canApprove(req.id) || busyId === req.id}
            >
              {t("pairing.approve")}
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  </Card>
{/if}
