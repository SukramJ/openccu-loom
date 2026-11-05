<script lang="ts">
  import type { ClimateSchedule, ScheduleDeviceSummary } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import ClimateScheduleEditor from "./ClimateScheduleEditor.svelte";
  import SimpleScheduleEditor from "./SimpleScheduleEditor.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Tabs from "$lib/components/ui/Tabs.svelte";
  import type { TabItem } from "$lib/components/ui/tabs";
  import { t } from "$lib/i18n";

  // Top-level dispatch: renders either the climate or the simple editor
  // based on the `kind` field of the schedule. Mirrors aiohomematic's
  // ClimateWeekProfile vs. DefaultWeekProfile split — the SPA does not need
  // to know which devices fall into which bucket, the backend tells it.
  //
  // A device can carry two schedules (HmIP-WGT: heating on its climate
  // channel, switching on its SWITCH_WEEK_PROFILE channel). The schedule
  // list for the device says so; with more than one entry a segmented strip
  // picks the schedule and the editors address its channel. With one entry,
  // or when the list cannot be read, the device-level endpoint resolves the
  // schedule as before.
  type Props = {
    address: string;
  };

  let { address }: Props = $props();

  let schedules = $state<ScheduleDeviceSummary[]>([]);
  let selectedChannel = $state<number | undefined>(undefined);
  let schedule = $state<ClimateSchedule | null>(null);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let notSupported = $state(false);

  const multi = $derived(schedules.length > 1);

  const tabs = $derived<TabItem[]>(
    schedules.map((s) => ({
      key: String(s.channel.number),
      label: t(s.kind === "climate" ? "schedule.kind.climate" : "schedule.kind.week_profile", {
        channel: s.channel.number,
      }),
    })),
  );

  async function loadSchedules() {
    try {
      const res = await api.listSchedules(address);
      schedules = res.items ?? [];
    } catch {
      // The device-level endpoint still resolves one schedule.
      schedules = [];
    }
    selectedChannel = schedules.length > 1 ? schedules[0].channel.number : undefined;
  }

  async function load() {
    loading = true;
    loadError = null;
    notSupported = false;
    try {
      schedule =
        selectedChannel === undefined
          ? await api.getDeviceSchedule(address)
          : await api.getSchedule(address, selectedChannel);
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

  async function select(key: string) {
    const no = Number(key);
    if (no === selectedChannel) return;
    selectedChannel = no;
    await load();
  }

  $effect(() => {
    void address;
    void (async () => {
      await loadSchedules();
      await load();
    })();
  });
</script>

{#if multi}
  <Tabs
    class="mb-4"
    items={tabs}
    active={String(selectedChannel)}
    variant="segmented"
    ariaLabel={t("schedule.kind.select")}
    onSelect={(k) => void select(k)}
  />
{/if}

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
  {#key selectedChannel}
    {#if schedule.kind === "simple"}
      <SimpleScheduleEditor {address} {schedule} channelNo={selectedChannel} onReload={load} />
    {:else}
      <ClimateScheduleEditor {address} channelNo={selectedChannel} />
    {/if}
  {/key}
{/if}
