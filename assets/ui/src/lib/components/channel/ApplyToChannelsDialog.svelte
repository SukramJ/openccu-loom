<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import type { ParamsetApplyOutcome, ParamsetApplyTarget } from "$lib/api/types";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { rawValue } from "$lib/channel/write-preview";

  // Applies the panel's unsaved MASTER values to other channels whose MASTER
  // description is identical to this one. The flow is check-then-write: the
  // dry run reports per target what would happen (a target can be refused
  // by the description-identity gate or by validation), and only a dry run
  // over exactly the current selection unlocks the write. Nothing is
  // pre-ticked — every target is an explicit operator choice.
  let {
    open,
    channelAddress,
    values,
    editToken,
    onClose,
  }: {
    open: boolean;
    channelAddress: string;
    values: Record<string, unknown>;
    editToken?: string;
    onClose: () => void;
  } = $props();

  let targets = $state<ParamsetApplyTarget[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let selected = $state<Set<string>>(new Set());
  let busy = $state(false);
  // Outcomes of the last run, and whether that run was the dry run. The
  // dry-run outcomes are only valid for the selection they were made for.
  let outcomes = $state<ParamsetApplyOutcome[] | null>(null);
  let outcomesAreDryRun = $state(false);
  let checkedSelection = $state("");
  let applied = $state(false);

  const selectionKey = $derived([...selected].sort().join("|"));
  const valueCount = $derived(Object.keys(values).length);
  // The write goes only to targets the dry run cleared; a refused target
  // would be refused again, and sending it anyway only adds noise.
  const cleared = $derived(
    (outcomes ?? []).filter((o) => o.status === "would_apply").map((o) => o.address),
  );
  const canApply = $derived(
    !busy &&
      !applied &&
      outcomesAreDryRun &&
      checkedSelection === selectionKey &&
      selected.size > 0 &&
      cleared.length > 0,
  );

  async function loadTargets() {
    loading = true;
    loadError = null;
    try {
      targets = await api.getParamsetApplyTargets(channelAddress);
    } catch (err) {
      loadError = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  // Every open starts from a clean slate: a previous run's outcomes belong
  // to values the operator may have changed since.
  $effect(() => {
    if (!open) return;
    selected = new Set();
    outcomes = null;
    outcomesAreDryRun = false;
    checkedSelection = "";
    applied = false;
    void loadTargets();
  });

  function toggle(address: string, on: boolean) {
    const next = new Set(selected);
    if (on) next.add(address);
    else next.delete(address);
    selected = next;
    if (applied) return;
    // A changed selection invalidates the dry run.
    if (outcomesAreDryRun) outcomes = null;
  }

  function orderedSelection(): string[] {
    return targets.map((tg) => tg.address).filter((a) => selected.has(a));
  }

  async function run(dryRun: boolean) {
    const list = dryRun ? orderedSelection() : [...cleared];
    if (list.length === 0) return;
    busy = true;
    try {
      const result = await api.applyParamsetToChannels(
        channelAddress,
        values,
        list,
        dryRun,
        editToken,
      );
      outcomesAreDryRun = dryRun;
      if (dryRun) {
        outcomes = result;
        checkedSelection = selectionKey;
        return;
      }
      // Keep the dry run's refusals on screen next to the write outcomes,
      // so every ticked target still shows why it did or did not change.
      const written = new Set(result.map((o) => o.address));
      outcomes = [...(outcomes ?? []).filter((o) => !written.has(o.address)), ...result];
      applied = true;
      const ok = result.filter((o) => o.status === "applied").length;
      const notOk = result.length - ok;
      const diverged = result.some(
        (o) => (o.result?.readback_divergences?.length ?? 0) > 0 || !!o.result?.readback_error,
      );
      if (notOk > 0) {
        toastStore.warn(
          t("channel.apply.partial", { failed: notOk, total: result.length }),
        );
      } else if (diverged) {
        toastStore.warn(t("channel.apply.done", { count: ok }), t("channel.apply.diverged"));
      } else {
        toastStore.success(t("channel.apply.done", { count: ok }));
      }
    } catch (err) {
      toastStore.error(
        dryRun ? t("channel.apply.check_failed") : t("channel.apply.failed"),
        friendlyError(err, t),
      );
    } finally {
      busy = false;
    }
  }

  function statusVariant(
    status: ParamsetApplyOutcome["status"],
  ): "success" | "default" | "warning" | "danger" {
    switch (status) {
      case "applied":
        return "success";
      case "would_apply":
        return "default";
      case "refused":
        return "warning";
      case "failed":
        return "danger";
    }
  }
</script>

<DialogFrame {open} title={t("channel.apply.title")} {onClose} closeDisabled={busy}>
  <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
    {t("channel.apply.hint", { count: valueCount })}
  </p>

  {#if loading}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={() => void loadTargets()} />
  {:else if targets.length === 0}
    <EmptyState message={t("channel.apply.empty")} icon="mdi:content-copy" />
  {:else}
    <ul class="divide-y divide-[var(--ha-divider-color)] rounded-md border border-[var(--ha-divider-color)]">
      {#each targets as target (target.address)}
        {@const outcome = outcomes?.find((o) => o.address === target.address)}
        <li class="p-2 text-sm">
          <label class="flex items-start gap-2">
            <input
              type="checkbox"
              class="mt-1 h-4 w-4 rounded border-[var(--ha-divider-color)]"
              checked={selected.has(target.address)}
              disabled={busy || applied}
              aria-label={t("channel.apply.select", { name: target.name || target.address })}
              onchange={(e) => toggle(target.address, (e.currentTarget as HTMLInputElement).checked)}
            />
            <span class="min-w-0 flex-1">
              <span class="block font-medium">{target.name || target.address}</span>
              <span class="block text-xs text-[var(--ha-secondary-text-color)]">
                {[target.device_name, target.device_model].filter(Boolean).join(" · ")}
                <span class="font-mono">{target.address}</span>
              </span>
            </span>
            {#if outcome}
              <Badge variant={statusVariant(outcome.status)}>
                {t(`channel.apply.status.${outcome.status}`)}
              </Badge>
            {/if}
          </label>
          {#if outcome}
            <div class="ml-6 mt-1 space-y-1 text-xs" data-testid="apply-outcome">
              {#if outcome.reason}
                <p class="text-[var(--ha-secondary-text-color)]">{outcome.reason}</p>
              {/if}
              {#if outcome.result?.readback_error}
                <p class="text-[var(--ha-warning-color)]">
                  {t("channel.apply.readback_error", { error: outcome.result.readback_error })}
                </p>
              {/if}
              {#each outcome.result?.readback_divergences ?? [] as d (d.parameter)}
                <p class="text-[var(--ha-warning-color)]">
                  {t("channel.apply.divergence", {
                    parameter: d.parameter,
                    sent: rawValue(d.sent),
                    stored: rawValue(d.stored),
                  })}
                </p>
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}

  {#snippet footer()}
    <Button type="button" variant="outline" size="md" class="w-full sm:w-auto" disabled={busy} onclick={onClose}>
      {applied ? t("common.close") : t("common.cancel")}
    </Button>
    {#if !applied}
      <Button
        type="button"
        variant="outline"
        size="md"
        class="w-full sm:w-auto"
        disabled={busy || selected.size === 0}
        onclick={() => void run(true)}
      >
        {t("channel.apply.check")}
      </Button>
      <Button
        type="button"
        size="md"
        class="w-full sm:w-auto"
        disabled={!canApply}
        onclick={() => void run(false)}
      >
        {t("channel.apply.apply", { count: outcomesAreDryRun ? cleared.length : 0 })}
      </Button>
    {/if}
  {/snippet}
</DialogFrame>
