<script lang="ts">
  import { api, type CentralProbeResult, type CentralRow } from "$lib/api/client";
  import { t } from "$lib/i18n";
  import { apiErrorMessage } from "$lib/features";
  import { createPairing } from "$lib/onboarding/pairing.svelte";
  import {
    boxLink,
    liteMoveBody,
    lostInterfaces,
    offeredInterfaces,
    type LiteMoveReason,
  } from "$lib/onboarding/lite-move";
  import { centralStore } from "$lib/stores/centrals.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import CentralStatusBadge from "$lib/components/ui/CentralStatusBadge.svelte";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import Spinner from "$lib/components/ui/Spinner.svelte";

  // Moves a stored CCU central to the openccu-lite box that now runs on the
  // same machine, for the cases the add-on could not move by itself: it
  // identifies the box, pairs with it while the box's administrator approves
  // the code in the box's own web interface, and then switches the central
  // over with the approved pairing. Opened from the pending-move warning in
  // Diagnostics and from `#/settings?tab=ccus&lite_move=<central>`.
  //
  // MCP is not extended with this move: it ends in an approval a human gives
  // on the box, which an assistant cannot give.
  type Props = {
    open: boolean;
    central: string;
    /** Why the automatic move did not happen; null when not known. */
    reason?: LiteMoveReason | null;
    onClose: () => void;
  };

  let { open, central, reason = null, onClose }: Props = $props();

  type Step = "loading" | "blocked" | "ready" | "saving" | "save_failed" | "done";

  let step = $state<Step>("loading");
  let blockedMessage = $state("");
  let saveError = $state("");
  let row = $state<CentralRow | null>(null);
  let probe = $state<CentralProbeResult | null>(null);

  const pairing = createPairing({
    start: (req) => api.startCentralPairing(req),
    status: (id, wait) => api.getCentralPairing(id, wait),
    cancel: (id) => api.cancelCentralPairing(id),
  });

  async function load() {
    step = "loading";
    blockedMessage = "";
    saveError = "";
    row = null;
    probe = null;
    pairing.reset();
    try {
      const rows = await api.listCentralsV2();
      const found = rows.find((r) => r.name === central);
      if (!found) {
        block(t("lite_move.blocked.not_found", { central }));
        return;
      }
      row = found;
      const res = await api.probeCentral({ host: found.host });
      probe = res;
      if (res.system_type !== "openccu-lite") {
        block(t("lite_move.blocked.not_lite", { host: found.host }));
      } else if (!res.ready) {
        block(t("lite_move.blocked.not_ready", { host: found.host }));
      } else if (res.lite?.pairing_available !== true) {
        block(t("lite_move.blocked.no_pairing", { host: found.host }));
      } else {
        step = "ready";
      }
    } catch (err) {
      block(apiErrorMessage(err));
    }
  }

  function block(message: string) {
    blockedMessage = message;
    step = "blocked";
  }

  // Each opening starts from the stored central and a fresh probe; closing
  // withdraws a pairing still waiting on the box.
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) void load();
    if (!open && wasOpen && pairing.phase === "pending") void pairing.cancel();
    wasOpen = open;
  });

  function startPairing() {
    if (!row) return;
    void pairing.start({ host: row.host, access: "full" });
  }

  async function save() {
    const id = pairing.approvedId;
    if (!row || !probe || id === "") return;
    step = "saving";
    saveError = "";
    try {
      await api.updateCentralV2(row.name, liteMoveBody(row, probe, id));
      step = "done";
      toastStore.success(t("lite_move.done", { central: row.name }));
      void centralStore.refresh();
    } catch (err) {
      saveError = apiErrorMessage(err);
      step = "save_failed";
      toastStore.error(t("lite_move.save_failed", { error: saveError }));
    }
  }

  // The approval is the operator's confirmation; the switch follows it.
  $effect(() => {
    if (open && step === "ready" && pairing.phase === "approved") void save();
  });

  function close() {
    if (pairing.phase === "pending") void pairing.cancel();
    onClose();
  }

  const lost = $derived(row && probe ? lostInterfaces(row, offeredInterfaces(probe)) : []);
  const link = $derived(boxLink(location));
  const entry = $derived(centralStore.byName(central));
</script>

