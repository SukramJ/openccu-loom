<script lang="ts">
  // A device's direct links: the fleet-wide LinkTable narrowed to the links
  // this device takes part in — the CCU WebUI's "Direkte" button on a
  // device row, which opens its link list filtered to that device. Editing
  // happens on each link's own page; "Neue Verknüpfung" starts the wizard
  // on this device's channels.
  import type { Link } from "$lib/api/types";
  import { api, ApiError } from "$lib/api/client";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import LinkTable from "./LinkTable.svelte";
  import { deleteLink } from "$lib/links/link-actions";
  import { newLinkHref } from "$lib/links/link-routes";
  import { t } from "$lib/i18n";

  type Props = {
    deviceAddress: string;
    locale: string;
  };

  let { deviceAddress, locale }: Props = $props();

  let links = $state<Link[]>([]);
  let loading = $state(true);
  let loadError = $state<string | null>(null);

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
    void deviceAddress;
    void load();
  });

  async function remove(link: Link) {
    if (await deleteLink(link)) await load();
  }
</script>

<Card class="p-4">
  <header class="mb-4 flex flex-wrap items-center justify-between gap-3">
    <div>
      <h2 class="text-lg font-semibold">{t("links.title")}</h2>
      <p class="text-xs text-[var(--ha-secondary-text-color)]">
        {loading ? t("common.loading") : t("links.count", { count: links.length })}
      </p>
    </div>
    <div class="flex items-center gap-2">
      <Button type="button" variant="outline" size="sm" onclick={() => void load()} disabled={loading}>
        {t("common.reload")}
      </Button>
      <Button
        type="button"
        size="sm"
        onclick={() => (location.hash = newLinkHref({ device: deviceAddress }))}
      >
        <Icon name="mdi:plus" size={16} />
        {t("links.new")}
      </Button>
    </div>
  </header>

  {#if loadError}
    <div class="mb-4">
      <ErrorState message={loadError} onRetry={load} />
    </div>
  {/if}

  {#if links.length === 0 && !loading}
    <EmptyState message={t("links.no_for_device")} />
  {:else if links.length > 0}
    <LinkTable
      {links}
      persistKey="device-links"
      onDelete={remove}
      emptyMessage={t("common.no_matches")}
    />
  {/if}
</Card>
