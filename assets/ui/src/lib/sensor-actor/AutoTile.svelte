<!--
  AutoTile — generalised device tile that renders any channel based
  on the daemon's per-DP UI hints. Sits as the final fallback in the
  CdpTilesPanel dispatcher after CDP and CONTROL widgets. See
  docs/ui/auto-tile-concept.md.

  Layout (driven by composer.ts output):
    header        device + channel label + reachability
    [tint]        background colour by worst-case lifecycle
    headline      one large readout
    readouts      bucketed by semantic; ≥ 2 in a bucket → sub-card
    controls      toggle pills / sliders / enum pickers
    actions       fire-and-forget buttons / inline-editor buttons
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, friendlyError } from "$lib/api/client";
  import type { ChannelSummary, DataPointSummary } from "$lib/api/types";
  import { subscribe } from "$lib/stores/events.svelte";
  import { t } from "$lib/i18n";
  import { toastStore } from "$lib/stores/toast.svelte";

  import { composeTile, type ControlSpec } from "./composer";
  import { dpLabel } from "./classify";
  import { lifecycleTint } from "./state-color";

  import NumericReadout from "./readouts/NumericReadout.svelte";
  import BooleanReadout from "./readouts/BooleanReadout.svelte";
  import EnumReadout from "./readouts/EnumReadout.svelte";
  import StringReadout from "./readouts/StringReadout.svelte";

  import TogglePill from "./primitives/TogglePill.svelte";
  import ActionButton from "./primitives/ActionButton.svelte";
  import NumericActionFeature from "./primitives/NumericActionFeature.svelte";

  import ControlSlider from "$lib/control/controls/ControlSlider.svelte";
  import ControlEnumSelect from "$lib/control/controls/ControlEnumSelect.svelte";

  type Props = {
    address: string;
    channel: ChannelSummary;
  };

  let { address, channel }: Props = $props();

  let dataPoints = $state<DataPointSummary[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  const channelNo = $derived(channel.number ?? -1);
  const channelAddress = $derived(channel.address);
  const channelLabel = $derived(channel.name ?? channel.type_label ?? channel.type ?? channelAddress);

  const composed = $derived(composeTile(channel, dataPoints));
  const tint = $derived(lifecycleTint(composed.tint));
  const isCompact = $derived(composed.density === "compact");
  const isWide = $derived(composed.gridSpan === 2);

  async function load() {
    loading = true;
    error = null;
    try {
      dataPoints = await api.listDataPoints(address, channelNo);
    } catch (err) {
      error = friendlyError(err, t);
    } finally {
      loading = false;
    }
  }

  function patchDP(parameter: string, value: unknown) {
    dataPoints = dataPoints.map((dp) =>
      dp.parameter === parameter
        ? { ...dp, value, observed: true, source: "live", modified_at: new Date().toISOString() }
        : dp,
    );
  }

  onMount(() => {
    load();
    const unsub = subscribe((ev) => {
      if (ev.type !== "data_point") return;
      const e = ev.payload as {
        channel_address: string;
        parameter: string;
        value: unknown;
      };
      if (e.channel_address !== channelAddress) return;
      patchDP(e.parameter, e.value);
    });
    return () => unsub();
  });

  // Pick the right readout component for a DP. Boolean and 2-value
  // ENUMs (e.g. SHUTTER_CONTACT) render via BooleanReadout; longer
  // ENUMs via EnumReadout; numerics via NumericReadout; everything
  // else via StringReadout.
  function readoutComponentFor(dp: DataPointSummary) {
    const t = (dp.type ?? "").toUpperCase();
    if (t === "BOOL") return BooleanReadout;
    if (t === "ENUM") {
      if (dp.value_list && dp.value_list.length === 2) return BooleanReadout;
      return EnumReadout;
    }
    if (t === "INTEGER" || t === "FLOAT") return NumericReadout;
    return StringReadout;
  }

  function writeNumber(dp: DataPointSummary, next: number) {
    api.setValue(address, channelNo, dp.parameter, next).catch((err) => {
      toastStore.error(
        t("sensor_actor.action_failed", { name: dpLabel(dp) }),
        friendlyError(err, t),
      );
    });
  }

  function writeEnum(dp: DataPointSummary, next: string) {
    const idx = dp.value_list?.indexOf(next);
    const value: unknown = idx !== undefined && idx >= 0 ? idx : next;
    api.setValue(address, channelNo, dp.parameter, value).catch((err) => {
      toastStore.error(
        t("sensor_actor.action_failed", { name: dpLabel(dp) }),
        friendlyError(err, t),
      );
    });
  }
</script>

<article
  class="rounded-lg border shadow-[var(--ha-elevation-card)]"
  class:col-span-1={!isWide}
  class:md:col-span-2={isWide}
  class:xl:col-span-2={isWide}
  style:border-color="var(--ha-divider-color)"
  style:background-color={tint ?? "var(--ha-card-background-color)"}
  aria-label={channelLabel}
>
  <header class="flex items-baseline justify-between gap-2 px-3 pt-3">
    <div class="flex flex-col">
      <span class="text-sm font-semibold text-[var(--ha-primary-text-color)]">{channelLabel}</span>
      {#if channel.type_label && channel.type_label !== channelLabel}
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{channel.type_label}</span>
      {/if}
    </div>
    <span class="text-[10px] uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
      CH {channelNo}
    </span>
  </header>

  {#if error}
    <div class="m-3 rounded border border-[var(--ha-error-color)] p-2 text-xs text-[var(--ha-error-color)]">
      {t("sensor_actor.load_failed", { address: channelAddress })}
      <button type="button" class="ml-2 underline" onclick={() => load()}>↻</button>
    </div>
  {:else if loading}
    <div class="px-3 py-3 text-xs text-[var(--ha-secondary-text-color)]">
      {t("sensor_actor.loading", { address: channelAddress })}
    </div>
  {:else}
    <!-- Headline -->
    <div class="px-3 pt-2 pb-1">
      {#if composed.headline}
        {@const HeadlineCmp = readoutComponentFor(composed.headline.dp)}
        <HeadlineCmp dp={composed.headline.dp} density="comfortable" />
      {:else}
        <span class="text-sm italic text-[var(--ha-secondary-text-color)]">
          {t("sensor_actor.no_primary")}
        </span>
      {/if}
    </div>

    <!-- Bucketed readouts -->
    {#if composed.readoutBuckets.length > 0}
      <div class="px-3 pb-2" class:grid={isCompact} class:grid-cols-2={isCompact} class:gap-2={isCompact}>
        {#each composed.readoutBuckets as bucket (bucket.semantic)}
          {#if bucket.readouts.length >= 2}
            <!-- Multi-member bucket renders as a sub-card -->
            <section
              class="my-1 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-secondary-background-color)] p-2"
              class:col-span-2={isCompact}
            >
              <div class="flex flex-wrap gap-x-3 gap-y-1">
                {#each bucket.readouts as r (r.dp.parameter)}
                  {@const Cmp = readoutComponentFor(r.dp)}
                  <Cmp dp={r.dp} density="compact" showAge={!isCompact} />
                {/each}
              </div>
            </section>
          {:else}
            <!-- Single-member bucket renders inline -->
            {#each bucket.readouts as r (r.dp.parameter)}
              {@const Cmp = readoutComponentFor(r.dp)}
              <Cmp dp={r.dp} density={isCompact ? "compact" : "comfortable"} showAge={!isCompact} />
            {/each}
          {/if}
        {/each}
      </div>
    {/if}

    <!-- Controls + actions row -->
    {#if composed.controls.length + composed.actions.length > 0}
      <div
        class="flex flex-wrap items-center gap-1.5 border-t px-3 py-2"
        style:border-color="var(--ha-divider-color)"
      >
        {#each composed.controls as c (c.dp.parameter)}
          {@render controlRender(c)}
        {/each}
        {#each composed.actions as a (a.dp.parameter)}
          {#if a.kind === "action"}
            <ActionButton
              {address}
              channel={channelNo}
              parameter={a.dp.parameter}
              label={dpLabel(a.dp)}
              icon="⟳"
            />
          {:else}
            <NumericActionFeature
              {address}
              channel={channelNo}
              dp={a.dp}
              label={dpLabel(a.dp)}
              icon="⏱"
            />
          {/if}
        {/each}
      </div>
    {/if}
  {/if}
</article>

{#snippet controlRender(c: ControlSpec)}
  {#if c.kind === "toggle"}
    <TogglePill
      {address}
      channel={channelNo}
      parameter={c.dp.parameter}
      label={dpLabel(c.dp)}
      value={c.dp.value === true}
    />
  {:else if c.kind === "slider"}
    <div class="flex min-w-[160px] flex-1 items-center gap-2">
      <span class="text-xs text-[var(--ha-secondary-text-color)]">
        {dpLabel(c.dp)}
      </span>
      <ControlSlider
        value={typeof c.dp.value === "number" ? c.dp.value : (c.min ?? 0)}
        min={c.min}
        max={c.max}
        label={c.dp.parameter}
        onChange={(v) => writeNumber(c.dp, v)}
      />
    </div>
  {:else if c.kind === "button-group" && c.options}
    <ControlEnumSelect
      value={typeof c.dp.value === "number" ? c.dp.value : c.dp.value as string | undefined}
      options={c.options}
      label={dpLabel(c.dp)}
      onChange={(v) => writeEnum(c.dp, v)}
    />
  {:else if c.kind === "dropdown" && c.options}
    <ControlEnumSelect
      value={typeof c.dp.value === "number" ? c.dp.value : c.dp.value as string | undefined}
      options={c.options}
      label={dpLabel(c.dp)}
      onChange={(v) => writeEnum(c.dp, v)}
    />
  {:else}
    <!-- free-input fallback: use NumericActionFeature in stepper mode -->
    <NumericActionFeature
      {address}
      channel={channelNo}
      dp={c.dp}
      label={dpLabel(c.dp)}
      icon="✎"
    />
  {/if}
{/snippet}