<DialogFrame
  {open}
  title={t("lite_move.title", { central })}
  onClose={close}
  closeDisabled={step === "saving"}
  size="md"
>
  <div class="space-y-3 text-sm" data-testid="lite-move-dialog">
    {#if step === "loading"}
      <LoadingState />
    {:else if step === "blocked"}
      <ErrorState message={blockedMessage} onRetry={() => void load()} />
    {:else if step === "done"}
      <p>{t("lite_move.done", { central })}</p>
      {#if entry}
        <div class="flex items-center gap-2">
          <span class="text-[var(--ha-secondary-text-color)]">{t("lite_move.readiness")}</span>
          <CentralStatusBadge available={entry.available} readiness={entry.readiness} />
        </div>
      {/if}
    {:else}
      <p>{t("lite_move.intro", { central })}</p>
      {#if reason === "no_token"}
        <p class="text-[var(--ha-secondary-text-color)]">{t("lite_move.reason.no_token")}</p>
      {/if}
      <div class="space-y-1 rounded-md bg-[var(--ha-secondary-background-color)] p-3">
        <p class="font-medium">{t("lite_move.lost.title")}</p>
        {#if lost.length > 0}
          <p data-testid="lite-move-lost">{t("lite_move.lost.interfaces", { interfaces: lost.join(", ") })}</p>
        {/if}
        <p>{t("lite_move.lost.hub")}</p>
      </div>

      {#if step === "saving"}
        <p class="flex items-center gap-2 text-[var(--ha-secondary-text-color)]">
          <Spinner size={14} />
          {t("lite_move.saving")}
        </p>
      {:else if step === "save_failed"}
        <ErrorState message={t("lite_move.save_failed", { error: saveError })} onRetry={() => void save()} />
      {:else if pairing.phase === "idle" || pairing.phase === "cancelled"}
        {#if pairing.phase === "cancelled"}
          <p class="text-[var(--ha-secondary-text-color)]">{t("onboarding.pair.cancelled")}</p>
        {/if}
      {:else if pairing.phase === "starting" || pairing.phase === "pending"}
        <div class="space-y-2 rounded-md border border-[var(--ha-divider-color)] p-3 text-center">
          {#if pairing.code}
            <p>{t("lite_move.code_hint")}</p>
            <p class="text-3xl font-semibold tabular-nums tracking-[0.3em]" data-testid="pairing-code">
              {pairing.code}
            </p>
            <a
              href={link}
              target="_blank"
              rel="noopener noreferrer"
              class="inline-block text-brand-600 underline hover:text-brand-700 dark:text-brand-300 dark:hover:text-brand-200"
            >
              {t("lite_move.open_box")}
            </a>
          {/if}
          <p class="flex items-center justify-center gap-2 text-[var(--ha-secondary-text-color)]">
            <Spinner size={14} />
            {t("onboarding.pair.waiting")}
          </p>
        </div>
      {:else if pairing.phase !== "approved"}
        <p class="text-red-600 dark:text-red-400" role="alert">
          {pairing.phase === "rejected"
            ? t("onboarding.pair.rejected")
            : pairing.phase === "expired"
              ? t("onboarding.pair.expired")
              : t("onboarding.pair.error", { error: pairing.error })}
        </p>
      {/if}
    {/if}
  </div>

  {#snippet footer()}
    {#if step === "ready" && (pairing.phase === "idle" || pairing.phase === "cancelled")}
      <Button type="button" variant="outline" size="sm" onclick={close}>{t("common.cancel")}</Button>
      <Button type="button" size="sm" onclick={startPairing}>{t("lite_move.start")}</Button>
    {:else if step === "ready" && (pairing.phase === "starting" || pairing.phase === "pending")}
      <Button type="button" variant="outline" size="sm" onclick={() => void pairing.cancel()}>
        {t("common.cancel")}
      </Button>
    {:else if step === "ready" && pairing.phase !== "approved"}
      <Button type="button" variant="outline" size="sm" onclick={close}>{t("common.close")}</Button>
      <Button type="button" size="sm" onclick={() => pairing.reset()}>{t("onboarding.pair.retry")}</Button>
    {:else if step !== "saving"}
      <Button type="button" variant="outline" size="sm" onclick={close}>{t("common.close")}</Button>
    {/if}
  {/snippet}
</DialogFrame>
