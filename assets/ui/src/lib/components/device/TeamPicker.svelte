<!--
  Channel team assignment (e.g. smoke-detector teaming). Loads the
  candidate team channels sharing the channel's team tag and lets the
  operator assign the channel to one, or reset it to its own default
  team. BidCos-RF / HmIP-RF only — the parent gates on
  detail.team_supported.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { TeamCandidate } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    address: string;
    channel: number;
  };

  let { address, channel }: Props = $props();

  let candidates = $state<TeamCandidate[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let selected = $state("");
  let busy = $state(false);

  async function load() {
    loading = true;
    loadError = null;
    try {
      candidates = await api.teamCandidates(address, channel);
      const current = candidates.find((c) => c.current);
      selected = current?.address ?? "";
    } catch (err) {
      loadError =
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  const options = $derived([
    { value: "", label: t("device.team.reset") },
    ...candidates.map((c) => ({
      value: c.address,
      label: c.name || c.address,
    })),
  ]);

  async function apply() {
    busy = true;
    try {
      await api.setChannelTeam(address, channel, selected || null);
      toastStore.success(t("device.team.changed"));
      await load();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError ? `${err.status}: ${err.message}` : String(err),
      );
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex flex-wrap items-center gap-2">
  <span class="text-xs font-medium text-slate-500 dark:text-slate-400">
    {t("device.team.title")}:
  </span>
  {#if loading}
    <LoadingState />
  {:else if loadError}
    <ErrorState message={loadError} onRetry={load} />
  {:else if candidates.length === 0}
    <span class="text-xs text-slate-400 dark:text-slate-500">
      {t("device.team.none")}
    </span>
  {:else}
    <Select
      class="w-auto"
      bind:value={selected}
      options={options}
      disabled={busy}
    />
    <Button type="button" size="sm" onclick={() => void apply()} disabled={busy}>
      {t("common.save")}
    </Button>
  {/if}
</div>
