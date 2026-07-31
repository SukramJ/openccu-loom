<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { SysvarEntry, DeviceDetail, CustomDPSummary } from "$lib/api/types";
  import { favoritesStore, type FavoriteType } from "$lib/stores/favorites.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import Switch from "$lib/components/ui/Switch.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import { t } from "$lib/i18n";
  import { sysvarWidget, sysvarNumberStep } from "$lib/sysvar-widget";
  import ChannelTiles from "$lib/cdp/ChannelTiles.svelte";
  import AutoTile from "$lib/sensor-actor/AutoTile.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";

  // Start page: the user's pinned devices and system variables, served
  // from server-side per-user preferences so they follow the operator
  // across browsers. Pin/unpin happens on the device-detail page; this
  // view lists them and lets the operator change pinned sysvar values
  // inline, so favorites become a quick-control surface rather than just
  // a bookmark list.

  let sysvars = $state<SysvarEntry[]>([]);
  let drafts = $state<Record<string, unknown>>({});
  let savingName = $state<string | null>(null);
  // Device details and CDP summaries for the pinned devices and
  // channels, keyed by device address. A channel pin resolves through
  // its parent device, so both kinds share one fetch.
  let details = $state<Record<string, DeviceDetail>>({});
  let cdps = $state<Record<string, CustomDPSummary[]>>({});
  let runningProgram = $state<string | null>(null);

  onMount(() => {
    if (!favoritesStore.loaded) void favoritesStore.load().then(loadTileSources);
    else void loadTileSources();
    void loadSysvars();
  });

  // Device address behind a favorite: the id itself for a device pin,
  // the part before the colon for a channel pin.
  function deviceAddressOf(fav: { type: string; id: string }): string | null {
    if (fav.type === "device") return fav.id;
    if (fav.type === "channel") return fav.id.split(":")[0] ?? null;
    return null;
  }

  // Fetches every pinned device once. Failures are per device: one
  // unreachable device must not blank the whole favorites page, so its
  // card falls back to the plain link it was before.
  async function loadTileSources() {
    const addresses = new Set<string>();
    for (const fav of favoritesStore.items) {
      const addr = deviceAddressOf(fav);
      if (addr) addresses.add(addr);
    }
    await Promise.all(
      [...addresses].map(async (addr) => {
        try {
          const [detail, cdpList] = await Promise.all([
            api.getDevice(addr),
            api.listCustomDataPoints(addr).catch(() => [] as CustomDPSummary[]),
          ]);
          details[addr] = detail;
          cdps[addr] = cdpList;
        } catch {
          // Leave it unresolved; the card renders as a link.
        }
      }),
    );
  }

  // The pinned channel's summary, once its device detail has arrived.
  function channelFor(id: string) {
    const addr = deviceAddressOf({ type: "channel", id });
    if (!addr) return undefined;
    return details[addr]?.channels.find((c) => c.address === id);
  }

  async function runProgram(id: string, label: string) {
    runningProgram = id;
    try {
      await api.executeProgram(id);
      toastStore.success(t("favorites.program_started", { label }));
    } catch (err) {
      toastStore.error(err instanceof ApiError ? err.message : String(err));
    } finally {
      runningProgram = null;
    }
  }

  async function loadSysvars() {
    try {
      sysvars = await api.listSysvars();
    } catch {
      // Sysvars unavailable — pinned sysvars degrade to plain links
      // rather than blocking the favorites view.
      sysvars = [];
    }
  }

  // Resolve a pinned sysvar (favorite id == sysvar name) to its live
  // entry. Names are unique per CCU; in a multi-CCU setup the first match
  // wins (favorites do not carry the central scope).
  function sysvarFor(id: string): SysvarEntry | undefined {
    return sysvars.find((s) => s.name === id);
  }

  function draftKey(sv: SysvarEntry): string {
    return (sv.central ?? "") + "/" + sv.name;
  }

  function currentValue(sv: SysvarEntry): unknown {
    const key = draftKey(sv);
    return key in drafts ? drafts[key] : sv.value;
  }

  function setDraft(sv: SysvarEntry, v: unknown) {
    const key = draftKey(sv);
    drafts = { ...drafts, [key]: v };
  }

  function isDirty(sv: SysvarEntry): boolean {
    const key = draftKey(sv);
    if (!(key in drafts)) return false;
    return JSON.stringify(drafts[key]) !== JSON.stringify(sv.value);
  }

  async function save(sv: SysvarEntry) {
    const key = draftKey(sv);
    if (!(key in drafts)) return;
    savingName = sv.name;
    try {
      await api.setSysvar(sv.name, drafts[key], sv.central);
      delete drafts[key];
      drafts = { ...drafts };
      toastStore.success(t("sysvars.saved", { name: sv.name }));
      await loadSysvars();
    } catch (err) {
      toastStore.error(
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      savingName = null;
    }
  }

  function hrefFor(type: string, id: string): string {
    if (type === "device") return `#/devices/${encodeURIComponent(id)}`;
    if (type === "channel") {
      const addr = deviceAddressOf({ type, id });
      return addr ? `#/devices/${encodeURIComponent(addr)}` : "#/devices";
    }
    if (type === "program") return "#/programs";
    return "#/sysvars";
  }

  async function unpin(type: FavoriteType, id: string, label: string) {
    try {
      await favoritesStore.remove(type, id);
      toastStore.success(t("favorites.removed", { label }));
    } catch (err) {
      toastStore.error(err instanceof Error ? err.message : String(err));
    }
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4">
    <h1 class="text-2xl font-semibold">{t("favorites.title")}</h1>
    <p class="text-sm text-slate-500 dark:text-slate-400">
      {t("favorites.subtitle")}
    </p>
  </header>

  {#if !favoritesStore.loaded}
    <LoadingState />
  {:else if favoritesStore.items.length === 0}
    <EmptyState message={t("favorites.empty")} icon="mdi:star-outline" />
  {:else}
    <ul class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {#each favoritesStore.items as fav (fav.type + ":" + fav.id)}
        {@const sv = fav.type === "sysvar" ? sysvarFor(fav.id) : undefined}
        <li>
          <Card class="flex flex-col gap-2 p-3">
            <div class="flex items-center justify-between gap-2">
              <a
                href={hrefFor(fav.type, fav.id)}
                class="flex min-w-0 items-center gap-2 hover:text-brand-700"
              >
                <Icon
                  name={fav.type === "device"
                    ? "mdi:home"
                    : fav.type === "channel"
                      ? "mdi:sliders"
                      : fav.type === "program"
                        ? "mdi:play"
                        : "mdi:zap"}
                  class="shrink-0"
                />
                <span class="min-w-0">
                  <span class="block truncate text-sm font-medium">{fav.label}</span>
                  <span class="block truncate font-mono text-xs text-slate-500 dark:text-slate-400"
                    >{fav.id}</span
                  >
                </span>
              </a>
              <div class="flex shrink-0 items-center gap-2">
                <Badge variant="muted">{t(`favorites.kind.${fav.type}`)}</Badge>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onclick={() => void unpin(fav.type, fav.id, fav.label)}
                  title={t("favorites.unpin")}
                >
                  <Icon name="mdi:star" />
                </Button>
              </div>
            </div>

            {#if sv}
              {@const dirty = isDirty(sv)}
              {@const saving = savingName === sv.name}
              {@const widget = sysvarWidget(sv)}
              <div class="flex flex-wrap items-center gap-2 border-t border-slate-200 pt-2 dark:border-slate-700">
                {#if widget === "switch"}
                  {@const on = Boolean(currentValue(sv))}
                  <Switch
                    checked={on}
                    onCheckedChange={(v) => setDraft(sv, v)}
                  />
                  {#if sv.value_name_0 || sv.value_name_1}
                    <span class="text-xs text-slate-500 dark:text-slate-400">
                      {on ? (sv.value_name_1 ?? "") : (sv.value_name_0 ?? "")}
                    </span>
                  {/if}
                {:else if widget === "select"}
                  <Select
                    options={(sv.value_list ?? []).map((label, i) => ({
                      value: String(i),
                      label,
                    }))}
                    value={currentValue(sv) != null ? String(currentValue(sv)) : ""}
                    onValueChange={(v) => setDraft(sv, Number(v))}
                  />
                {:else if widget === "number"}
                  <Input
                    type="number"
                    step={sysvarNumberStep(sv.value_type)}
                    value={currentValue(sv) as number | null}
                    oninput={(e) => {
                      const n = Number((e.target as HTMLInputElement).value);
                      if (Number.isFinite(n)) setDraft(sv, n);
                    }}
                  />
                {:else}
                  <Input
                    type="text"
                    value={(currentValue(sv) ?? "") as string}
                    oninput={(e) => setDraft(sv, (e.target as HTMLInputElement).value)}
                  />
                {/if}
                {#if sv.unit}
                  <span class="text-xs text-slate-500 dark:text-slate-400">{sv.unit}</span>
                {/if}
                <Button
                  type="button"
                  size="sm"
                  class="ml-auto"
                  onclick={() => void save(sv)}
                  disabled={!dirty || saving}
                >
                  {saving ? "…" : t("common.save")}
                </Button>
              </div>
            {/if}

            {#if fav.type === "device"}
              {@const detail = details[fav.id]}
              {#if detail}
                <div class="border-t border-slate-200 pt-2 dark:border-slate-700">
                  <ChannelTiles {detail} cdps={cdps[fav.id] ?? []} />
                </div>
              {/if}
            {:else if fav.type === "channel"}
              {@const ch = channelFor(fav.id)}
              {@const devAddr = deviceAddressOf(fav)}
              {#if ch && devAddr}
                <div class="border-t border-slate-200 pt-2 dark:border-slate-700">
                  <AutoTile address={devAddr} channel={ch} />
                </div>
              {/if}
            {:else if fav.type === "program"}
              <div class="flex items-center gap-2 border-t border-slate-200 pt-2 dark:border-slate-700">
                <Button
                  type="button"
                  size="sm"
                  onclick={() => void runProgram(fav.id, fav.label)}
                  disabled={runningProgram === fav.id}
                >
                  <Icon name="mdi:play" class="mr-1" />
                  {runningProgram === fav.id ? "…" : t("programs.execute")}
                </Button>
              </div>
            {/if}
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>
