<!--
  Fleet-wide list of direct links ("Programme und Verknüpfungen › Direkte
  Verknüpfungen" in the CCU WebUI). Aggregates every link across all
  configured centrals into one LinkTable; each row opens the link's own
  "Profileinstellung" page (#/links/<sender>/<receiver>), and "Neue
  Verknüpfung" starts the wizard.

  When the surface profile hides the link editor the listing stays and
  the rows lose their actions — see the `opens` relation in
  notes/concepts/ui-surface-profiles.md.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api, ApiError } from "$lib/api/client";
  import type { Link } from "$lib/api/types";
  import { surfacesStore } from "$lib/stores/surfaces.svelte";
  import Card from "$lib/components/ui/Card.svelte";
  import Button from "$lib/components/ui/Button.svelte";
  import Icon from "$lib/components/ui/Icon.svelte";
  import Input from "$lib/components/ui/Input.svelte";
  import PageHeader from "$lib/components/ui/PageHeader.svelte";
  import LoadingState from "$lib/components/ui/LoadingState.svelte";
  import EmptyState from "$lib/components/ui/EmptyState.svelte";
  import ErrorState from "$lib/components/ui/ErrorState.svelte";
  import PageShell from "$lib/components/ui/PageShell.svelte";
  import Select from "$lib/components/ui/Select.svelte";
  import LinkTable from "$lib/components/links/LinkTable.svelte";
  import { deleteLink } from "$lib/links/link-actions";
  import { newLinkHref } from "$lib/links/link-routes";
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
      link.sender_channel_name,
      link.receiver_channel_name,
      link.sender_channel_type_label,
      link.receiver_channel_type_label,
      link.central_name,
      link.interface_id,
    ]
      .filter(Boolean)
      .some((v) => (v as string).toLowerCase().includes(needle));
  }

  // Whether the link editor exists in this profile. Hidden means the rows
  // lose their actions, not that the cross-device listing loses its value.
  const linkable = $derived(surfacesStore.opensVisible("nav.links"));

  const filtered = $derived(
    links
      .filter((l) => !centralFilter || l.central_name === centralFilter)
      .filter((l) => matches(l, search)),
  );

  async function remove(link: Link) {
    if (await deleteLink(link)) await load();
  }
</script>

<svelte:head>
  <title>{t("page.title.links")}</title>
</svelte:head>

<PageShell>
  <PageHeader
    title={t("links.title")}
    subtitle={loading ? t("common.loading") : t("links.count", { count: filtered.length })}
  >
    {#snippet actions()}
      {#if centrals.length > 1}
        <Select
          class="w-auto"
          bind:value={centralFilter}
          ariaLabel={t("filter.central_aria")}
          options={[
            { value: "", label: t("common.all_ccus") },
            ...centrals.map((c) => ({ value: c, label: c })),
          ]}
        />
      {/if}
      {#if linkable}
        <Button type="button" onclick={() => (location.hash = newLinkHref())}>
          <Icon name="mdi:plus" size={16} />
          {t("links.new")}
        </Button>
      {/if}
    {/snippet}
  </PageHeader>

  {#if loadError}
    <ErrorState message={loadError} onRetry={load} class="mb-4" />
  {/if}

  {#if !linkable}
    <p
      class="mb-4 flex items-start gap-2 rounded-md border border-[var(--ha-divider-color)] bg-[var(--ha-card-background-color)] px-3 py-2 text-sm text-[var(--ha-secondary-text-color)]"
    >
      <Icon name="mdi:information-outline" class="mt-0.5 shrink-0" />
      <span>{t("links.editor_hidden")}</span>
    </p>
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
      <Card class="p-4">
        <LinkTable
          links={filtered}
          persistKey="links"
          showCentral={centrals.length > 1}
          editable={linkable}
          onDelete={remove}
          emptyMessage={t("links.no_matches")}
        />
      </Card>
    {/if}
  {/if}
</PageShell>
