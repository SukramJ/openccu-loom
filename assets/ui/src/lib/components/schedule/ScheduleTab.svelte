<script lang="ts">
  import type { ClimateSchedule } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import ClimateScheduleEditor from "./ClimateScheduleEditor.svelte";
  import SimpleScheduleEditor from "./SimpleScheduleEditor.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import { t } from "$lib/i18n";

  // Top-level dispatch: probes the device-level schedule endpoint
  // once and renders either the climate or the simple editor based
  // on the `kind` field. Mirrors aiohomematic's ClimateWeekProfile
  // vs. DefaultWeekProfile split — the SPA does not need to know
  // which devices fall into which bucket, the backend tells it.
  type Props = {
    address: string;
  };

  let { address }: Props = $props();

  let schedule = $state<ClimateSchedule | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notSupported = $state(false);

  async function load() {
    loading = true;
    loadError = null;
    notSupported = false;
    try {
      schedule = await api.getDeviceSchedule(address);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        notSupported = true;
      } else {
        loadError = err instanceof Error ? err.message : String(err);
      }
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load();
  });
</script>

{#if loading}
  <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
    {t("schedule.loading")}
  </Card>
{:else if notSupported}
  <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
    {t("schedule.unsupported")}
  </Card>
{:else if loadError}
  <Card class="p-3">
    <p class="text-sm text-red-600 dark:text-red-400">
      {t("common.error")} {loadError}
    </p>
  </Card>
{:else if schedule}
  {#if schedule.kind === "simple"}
    <SimpleScheduleEditor {address} {schedule} onReload={load} />
  {:else}
    <ClimateScheduleEditor {address} />
  {/if}
{/if}
