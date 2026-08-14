<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "$lib/api/client";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import type { ChannelSummary } from "$lib/api/types";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";

  // Channel-assignment picker for a system variable ("Kanalzuordnung"): a
  // searchable device list drawn from the shared device store, and a channel
  // dropdown fetched for the picked device. The value is a channel address
  // ("ADDR:idx"); an empty string means the variable is unassigned. Every
  // interaction reports through onChange so the parent can dirty-track.

  type Props = {
    value: string;
    central?: string;
    onChange: (channelAddress: string) => void;
  };
  let { value, central, onChange }: Props = $props();

  let search = $state("");
  let selectedDevice = $state("");
  let channels = $state<ChannelSummary[]>([]);
  let loadingChannels = $state(false);

  // Device part of the current assignment, so an existing value pre-selects
  // its owning device and channel dropdown.
  const currentDeviceAddr = $derived(value ? value.split(":")[0] : "");

  const matcher = $derived(makeTextMatcher(search));
  const candidates = $derived(
    deviceStore.items
      .filter((d) => (central ? d.central === central : true))
      .filter((d) =>
        search
          ? matcher(d.name ?? "") ||
            matcher(d.address) ||
            matcher(d.model) ||
            matcher(d.model_label ?? "")
          : true,
      )
      .slice(0, 80),
  );

  // Monotonic generation guarding the channel fetch: a response for a
  // device the operator has already moved off must never fill the dropdown
  // that now belongs to another device, or the assignment they pick from it
  // names a channel of the wrong device.
  let loadGeneration = 0;

  async function pickDevice(addr: string) {
    selectedDevice = addr;
    channels = [];
    loadingChannels = true;
    const generation = ++loadGeneration;
    try {
      const res = await api.listChannels(addr);
      if (generation !== loadGeneration) return;
      channels = res.items ?? [];
    } catch {
      if (generation !== loadGeneration) return;
      channels = [];
      toastStore.error(t("sysvars.channel.load_failed"));
    } finally {
      if (generation === loadGeneration) loadingChannels = false;
    }
  }

  const channelOptions = $derived([
    { value: "", label: t("sysvars.channel.none") },
    ...channels.map((c) => ({
      value: c.address,
      label: `#${c.number}${c.name ? " " + c.name : c.type_label ? " " + c.type_label : ""}`,
    })),
  ]);

  onMount(() => {
    void deviceStore.refresh();
    if (currentDeviceAddr) void pickDevice(currentDeviceAddr);
  });
</script>

<div class="space-y-1.5">
  <div class="flex items-center justify-between gap-2">
    <span class="truncate font-mono text-xs text-slate-600 dark:text-slate-300">
      {value ? value : t("sysvars.channel.none")}
    </span>
    {#if value}
      <button
        type="button"
        class="shrink-0 text-xs text-brand-600 hover:underline dark:text-brand-400"
        onclick={() => onChange("")}
      >
        {t("sysvars.channel.clear")}
      </button>
    {/if}
  </div>
  <Input type="search" placeholder={t("sysvars.channel.search")} bind:value={search} />
  <div class="max-h-32 overflow-y-auto rounded-md border border-slate-200 dark:border-slate-700">
    {#if candidates.length === 0}
      <p class="p-2 text-center text-xs text-slate-500 dark:text-slate-400">
        {t("sysvars.channel.no_devices")}
      </p>
    {:else}
      {#each candidates as d (d.address)}
        <button
          type="button"
          class="block w-full truncate px-2 py-1 text-left text-sm hover:bg-slate-100 dark:hover:bg-slate-800 {selectedDevice ===
          d.address
            ? 'bg-brand-50 dark:bg-brand-950'
            : ''}"
          onclick={() => void pickDevice(d.address)}
        >
          {d.name || d.address}
          <span class="ml-1 font-mono text-xs text-slate-400">{d.address}</span>
        </button>
      {/each}
    {/if}
  </div>
  {#if selectedDevice}
    {#if loadingChannels}
      <p class="text-xs text-slate-500 dark:text-slate-400">{t("common.loading")}</p>
    {:else}
      <Select options={channelOptions} value={value} onValueChange={(v) => onChange(v)} />
    {/if}
  {/if}
</div>
