<script lang="ts">
  // "Geräte-/Kanalparameter" — one page for a device's configuration, laid
  // out like the CCU WebUI's config/ic_deviceparameters.cgi: the device
  // parameters first, then every channel as its own block ("Ch. n"), and
  // one save bar for the whole device.
  //
  // A channel's MASTER paramset is read from the device, so a block loads
  // it only once it is opened, and stays mounted afterwards so its unsaved
  // edits survive closing it. Each panel keeps its own edit lock; "Apply"
  // writes the panels with edits one after the other and names any that
  // failed.
  import { untrack, type Snippet } from "svelte";
  import type { ChannelSummary, DeviceDetail } from "$lib/api/types";
  import ChannelPanel from "$lib/components/channel/ChannelPanel.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { channelHeader, isWeekProfileChannel } from "$lib/channel/channel-roles";
  import { prefs, setExpertMode } from "$lib/stores/preferences.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { t } from "$lib/i18n";

  type Props = {
    detail: DeviceDetail;
    locale: string;
    /** The device's own parameter channel, when it has one. */
    deviceChannel: ChannelSummary | null;
    /** The maintenance channel ":0". */
    channelZero: ChannelSummary | null;
    /** The channels shown as "Kanalparameter" blocks. */
    channels: ChannelSummary[];
    /** Links per channel address, for the "Direkte (n)" entry. */
    linkCounts: Map<string, number>;
    /** Channel to open and scroll to (a #/devices/<addr>/channels/<n> link). */
    focusChannel?: number;
    /** Name, rooms, functions, team and flags of one channel. */
    settings: Snippet<[ChannelSummary]>;
    /** Week-profile channels hold a program, edited on the schedule tab. */
    onOpenSchedule?: () => void;
    onOpenLinks?: () => void;
  };

  let {
    detail,
    locale,
    deviceChannel,
    channelZero,
    channels: channelsIn,
    linkCounts,
    focusChannel,
    settings,
    onOpenSchedule,
    onOpenLinks,
  }: Props = $props();

  // By channel number — the CCU may list them in any order, and a string
  // sort would put channel 10 between 1 and 2.
  const channels = $derived([...channelsIn].sort((a, b) => a.number - b.number));

  // The device-level blocks: its parameter channel and, when separate, the
  // maintenance channel.
  const deviceBlocks = $derived(
    [deviceChannel, channelZero && channelZero.address !== deviceChannel?.address ? channelZero : null].filter(
      (c): c is ChannelSummary => c !== null,
    ),
  );

  // Open state per channel address; "mounted" outlives "open" so a closed
  // block keeps its panel and its edits.
  let open = $state<Record<string, boolean>>({});
  let mounted = $state<Record<string, boolean>>({});
  let dirtyCounts = $state<Record<string, number>>({});
  const panels: Record<string, ChannelPanel | null> = $state({});

  // The device parameters and the first channel are open from the start,
  // the same two reads the page cost when they were separate tabs; the
  // other channels load when they are opened.
  $effect(() => {
    const initial = [...deviceBlocks, ...(focusChannel === undefined ? channels.slice(0, 1) : [])];
    untrack(() => {
      for (const c of initial) {
        if (open[c.address] === undefined) setOpen(c.address, true);
      }
    });
  });

  // A deep link opens its channel and scrolls it into view.
  $effect(() => {
    const no = focusChannel;
    if (no === undefined) return;
    const ch = channels.find((c) => c.number === no);
    if (!ch) return;
    // Only the route drives this effect; the open state it writes must not.
    untrack(() => setOpen(ch.address, true));
    queueMicrotask(() => document.getElementById(blockId(ch.address))?.scrollIntoView({ block: "start" }));
  });

  function setOpen(address: string, value: boolean) {
    open = { ...open, [address]: value };
    if (value) mounted = { ...mounted, [address]: true };
  }

  function blockId(address: string): string {
    return `params-${address.replace(/[^A-Za-z0-9]/g, "-")}`;
  }

  const pending = $derived(Object.values(dirtyCounts).reduce((a, b) => a + b, 0));
  let saving = $state(false);

  function labelOf(address: string): string {
    const ch = [...deviceBlocks, ...channels].find((c) => c.address === address);
    return ch ? channelTitle(ch) : address;
  }

  async function apply() {
    if (saving) return;
    saving = true;
    const failed: string[] = [];
    try {
      for (const [address, n] of Object.entries(dirtyCounts)) {
        if (n === 0) continue;
        const ok = await panels[address]?.save();
        if (!ok) failed.push(labelOf(address));
      }
      if (failed.length > 0) {
        toastStore.error(t("device.params.partial_title"), t("device.params.partial_body", { channels: failed.join(", ") }));
      } else {
        toastStore.success(t("device.params.saved"));
      }
    } finally {
      saving = false;
    }
  }

  function discard() {
    for (const p of Object.values(panels)) p?.discard();
  }

  function channelTitle(ch: ChannelSummary): string {
    if (ch.name?.trim()) return ch.name.trim();
    return channelHeader(ch, detail.model || detail.model_label || "");
  }
