<script lang="ts">
  // Guided picker for one diagram series (SV03): a searchable device list →
  // channel dropdown → data-point dropdown, so an operator no longer types raw
  // central / interface / channel-address / parameter strings. Central and
  // interface_id are derived from the picked device; the label is auto-suggested
  // from the device + parameter and stays editable. Mirrors SysvarChannelPicker
  // and extends it with the parameter step.
  import { onMount, untrack } from "svelte";
  import { api } from "$lib/api/client";
  import { deviceStore } from "$lib/stores/devices.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";
  import { makeTextMatcher } from "$lib/utils";
  import type {
    ChannelSummary,
    DataPointSummary,
    DiagramSeries,
  } from "$lib/api/types";
  import Input from "$lib/components/ui/Input.svelte";
  import Select from "$lib/components/ui/Select.svelte";

  type Props = {
    series: DiagramSeries;
    index: number;
    onChange: (s: DiagramSeries) => void;
    onRemove: () => void;
  };
  let { series, index, onChange, onRemove }: Props = $props();

  let search = $state("");
  let selectedDevice = $state(
    untrack(() =>
      series.channel_address ? series.channel_address.split(":")[0] : "",
    ),
  );
  let channels = $state<ChannelSummary[]>([]);
  let dataPoints = $state<DataPointSummary[]>([]);
  let loadingChannels = $state(false);
  let loadingParams = $state(false);

  const matcher = $derived(makeTextMatcher(search));
  const candidates = $derived(
    deviceStore.items
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

  function deviceOf(addr: string) {
    return deviceStore.items.find((d) => d.address === addr);
  }
  function emit(patch: Partial<DiagramSeries>) {
    onChange({ ...series, ...patch });
  }

  async function loadChannels(addr: string) {
    loadingChannels = true;
    try {
      channels = (await api.listChannels(addr)).items ?? [];
    } catch {
      channels = [];
      toastStore.error(t("diagrams.picker.channels_failed"));
    } finally {
      loadingChannels = false;
    }
  }
  async function loadParams(addr: string, channelNo: number) {
    loadingParams = true;
    try {
      dataPoints = (await api.listDataPoints(addr, channelNo)) ?? [];
    } catch {
      dataPoints = [];
      toastStore.error(t("diagrams.picker.params_failed"));
    } finally {
      loadingParams = false;
    }
  }

  // User picks a different device → derive central/interface, reset downstream.
  async function pickDevice(addr: string) {
    selectedDevice = addr;
    const dev = deviceOf(addr);
    emit({
      central: dev?.central ?? "",
      interface_id: dev?.interface_id ?? "",
      channel_address: "",
      parameter: "",
    });
    dataPoints = [];
    await loadChannels(addr);
  }
  async function pickChannel(chAddr: string) {
    emit({ channel_address: chAddr, parameter: "" });
    dataPoints = [];
    const ch = channels.find((c) => c.address === chAddr);
    if (ch) await loadParams(selectedDevice, ch.number);
  }
  function pickParam(param: string) {
    let label = series.label;
    if (!label && param) {
      const dev = deviceOf(selectedDevice);
      const cap =
        dataPoints.find((d) => d.parameter === param)?.parameter_label || param;
      label = dev?.name ? `${dev.name} / ${cap}` : cap;
    }
    emit({ parameter: param, label });
  }

  const channelOptions = $derived([
    { value: "", label: t("diagrams.picker.channel_none") },
    ...channels.map((c) => ({
      value: c.address,
      label: `#${c.number}${c.name ? " " + c.name : c.type_label ? " " + c.type_label : ""}`,
    })),
  ]);
  const paramOptions = $derived([
    { value: "", label: t("diagrams.picker.param_none") },
    ...dataPoints.map((d) => ({
      value: d.parameter,
      label: d.parameter_label || d.parameter,
    })),
  ]);

  onMount(async () => {
    await deviceStore.refresh();
    if (selectedDevice) {
      await loadChannels(selectedDevice);
      const ch = channels.find((c) => c.address === series.channel_address);
      if (ch) await loadParams(selectedDevice, ch.number);
    }
  });
</script>

<div
  class="space-y-2 rounded-md border border-slate-200 p-3 dark:border-slate-700"
>
  <div class="flex items-center justify-between">
    <span class="text-sm font-medium text-slate-700 dark:text-slate-200">
      {t("diagrams.picker.series")}
      {index + 1}
    </span>
    <button
      type="button"
      class="text-xs text-brand-600 hover:underline dark:text-brand-400"
      onclick={onRemove}
    >
      {t("diagrams.series.remove")}
    </button>
  </div>

  <span class="block text-xs text-slate-500 dark:text-slate-400">{t("diagrams.picker.device")}</span>
  <Input
    type="search"
    placeholder={t("diagrams.picker.search")}
    bind:value={search}
  />
  <div
    class="max-h-28 overflow-y-auto rounded-md border border-slate-200 dark:border-slate-700"
  >
    {#if candidates.length === 0}
      <p class="p-2 text-center text-xs text-slate-500 dark:text-slate-400">
        {t("diagrams.picker.no_devices")}
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
    <span class="block text-xs text-slate-500 dark:text-slate-400">{t("diagrams.picker.channel")}</span>
    {#if loadingChannels}
      <p class="text-xs text-slate-500 dark:text-slate-400">
        {t("common.loading")}
      </p>
    {:else}
      <Select
        options={channelOptions}
        value={series.channel_address}
        onValueChange={(v) => void pickChannel(v)}
      />
    {/if}
  {/if}

  {#if series.channel_address}
    <span class="block text-xs text-slate-500 dark:text-slate-400">{t("diagrams.picker.value")}</span>
    {#if loadingParams}
      <p class="text-xs text-slate-500 dark:text-slate-400">
        {t("common.loading")}
      </p>
    {:else}
      <Select
        options={paramOptions}
        value={series.parameter}
        onValueChange={(v) => pickParam(v)}
      />
    {/if}
  {/if}

  {#if series.parameter}
    <span class="block text-xs text-slate-500 dark:text-slate-400">{t("diagrams.picker.label")}</span>
    <Input
      value={series.label ?? ""}
      placeholder={t("diagrams.series.label")}
      oninput={(e) => emit({ label: (e.target as HTMLInputElement).value })}
    />
  {/if}
</div>
