<script lang="ts">
  import { api, friendlyError } from "$lib/api/client";
  import type { ConfigRepairOutcome } from "$lib/api/types";
  import DialogFrame from "$lib/components/ui/DialogFrame.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { rawValue } from "$lib/channel/write-preview";
  import { t } from "$lib/i18n";

  // Configuration repair: rebuilds a device's stored MASTER configuration
  // from its own paramset descriptions. Opening the dialog runs the dry run,
  // which reads everything and reports per channel what a rewrite would
  // correct; only the explicit confirm step writes. The write phase answers
  // with the same per-channel shape, so both phases render through one list.
  let {
    open,
    address,
    name,
    onClose,
  }: {
    open: boolean;
    address: string;
    name: string;
    onClose: () => void;
  } = $props();

  let outcomes = $state<ConfigRepairOutcome[]>([]);
  let phase = $state<"checking" | "checked" | "repairing" | "repaired">("checking");
  let runError = $state<string | null>(null);

  // A channel is worth writing when the dry run found something to correct.
  // `foreign_parameters` is included: the rewrite of the valid parameters is
  // still attempted for it, even though the foreign entries stay.
  const actionable = $derived(
    outcomes.filter(
      (o) => o.status === "would_repair" || o.status === "foreign_parameters",
    ),
  );
  const busy = $derived(phase === "checking" || phase === "repairing");

  async function dryRun() {
    phase = "checking";
    runError = null;
    outcomes = [];
    try {
      outcomes = await api.repairDeviceConfig(address, true);
      phase = "checked";
    } catch (err) {
      runError = friendlyError(err, t);
      phase = "checked";
    }
  }

  async function repair() {
    const channels = actionable.map((o) => o.channel);
    if (channels.length === 0) return;
    phase = "repairing";
    try {
      const result = await api.repairDeviceConfig(address, false, channels);
      // Channels the dry run found clean were not sent; keep them listed so
      // the final view still accounts for every channel of the device.
      const written = new Set(result.map((o) => o.channel));
      outcomes = [...outcomes.filter((o) => !written.has(o.channel)), ...result];
      phase = "repaired";
      const failed = result.filter((o) => o.status === "failed").length;
      if (failed > 0) {
        toastStore.warn(t("device.repair_config.partial", { count: failed }));
      } else {
        toastStore.success(t("device.repair_config.done"));
      }
    } catch (err) {
      phase = "checked";
      toastStore.error(t("device.repair_config.failed"), friendlyError(err, t));
    }
  }

  $effect(() => {
    if (open) void dryRun();
  });

  function statusVariant(
    status: ConfigRepairOutcome["status"],
  ): "success" | "default" | "warning" | "danger" | "muted" {
    switch (status) {
      case "clean":
        return "muted";
      case "repaired":
        return "success";
      case "would_repair":
        return "default";
      case "foreign_parameters":
        return "warning";
      case "failed":
        return "danger";
    }
  }
</script>

<DialogFrame
  {open}
  title={t("device.repair_config.title", { name })}
  {onClose}
  closeDisabled={phase === "repairing"}
  size="xl"
>
  <p class="mb-3 text-sm text-[var(--ha-secondary-text-color)]">
    {phase === "repaired"
      ? t("device.repair_config.intro_done")
      : t("device.repair_config.intro")}
  </p>

  {#if phase === "checking"}
    <LoadingState label={t("device.repair_config.checking")} />
  {:else if runError}
    <ErrorState message={runError} onRetry={() => void dryRun()} />
  {:else if outcomes.length === 0}
    <EmptyState message={t("device.repair_config.no_channels")} icon="mdi:cog" />
  {:else}
    {#if phase === "checked" && actionable.length === 0}
      <p class="mb-3 text-sm font-medium" data-testid="repair-nothing">
        {t("device.repair_config.nothing")}
      </p>
    {/if}
    <ul class="space-y-3">
      {#each outcomes as outcome (outcome.channel)}
        <li
          class="rounded-md border border-[var(--ha-divider-color)] p-3 text-sm"
          data-testid="repair-outcome"
        >
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-mono text-xs">{outcome.channel}</span>
            <Badge variant={statusVariant(outcome.status)}>
              {t(`device.repair_config.status.${outcome.status}`)}
            </Badge>
          </div>
          {#if outcome.corrections && outcome.corrections.length > 0}
            <!-- A short fixed three-column list inside a dialog card; the
                 phone reflow of DataTable does not apply at this size, the
                 wrapper scrolls like the write preview's table does. -->
            <div class="mt-2 overflow-x-auto">
              <table class="w-full text-xs" data-testid="repair-corrections">
                <thead
                  class="border-b border-[var(--ha-divider-color)] text-left font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]"
                >
                  <tr>
                    <th class="px-2 py-1" scope="col">{t("device.repair_config.col.parameter")}</th>
                    <th class="px-2 py-1" scope="col">{t("device.repair_config.col.stored")}</th>
                    <th class="px-2 py-1" scope="col">{t("device.repair_config.col.corrected")}</th>
                    <th class="px-2 py-1" scope="col">{t("device.repair_config.col.reason")}</th>
                  </tr>
                </thead>
                <tbody>
                  {#each outcome.corrections as c (c.parameter)}
                    <tr class="border-b border-[var(--ha-divider-color)] last:border-0">
                      <td class="px-2 py-1 font-mono">{c.parameter}</td>
                      <td class="px-2 py-1 text-[var(--ha-secondary-text-color)]">{rawValue(c.stored)}</td>
                      <td class="px-2 py-1 font-medium">{rawValue(c.corrected)}</td>
                      <td class="px-2 py-1 text-[var(--ha-secondary-text-color)]">{c.reason ?? "—"}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
          {#if outcome.foreign && outcome.foreign.length > 0}
            <p class="mt-2 text-xs text-[var(--ha-warning-color)]">
              {t("device.repair_config.foreign", { names: outcome.foreign.join(", ") })}
            </p>
          {/if}
          {#if outcome.error}
            <p class="mt-2 text-xs text-[var(--ha-error-color)]">{outcome.error}</p>
          {/if}
          {#if outcome.result?.readback_error}
            <p class="mt-2 text-xs text-[var(--ha-warning-color)]">
              {t("channel.apply.readback_error", { error: outcome.result.readback_error })}
            </p>
          {/if}
          {#each outcome.result?.readback_divergences ?? [] as d (d.parameter)}
            <p class="mt-1 text-xs text-[var(--ha-warning-color)]">
              {t("channel.apply.divergence", {
                parameter: d.parameter,
                sent: rawValue(d.sent),
                stored: rawValue(d.stored),
              })}
            </p>
          {/each}
        </li>
      {/each}
    </ul>
  {/if}

  {#snippet footer()}
    <Button
      type="button"
      variant="outline"
      size="md"
      class="w-full sm:w-auto"
      disabled={phase === "repairing"}
      onclick={onClose}
    >
      {phase === "repaired" ? t("common.close") : t("common.cancel")}
    </Button>
    {#if phase !== "repaired"}
      <Button
        type="button"
        variant="destructive"
        size="md"
        class="w-full sm:w-auto"
        disabled={busy || !!runError || actionable.length === 0}
        onclick={() => void repair()}
      >
        {phase === "repairing"
          ? t("device.repair_config.running")
          : t("device.repair_config.confirm", { count: actionable.length })}
      </Button>
    {/if}
  {/snippet}
</DialogFrame>
