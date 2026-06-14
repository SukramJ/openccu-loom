<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { InboxDevice } from "$lib/api/types";
  import Button from "$lib/components/ui/Button.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import { installModeStore } from "$lib/stores/installMode.svelte";
  import { t } from "$lib/i18n";

  // Inbox of pending pairing candidates. The CCU populates the list
  // through its system-variable feed; this view lets the operator
  // accept devices into the running registry. Mirrors the
  // "Posteingang" panel of the CCU WebUI.

  type Props = { locale: string };
  let { locale }: Props = $props();

  let entries = $state<InboxDevice[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state("");
  let accepting = $state<string | null>(null);
  let banner = $state<string | null>(null);

  // Active-pairing tick: while the install mode is running on the CCU,
  // the inbox should reflect freshly-discovered candidates without the
  // user having to hit reload. 3 s is fast enough that the operator
  // sees the device shortly after pressing the physical pairing
  // button and slow enough to keep CCU pressure low.
  let pairingTimer: ReturnType<typeof setInterval> | null = null;

  $effect(() => {
    if (installModeStore.active) {
      if (!pairingTimer) {
        pairingTimer = setInterval(() => void load(), 3000);
      }
    } else if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
  });

  async function load() {
    loading = true;
    loadError = null;
    try {
      entries = await api.listInbox();
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  async function accept(addr: string, central: string) {
    accepting = addr;
    banner = null;
    try {
      await api.acceptInboxDevice(addr, central);
      banner = t("inbox.accepted", { name: addr });
      await load();
    } catch (err) {
      banner =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      accepting = null;
    }
  }

  onMount(() => {
    void load();
    installModeStore.ensurePoll();
  });

  onDestroy(() => {
    if (pairingTimer) {
      clearInterval(pairingTimer);
      pairingTimer = null;
    }
    installModeStore.release();
  });

  const centrals = $derived.by(() => {
    const set = new Set<string>();
    for (const d of entries) if (d.central) set.add(d.central);
    return Array.from(set).sort((a, b) =>
      a.localeCompare(b, undefined, { sensitivity: "base" }),
    );
  });

  const visibleEntries = $derived(
    centralFilter ? entries.filter((d) => d.central === centralFilter) : entries,
  );

  function formatTs(secs: number | undefined): string {
    if (!secs) return "";
    try {
      return new Date(secs * 1000).toLocaleString(
        locale === "de" ? "de-DE" : "en-US",
      );
    } catch {
      return String(secs);
    }
  }
</script>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-2xl font-semibold">{t("inbox.title")}</h1>
      <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("inbox.subtitle")}</p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      {#if banner}<span class="text-xs text-[var(--ha-secondary-text-color)]">{banner}</span>{/if}
      {#if installModeStore.banner && !installModeStore.active}
        <span class="text-xs text-[var(--ha-secondary-text-color)]">{installModeStore.banner}</span>
      {/if}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900"
          title="CCU"
        >
          <option value="">Alle CCUs</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
      <Button
        type="button"
        variant={installModeStore.active ? "default" : "outline"}
        onclick={() => void installModeStore.toggle()}
        disabled={installModeStore.busy}
        title={installModeStore.active
          ? "Anlernmodus aktiv — klicken um zu beenden"
          : "Anlernmodus starten (60 s) um neue Geräte zu koppeln"}
      >
        {#if installModeStore.active}
          Anlernen · {installModeStore.remainingSeconds ?? "…"} s
        {:else}
          Anlernmodus
        {/if}
      </Button>
      <Button type="button" variant="outline" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
    </div>
  </header>

  {#if installModeStore.active}
    <div class="mb-4 flex items-center gap-2 rounded border border-brand-300 bg-brand-50 p-3 text-sm text-brand-900 dark:border-brand-800 dark:bg-brand-950 dark:text-brand-200">
      <Badge variant="default">aktiv</Badge>
      <span>
        Anlernmodus läuft
        {#if installModeStore.remainingSeconds !== null}
          · {installModeStore.remainingSeconds}&nbsp;Sekunden verbleibend
        {/if}
      </span>
    </div>
  {/if}

  {#if loadError}
    <Card class="mb-4 p-3">
      <p class="text-sm text-red-600 dark:text-red-400">
        {t("common.error")} {loadError}
      </p>
    </Card>
  {/if}

  {#if loading}
    <p class="text-sm text-[var(--ha-secondary-text-color)]">{t("common.loading")}</p>
  {:else if visibleEntries.length === 0}
    <Card class="p-6 text-center text-sm text-[var(--ha-secondary-text-color)]">
      {t("inbox.empty")}
    </Card>
  {:else}
    <ul class="space-y-2">
      {#each visibleEntries as d (d.central + "/" + d.address)}
        <li>
          <Card class="p-4">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="flex flex-wrap items-baseline gap-2">
                  <h3 class="font-mono font-semibold">{d.address}</h3>
                  <Badge variant="muted">{d.model}</Badge>
                  {#if d.manufacturer}
                    <Badge variant="muted">{d.manufacturer}</Badge>
                  {/if}
                  {#if centrals.length > 1 && d.central}
                    <Badge variant="muted">{d.central}</Badge>
                  {/if}
                </div>
                {#if d.serial}
                  <p class="mt-1 font-mono text-[11px] text-[var(--ha-secondary-text-color)]">
                    {t("inbox.serial")} {d.serial}
                  </p>
                {/if}
                {#if d.first_seen}
                  <p class="mt-0.5 text-xs text-[var(--ha-secondary-text-color)]">
                    {t("inbox.first_seen")} {formatTs(d.first_seen)}
                  </p>
                {/if}
              </div>
              <Button
                type="button"
                size="sm"
                onclick={() => void accept(d.address, d.central)}
                disabled={accepting === d.address}
              >
                {accepting === d.address ? "…" : t("inbox.accept")}
              </Button>
            </div>
          </Card>
        </li>
      {/each}
    </ul>
  {/if}
</section>
