<script lang="ts">
  import type { Link } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Badge from "$lib/components/ui/Badge.svelte";
  import AddLinkForm from "./AddLinkForm.svelte";
  import LinkConfigPanel from "./LinkConfigPanel.svelte";
  import { toastStore } from "$lib/stores/toast.svelte";
  import { confirmStore } from "$lib/stores/confirm.svelte";
  import { t } from "$lib/i18n";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";

  type Props = {
    deviceAddress: string;
    interfaceId: string;
    locale: string;
  };

  let { deviceAddress, interfaceId, locale }: Props = $props();

  let links = $state<Link[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let adding = $state(false);
  // The link currently being edited (or null for list view).
  let editing = $state<Link | null>(null);

  // Sort + filter state. The list can grow large for devices with
  // many channels; sorting by direction or peer-name speeds scanning,
  // and a free-text search filters across address, name, channel
  // type label and description.
  type SortKey = "direction" | "name" | "sender" | "receiver";
  let sortKey = $state<SortKey>("direction");
  let sortAsc = $state(true);
  let search = $state("");

  function partyKey(link: Link, side: "sender" | "receiver"): string {
    if (side === "sender") {
      return (
        link.sender_device_name ||
        link.sender_channel_name ||
        link.sender_channel_type_label ||
        link.sender_address
      ).toLowerCase();
    }
    return (
      link.receiver_device_name ||
      link.receiver_channel_name ||
      link.receiver_channel_type_label ||
      link.receiver_address
    ).toLowerCase();
  }

  function matches(link: Link, q: string): boolean {
    if (!q) return true;
    const haystack = [
      link.sender_address,
      link.receiver_address,
      link.name,
      link.description,
      link.sender_device_name,
      link.receiver_device_name,
      link.sender_channel_name,
      link.receiver_channel_name,
      link.sender_channel_type_label,
      link.receiver_channel_type_label,
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();
    return haystack.includes(q);
  }

  const visibleLinks = $derived.by(() => {
    const q = search.trim().toLowerCase();
    const filtered = links.filter((l) => matches(l, q));
    const sorted = filtered.slice().sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "direction":
          cmp = a.direction.localeCompare(b.direction);
          if (cmp === 0) cmp = partyKey(a, "sender").localeCompare(partyKey(b, "sender"));
          break;
        case "name":
          cmp = (a.name || "").localeCompare(b.name || "");
          break;
        case "sender":
          cmp = partyKey(a, "sender").localeCompare(partyKey(b, "sender"));
          break;
        case "receiver":
          cmp = partyKey(a, "receiver").localeCompare(partyKey(b, "receiver"));
          break;
      }
      return sortAsc ? cmp : -cmp;
    });
    return sorted;
  });

  function setSort(k: SortKey) {
    if (sortKey === k) {
      sortAsc = !sortAsc;
    } else {
      sortKey = k;
      sortAsc = true;
    }
  }

  async function load() {
    loading = true;
    loadError = null;
    try {
      links = await api.listLinks(deviceAddress, locale);
    } catch (err) {
      loadError =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    void load();
  });

  async function onDelete(link: Link) {
    const ok = await confirmStore.ask({
      title: t("common.delete"),
      body: t("links.confirm_delete", {
        sender: link.sender_address,
        receiver: link.receiver_address,
      }),
      confirmLabel: t("common.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.removeLink(
        deviceAddress,
        link.sender_address,
        link.receiver_address,
      );
      toastStore.success(t("links.removed"));
      await load();
    } catch (err) {
      const msg =
        err instanceof ApiError
          ? `${err.status}: ${err.message}`
          : err instanceof Error
            ? err.message
            : String(err);
      toastStore.error(t("links.removal_failed"), msg);
    }
  }

  async function onAdded() {
    adding = false;
    toastStore.success(t("links.created"));
    await load();
  }

  function directionLabel(direction: string): string {
    return direction === "outgoing"
      ? t("links.outgoing_label")
      : t("links.incoming_label");
  }

  function partyLabel(
    deviceName: string | undefined,
    channelName: string | undefined,
    channelTypeLabel: string | undefined,
    address: string,
  ): string {
    const dev = deviceName?.trim() || address.split(":")[0];
    const ch =
      channelName?.trim() ||
      channelTypeLabel ||
      t("device.channel_n", { n: address.split(":")[1] ?? "" });
    return `${dev} · ${ch}`;
  }
</script>

{#if editing}
  <LinkConfigPanel
    link={editing}
    {locale}
    onBack={() => (editing = null)}
  />
{:else}
  <Card class="p-4">
    <header class="mb-4 flex items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold">{t("links.title")}</h2>
        <p class="text-xs text-[var(--ha-secondary-text-color)]">
          {loading
            ? t("common.loading")
            : `${links.length} ${t("links.links_label")}`}
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onclick={() => void load()}
          disabled={loading}
        >
          {t("common.reload")}
        </Button>
        <Button
          type="button"
          size="sm"
          onclick={() => (adding = !adding)}
          disabled={loading}
        >
          {adding ? t("common.cancel") : t("common.add")}
        </Button>
      </div>
    </header>

    {#if loadError}
      <div class="mb-4">
        <ErrorState message={loadError} onRetry={load} />
      </div>
    {/if}

    {#if adding}
      <AddLinkForm
        {deviceAddress}
        {interfaceId}
        {locale}
        onCancel={() => (adding = false)}
        onAdded={onAdded}
      />
    {/if}

    {#if links.length === 0 && !loading && !adding}
      <EmptyState message={t("links.no_for_device")} />
    {:else}
      {#if links.length > 1}
        <div class="mb-3 flex flex-wrap items-center gap-2 text-xs">
          <input
            type="search"
            bind:value={search}
            placeholder={t("common.search")}
            class="w-full rounded-md border border-slate-300 bg-white px-2 py-1 shadow-sm sm:w-56 dark:border-slate-700 dark:bg-slate-900"
          />
          <span class="text-[var(--ha-secondary-text-color)]">{t("common.sort")}</span>
          {#each [
            { k: "direction" as const, label: t("links.direction") },
            { k: "name" as const, label: t("links.name") },
            { k: "sender" as const, label: t("links.sender") },
            { k: "receiver" as const, label: t("links.receiver") },
          ] as col (col.k)}
            <button
              type="button"
              class="rounded-md border px-2 py-1 transition {sortKey === col.k
                ? 'border-brand-500 text-brand-700'
                : 'border-slate-300 text-slate-600 hover:border-slate-400 dark:border-slate-700 dark:text-slate-300'}"
              onclick={() => setSort(col.k)}
            >
              {col.label}
              {#if sortKey === col.k}
                <span aria-hidden="true">{sortAsc ? "↑" : "↓"}</span>
              {/if}
            </button>
          {/each}
          <span class="ml-auto text-[var(--ha-secondary-text-color)]">
            {visibleLinks.length} / {links.length}
          </span>
        </div>
      {/if}
      {#if visibleLinks.length === 0}
        <EmptyState message={t("common.no_matches")} />
      {/if}
      <ul class="space-y-2">
        {#each visibleLinks as link (link.sender_address + "->" + link.receiver_address)}
          <li class="flex items-center justify-between gap-3 rounded-md border border-slate-200 bg-white p-3 text-sm shadow-sm dark:border-slate-800 dark:bg-slate-900">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <Badge variant={link.direction === "outgoing" ? "default" : "muted"}>
                  {directionLabel(link.direction)}
                </Badge>
                {#if link.name}
                  <span class="font-medium">{link.name}</span>
                {/if}
              </div>
              <div class="mt-1 text-xs text-[var(--ha-secondary-text-color)]">
                <span>
                  {partyLabel(
                    link.sender_device_name,
                    link.sender_channel_name,
                    link.sender_channel_type_label,
                    link.sender_address,
                  )}
                </span>
                <span class="mx-2 text-[var(--ha-secondary-text-color)]">→</span>
                <span>
                  {partyLabel(
                    link.receiver_device_name,
                    link.receiver_channel_name,
                    link.receiver_channel_type_label,
                    link.receiver_address,
                  )}
                </span>
              </div>
              {#if link.description}
                <p class="mt-1 text-xs italic text-[var(--ha-secondary-text-color)]">
                  {link.description}
                </p>
              {/if}
            </div>
            <div class="flex flex-shrink-0 items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onclick={() => (editing = link)}
              >
                {t("links.configure")}
              </Button>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                onclick={() => void onDelete(link)}
              >
                {t("common.delete")}
              </Button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </Card>
{/if}