</script>

{#snippet block(ch: ChannelSummary, isDevice: boolean)}
  {@const isOpen = open[ch.address] ?? false}
  {@const dirtyHere = (dirtyCounts[ch.address] ?? 0) > 0}
  <section
    id={blockId(ch.address)}
    class="scroll-mt-20 rounded-lg border border-[var(--ha-divider-color)]"
    data-channel={ch.number}
  >
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3">
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-3 text-left"
        aria-expanded={isOpen}
        aria-controls={`${blockId(ch.address)}-body`}
        onclick={() => setOpen(ch.address, !isOpen)}
      >
        <span
          class="text-xs text-[var(--ha-secondary-text-color)] transition-transform {isOpen ? 'rotate-90' : ''}"
          aria-hidden="true">▶</span
        >
        <span class="w-12 shrink-0 font-mono text-xs text-[var(--ha-secondary-text-color)]">Ch. {ch.number}</span>
        <span class="min-w-0">
          <span class="block font-medium break-words">
            {isDevice && ch.number === 0 ? t("device.subtab.maintenance_config") : channelTitle(ch)}
          </span>
          {#if !isDevice && ch.type_label}
            <span class="block text-xs text-[var(--ha-secondary-text-color)]">{ch.type_label}</span>
          {/if}
        </span>
        {#if dirtyHere}
          <Badge variant="warning">{t("parameter.modified")}</Badge>
        {/if}
      </button>
      {#if !isDevice && onOpenLinks}
        {@const linkCount = linkCounts.get(ch.address)}
        <!-- No entry means "not counted", not "no links": the count is
             shown only when the listing produced one. -->
        <button
          type="button"
          class="text-xs font-medium text-[var(--ha-primary-color)] hover:underline"
          data-link-count={linkCount ?? ""}
          onclick={onOpenLinks}
        >
          {linkCount ? t("device.params.links", { count: linkCount }) : t("device.params.links_plain")}
        </button>
      {/if}
    </div>
    {#if mounted[ch.address]}
      <div
        id={`${blockId(ch.address)}-body`}
        class="border-t border-[var(--ha-divider-color)] px-4 py-4"
        class:hidden={!isOpen}
      >
        {#if !isDevice}
          {@render settings(ch)}
        {/if}
        {#if isWeekProfileChannel(ch.type)}
          <div class="flex flex-wrap items-center gap-3">
            <Icon name="mdi:calendar-clock" size={20} />
            <p class="flex-1 text-sm text-[var(--ha-secondary-text-color)]">
              {t("device.week_profile_channel.body")}
            </p>
            {#if onOpenSchedule}
              <Button variant="outline" size="sm" onclick={onOpenSchedule}>
                {t("device.subtab.schedule")}
              </Button>
            {/if}
          </div>
        {:else}
          <ChannelPanel
            bind:this={panels[ch.address]}
            address={detail.address}
            channel={ch.number}
            paramset="MASTER"
            {locale}
            pushesConfigPending={detail.master_pushes_config_pending}
            hosted
            onDirtyChange={(n) => (dirtyCounts = { ...dirtyCounts, [ch.address]: n })}
          />
        {/if}
      </div>
    {/if}
  </section>
{/snippet}

<div class="mb-4 flex flex-wrap items-center gap-3">
  <label class="flex items-center gap-2 text-sm">
    <input
      type="checkbox"
      class="h-4 w-4"
      checked={prefs.expertMode}
      onchange={(e) => setExpertMode((e.target as HTMLInputElement).checked)}
    />
    {t("channel.expert_label")}
  </label>
</div>

<div class="grid gap-6 @5xl:grid-cols-[12rem_1fr]">
  {#if channels.length > 0}
    <nav class="hidden self-start @5xl:sticky @5xl:top-20 @5xl:block" aria-label={t("device.params.jump")}>
      <ul class="space-y-0.5 text-sm">
        {#if deviceBlocks.length > 0}
          <li>
            <a
              class="block rounded px-2 py-1 text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]"
              href={`#${blockId(deviceBlocks[0].address)}`}
              onclick={(e) => {
                e.preventDefault();
                document.getElementById(blockId(deviceBlocks[0].address))?.scrollIntoView({ block: "start" });
              }}>{t("device.params.device")}</a
            >
          </li>
        {/if}
        {#each channels as ch (ch.address)}
          <li>
            <a
              class="flex items-center gap-2 rounded px-2 py-1 text-[var(--ha-secondary-text-color)] hover:bg-[var(--ha-secondary-background-color)]"
              href={`#${blockId(ch.address)}`}
              onclick={(e) => {
                e.preventDefault();
                setOpen(ch.address, true);
                document.getElementById(blockId(ch.address))?.scrollIntoView({ block: "start" });
              }}
            >
              <span class="font-mono text-xs">{ch.number}</span>
              <span class="truncate">{channelTitle(ch)}</span>
              {#if (dirtyCounts[ch.address] ?? 0) > 0}
                <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--ha-warning-color)]" aria-hidden="true"></span>
              {/if}
            </a>
          </li>
        {/each}
      </ul>
    </nav>
  {/if}

  <div class="min-w-0 space-y-6 @5xl:col-start-2">
    {#if deviceBlocks.length > 0}
      <div>
        <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
          {t("device.params.device")}
        </h2>
        <div class="space-y-2">
          {#each deviceBlocks as ch (ch.address)}
            {@render block(ch, true)}
          {/each}
        </div>
      </div>
    {/if}
    {#if channels.length > 0}
      <div>
        <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-[var(--ha-secondary-text-color)]">
          {t("device.params.channels")}
        </h2>
        <div class="space-y-2">
          {#each channels as ch (ch.address)}
            {@render block(ch, false)}
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>

{#if pending > 0 || saving}
  <div
    class="sticky bottom-0 z-20 mt-6 flex flex-wrap items-center gap-2 border-t border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] py-3"
    role="region"
    aria-label={t("channel.unsaved")}
  >
    <span class="mr-auto text-sm">
      <span class="font-semibold text-[var(--ha-warning-color)]">
        {t("links.editor.pending", { count: pending })}
      </span>
      <span class="text-[var(--ha-secondary-text-color)]"> · {t("device.params.pending_hint")}</span>
    </span>
    <Button variant="outline" size="sm" onclick={discard} disabled={saving}>
      {t("common.cancel")}
    </Button>
    <Button size="sm" onclick={() => void apply()} disabled={saving || pending === 0}>
      {saving ? t("common.saving") : t("links.editor.apply")}
    </Button>
  </div>
{/if}
