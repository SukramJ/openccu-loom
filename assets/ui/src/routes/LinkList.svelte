<!--
  Global direct-links overview (V01). Aggregates every direct link
  (channel-to-channel peering) across all configured centrals into one
  searchable list — the CCU WebUI's cross-device link view. This surface
  is read-only; creating, editing and deleting a link happens on the
  owning device's detail page (its Links section), which each row links
  to.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { Link } from "$lib/api/types";
  import Card from "$lib/components/ui/Card.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import { t } from "$lib/i18n";
  import { loadLS, saveLS } from "$lib/utils";

  type Props = { locale: string };
  let { locale }: Props = $props();

  let links = $state<Link[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let centralFilter = $state(loadLS("links:central"));
  let search = $state("");
  $effect(() => saveLS("links:central", centralFilter));

  async function load() {
    loading = true;
    loadError = null;
    try {
      // Fetch the full cross-central roster once; the central dropdown
      // filters the loaded list client-side so switching is instant.
      links = await api.listAllLinks(undefined, locale);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : String(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function deviceOf(channelAddress: string): string {
    const i = channelAddress.lastIndexOf(":");
    return i === -1 ? channelAddress : channelAddress.slice(0, i);
  }

  function partyName(link: Link, side: "sender" | "receiver"): string {
    if (side === "sender") {
      return (
        link.sender_device_name ||
        link.sender_channel_name ||
        link.sender_channel_type_label ||
        link.sender_address
      );
    }
    return (
      link.receiver_device_name ||
      link.receiver_channel_name ||
      link.receiver_channel_type_label ||
      link.receiver_address
    );
  }

  const centrals = $derived(
    [...new Set(links.map((l) => l.central_name).filter(Boolean) as string[])].sort(
      (a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }),
    ),
  );

  function matches(link: Link, q: string): boolean {
    if (!q) return true;
    const needle = q.toLowerCase();
    return [
      link.sender_address,
      link.receiver_address,
      link.name,
      link.description,
      link.sender_device_name,
      link.receiver_device_name,
      link.sender_channel_type_label,
      link.receiver_channel_type_label,
      link.central_name,
      link.interface_id,
    ]
      .filter(Boolean)
      .some((v) => (v as string).toLowerCase().includes(needle));
  }

  const filtered = $derived(
    links
      .filter((l) => !centralFilter || l.central_name === centralFilter)
      .filter((l) => matches(l, search))
      .sort((a, b) => {
        const ca = (a.central_name || "").localeCompare(b.central_name || "");
        if (ca !== 0) return ca;
        return partyName(a, "sender").localeCompare(
          partyName(b, "sender"),
          undefined,
          { sensitivity: "base" },
        );
      }),
  );
</script>

<svelte:head>
  <title>{t("page.title.links")}</title>
</svelte:head>

<section class="mx-auto max-w-6xl px-4 py-6 sm:px-6">
  <PageHeader
    title={t("links.title")}
    subtitle={loading ? t("common.loading") : t("links.count", { count: filtered.length })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <select
          bind:value={centralFilter}
          class="rounded-md border border-slate-300 bg-white px-2 py-2 text-sm shadow-sm focus:border-brand-500 focus:outline-none dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100"
          title={t("links.central")}
          aria-label={t("links.central")}
        >
          <option value="">{t("common.all_ccus")}</option>
          {#each centrals as c (c)}
            <option value={c}>{c}</option>
          {/each}
        </select>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if loading}
    <LoadingState />
  {:else if links.length === 0}
    <EmptyState
      message={t("links.empty")}
      description={t("links.empty.description")}
      icon="mdi:link"
    />
  {:else}
    <div class="mb-4">
      <Input
        type="search"
        bind:value={search}
        placeholder={t("links.search")}
        aria-label={t("links.search")}
      />
    </div>

    {#if filtered.length === 0}
      <EmptyState message={t("links.no_matches")} icon="mdi:link" />
    {:else}
      <ul class="flex flex-col gap-3">
        {#each filtered as link (link.central_name + "|" + link.sender_address + "->" + link.receiver_address)}
          <Card class="flex flex-col gap-2 p-4">
            <div class="flex flex-wrap items-center gap-2">
              <span class="min-w-0 truncate font-semibold text-[var(--ha-primary-text-color)]">
                {partyName(link, "sender")}
              </span>
              <Icon
                name="mdi:arrow-right"
                class="shrink-0 text-[var(--ha-secondary-text-color)]"
              />
              <span class="min-w-0 truncate font-semibold text-[var(--ha-primary-text-color)]">
                {partyName(link, "receiver")}
              </span>
              <span class="grow"></span>
              {#if centrals.length > 1 && link.central_name}
                <Badge variant="muted">{link.central_name}</Badge>
              {/if}
              {#if link.interface_id}
                <Badge variant="muted">{link.interface_id}</Badge>
              {/if}
            </div>

            <div
              class="flex flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-xs text-[var(--ha-secondary-text-color)]"
            >
              <span>{link.sender_address}</span>
              <Icon name="mdi:arrow-right" class="shrink-0" />
              <span>{link.receiver_address}</span>
            </div>

            {#if link.name}
              <p class="text-sm text-[var(--ha-primary-text-color)]">{link.name}</p>
            {/if}
            {#if link.description}
              <p class="text-xs text-[var(--ha-secondary-text-color)]">{link.description}</p>
            {/if}

            <div>
              <a
                href={`#/devices/${encodeURIComponent(deviceOf(link.sender_address))}`}
                class="inline-flex items-center gap-1 text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
              >
                <Icon name="mdi:pencil" />
                {t("links.edit_on_device")}
              </a>
            </div>
          </Card>
        {/each}
      </ul>
    {/if}
  {/if}
</section>
